// Command sextant runs the fleet control-plane server. main stays tiny: parse
// config, wire the components explicitly, run until SIGTERM/SIGINT, exit
// non-zero on failure. All logic lives in the packages it belongs to.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/nix"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/oidc"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/postgres"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/api"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/mw"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/config"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/health"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/logging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/metrics"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/server"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "sextant:", err)
		os.Exit(1)
	}
}

func run(args []string, getenv config.Getenv) error {
	cfg, err := config.Load(args, getenv)
	if err != nil {
		return err
	}
	log := logging.New(os.Stderr, cfg.LogFormat, cfg.LogLevel)

	m := metrics.New()
	checks := health.New(5 * time.Second)

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", checks.Liveness())
	mux.Handle("GET /readyz", checks.Readiness())
	mux.Handle("GET /metrics", m.Handler())
	consoleMounted := false

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Configuration plane: mounted when an overlay repo is given.
	if cfg.RepoDir != "" {
		repo, err := git.Open(cfg.RepoDir, cfg.GitRemote)
		if err != nil {
			return err
		}
		var gate ports.Gate = nix.NewEvalGate()
		var builder ports.Builder = nix.NewBuilder()
		if cfg.GateMode == "none" {
			log.Warn("validation gate disabled (--gate none): edits are not checked against the generator")
			gate = ports.GateFunc(func(context.Context, string, []string) error { return nil })
			builder = builderFunc(func(context.Context, string, []string) error { return nil })
		}
		svc, err := app.NewConfigService(repo, gate)
		if err != nil {
			return err
		}

		stateDir := cfg.StateDir
		if stateDir == "" {
			stateDir = filepath.Join(cfg.RepoDir, ".sextant-state")
		}
		st, err := state.Open(stateDir)
		if err != nil {
			return err
		}
		clock := app.SystemClock{}
		openWT := func(dir string) (ports.ConfigRepo, error) { return git.Open(dir, "") }
		changes := app.NewChangeService(repo, st.Changes(), gate, builder, clock, openWT, svc)

		// Observed plane: Postgres when a DSN is given; without it check-in
		// and status stay off and rollout ticks report the gap honestly.
		var conv ports.ConvergenceSource = noConvergence{}
		var inv *app.InventoryService
		if cfg.PgDSN != "" {
			pg, err := postgres.Open(ctx, cfg.PgDSN)
			if err != nil {
				return err
			}
			defer pg.Close()
			inv = app.NewInventoryService(pg, pg, clock, app.DefaultTenant)
			conv = pg.NewConvergence(app.DefaultTenant, func(group string) []string {
				return svc.Fleet().GroupDevices(group)
			})
			checks.Register("postgres", pg.Ping)
			// Check-in is device-facing and brute-forceable: rate limit it.
			checkinMux := http.NewServeMux()
			api.NewCheckin(inv, cfg.CheckinToken).Routes(checkinMux)
			mux.Handle("POST /api/checkin", mw.RateLimit(rate.Limit(20), 40)(checkinMux))
			log.Info("observed plane mounted", "checkin", cfg.CheckinToken != "")
		}

		rollouts := app.NewRolloutService(svc, st.Rollouts(), conv, clock, log)
		go rollouts.Run(ctx, 30*time.Second)

		// Console SSO: OIDC sessions on top of (or instead of) the token.
		authz := api.Authz{
			BaselineViewer: cfg.ViewerGroups,
			BaselineEditor: cfg.EditorGroups,
			BaselineOwner:  cfg.OwnerGroups,
		}
		if cfg.OIDCIssuer != "" {
			authn, err := oidc.New(ctx, oidc.Config{
				Issuer:       cfg.OIDCIssuer,
				ClientID:     cfg.OIDCClientID,
				ClientSecret: cfg.OIDCClientSecret,
				RedirectURL:  cfg.OIDCRedirectURL,
				GroupsClaim:  cfg.OIDCGroupsClaim,
				SessionKey:   cfg.SessionKey,
				Secure:       cfg.SecureCookies,
				Authorize: func(u identity.User) bool {
					return svc.Fleet().IdentityResolver(
						cfg.ViewerGroups, cfg.EditorGroups, cfg.OwnerGroups).CanViewAnything(u)
				},
			})
			if err != nil {
				return err
			}
			// The login flow is brute-forceable: rate limit it.
			authMux := http.NewServeMux()
			authn.Routes(authMux)
			limited := mw.RateLimit(rate.Limit(2), 10)(authMux)
			mux.Handle("GET /login/start", limited)
			mux.Handle("GET /callback", limited)
			mux.Handle("POST /logout", limited)
			authz.Sessions = authn
			log.Info("oidc session auth mounted", "issuer", cfg.OIDCIssuer)
		}

		if cfg.DevAuth {
			log.Warn("dev auth enabled: synthetic owner session, loopback only")
			authz.Sessions = web.DevSessions{}
			mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			})
		}

		// Human console (SSR) when session auth is available.
		if authz.Sessions != nil {
			console, err := web.New(
				web.Services{Config: svc, Changes: changes, Rollouts: rollouts, Inventory: inv},
				authz.Sessions.(web.Sessions), cfg.Write,
				cfg.ViewerGroups, cfg.EditorGroups, cfg.OwnerGroups, log)
			if err != nil {
				return err
			}
			console.Routes(mux)
			consoleMounted = true
			log.Info("console mounted")
		}

		api.New(api.Services{Config: svc, Changes: changes, Rollouts: rollouts, Inventory: inv},
			authz, cfg.APIToken, cfg.Write, log).Routes(mux)
		checks.Register("config-repo", func(context.Context) error {
			_, err := repo.ReadFile(app.FleetFile)
			return err
		})
		log.Info("config plane mounted", "repo", cfg.RepoDir,
			"write", cfg.Write, "gate", cfg.GateMode, "remote", cfg.GitRemote,
			"state", stateDir, "api", cfg.APIToken != "")
	}

	if !consoleMounted {
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "Sextant - declarative fleet control-plane for NixOS")
		})
	}

	handler := mw.Chain(mux,
		mw.Recover(log),
		m.Middleware,
		mw.AccessLog(log),
		mw.SecureHeaders(),
	)

	srv := server.New(cfg.Addr, handler, log, server.Options{ShutdownGrace: cfg.ShutdownGrace})
	return srv.Run(ctx)
}

// builderFunc adapts a function to ports.Builder (gate mode none).
type builderFunc func(ctx context.Context, repoDir string, hosts []string) error

func (f builderFunc) Build(ctx context.Context, repoDir string, hosts []string) error {
	return f(ctx, repoDir, hosts)
}

// noConvergence reports the observed plane as not yet configured, so a
// rollout cannot silently advance without real convergence data.
type noConvergence struct{}

func (noConvergence) RingStatus(context.Context, string, string) (rollout.RingStatus, error) {
	return rollout.RingStatus{}, fmt.Errorf("observed plane not configured, convergence unknown: %w", ports.ErrUnavailable)
}
