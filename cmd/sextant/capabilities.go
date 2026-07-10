package main

// capabilities.go wires the product's capabilities (ADR 0006): one
// constructor per capability, mounted through the registry. main.go stays
// lifecycle-only. As capabilities grow their own packages (M3), their
// constructors move with them; the registry stays.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"golang.org/x/time/rate"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	ldapdir "code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/ldap"
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
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/capability"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/config"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/health"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// deps carries what the capability constructors share.
type deps struct {
	ctx    context.Context
	cfg    *config.Config
	log    *slog.Logger
	checks *health.Registry

	svc      *app.ConfigService
	changes  *app.ChangeService
	rollouts *app.RolloutService
	inv      *app.InventoryService
	tokens   *app.TokenService
	devCreds *app.DeviceCredentials
	prefs    ports.PrefsStore
	dir      ports.Directory
	authz    api.Authz
	cleanup  []func()
}

// buildCapabilities wires the config plane and returns the registry list
// plus cleanup functions (run at shutdown).
func buildCapabilities(ctx context.Context, cfg *config.Config, log *slog.Logger, checks *health.Registry) ([]capability.Capability, []func(), error) {
	if cfg.RepoDir == "" {
		return nil, nil, nil // health/metrics-only deployment
	}
	d := &deps{ctx: ctx, cfg: cfg, log: log, checks: checks}
	if err := d.buildConfigPlane(); err != nil {
		return nil, nil, err
	}
	caps := []capability.Capability{
		d.observedCapability(),
		d.authCapability(), // mounts sessions; console reads them at mount time
		d.consoleCapability(),
		d.apiCapability(),
	}
	return caps, d.cleanup, nil
}

// buildConfigPlane constructs the services every capability shares.
func (d *deps) buildConfigPlane() error {
	cfg, log := d.cfg, d.log

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
	d.svc = svc

	// The remote is the source of truth: keep the snapshot in sync with
	// commits made outside this console (engineers, other tools).
	if repo.HasRemote() {
		go svc.SyncLoop(d.ctx, 30*time.Second, log)
		log.Info("remote sync loop started", "remote", cfg.GitRemote)
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
	d.changes = app.NewChangeService(repo, st.Changes(), gate, builder, clock, openWT, svc)

	d.authz = api.Authz{
		BaselineViewer: cfg.ViewerGroups,
		BaselineEditor: cfg.EditorGroups,
		BaselineOwner:  cfg.OwnerGroups,
	}

	var conv ports.ConvergenceSource = noConvergence{}
	if cfg.PgDSN != "" {
		pg, err := postgres.Open(d.ctx, cfg.PgDSN)
		if err != nil {
			return err
		}
		d.cleanup = append(d.cleanup, pg.Close)
		d.inv = app.NewInventoryService(pg, pg, clock, app.DefaultTenant)
		d.tokens = app.NewTokenService(pg.Tokens(), clock, 0)
		d.devCreds = app.NewDeviceCredentials(pg.Tokens(), clock)
		d.prefs = pg
		d.authz.Tokens = d.tokens // scoped tokens (ADR 0008); break-glass token still works
		conv = pg.NewConvergence(app.DefaultTenant, func(group string) []string {
			// Retired devices never converge; counting them stalls a ring.
			return svc.Fleet().ActiveGroupDevices(group)
		})
		d.checks.Register("postgres", pg.Ping)
	}

	// Directory browse: the login IdP (OIDC) and the group source (LDAP)
	// may differ; LDAP only ever lists groups for binding pickers.
	if cfg.LDAPURL != "" {
		dir, err := ldapdir.New(ldapdir.Config{
			URL: cfg.LDAPURL, BindDN: cfg.LDAPBindDN, BindPassword: cfg.LDAPBindPass,
			BaseDN: cfg.LDAPBaseDN, GroupFilter: cfg.LDAPGroupFilter, NameAttr: cfg.LDAPNameAttr,
		})
		if err != nil {
			return err
		}
		d.dir = dir
		log.Info("directory browse mounted", "ldap", cfg.LDAPURL, "base", cfg.LDAPBaseDN)
	}

	d.rollouts = app.NewRolloutService(svc, st.Rollouts(), conv, clock, log)
	go d.rollouts.Run(d.ctx, 30*time.Second)
	d.checks.Register("config-repo", func(context.Context) error {
		_, err := repo.ReadFile(app.FleetFile)
		return err
	})
	log.Info("config plane mounted", "repo", cfg.RepoDir, "write", cfg.Write,
		"gate", cfg.GateMode, "remote", cfg.GitRemote, "state", stateDir)
	return nil
}

// observedCapability serves the device-facing check-in (rate limited).
func (d *deps) observedCapability() capability.Capability {
	return capability.Func{
		CapName:   "observed",
		EnabledFn: func() bool { return d.inv != nil },
		RoutesFn: func(mux *http.ServeMux) {
			inner := http.NewServeMux()
			api.NewCheckin(d.inv, d.devCreds, d.cfg.CheckinToken).
				WithLifecycle(func(tag string) bool {
					dev, ok := d.svc.Fleet().Devices[tag]
					return ok && dev.Retired()
				}).Routes(inner)
			mux.Handle("POST /api/checkin", mw.RateLimit(rate.Limit(20), 40)(inner))
		},
	}
}

// authCapability mounts session auth: OIDC or the loopback dev stub.
func (d *deps) authCapability() capability.Capability {
	return capability.Func{
		CapName:   "auth",
		EnabledFn: func() bool { return d.cfg.OIDCIssuer != "" || d.cfg.DevAuth },
		RoutesFn: func(mux *http.ServeMux) {
			if d.cfg.DevAuth {
				d.log.Warn("dev auth enabled: synthetic owner session, loopback only")
				d.authz.Sessions = web.DevSessions{}
				mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "/login", http.StatusSeeOther)
				})
				return
			}
			authn, err := oidc.New(d.ctx, oidc.Config{
				Issuer:       d.cfg.OIDCIssuer,
				ClientID:     d.cfg.OIDCClientID,
				ClientSecret: d.cfg.OIDCClientSecret,
				RedirectURL:  d.cfg.OIDCRedirectURL,
				GroupsClaim:  d.cfg.OIDCGroupsClaim,
				Scopes:       d.cfg.OIDCScopes,
				SessionKey:   d.cfg.SessionKey,
				Secure:       d.cfg.SecureCookies,
				Authorize: func(u identity.User) bool {
					return d.svc.Fleet().IdentityResolver(
						d.cfg.ViewerGroups, d.cfg.EditorGroups, d.cfg.OwnerGroups).CanViewAnything(u)
				},
			})
			if err != nil {
				// Fail loudly: readiness carries the failure so the pod
				// never reports healthy without its login flow.
				d.log.Error("oidc discovery failed; session auth NOT mounted", "err", err)
				d.checks.Register("oidc", func(context.Context) error { return err })
				return
			}
			inner := http.NewServeMux()
			authn.Routes(inner)
			limited := mw.RateLimit(rate.Limit(2), 10)(inner)
			mux.Handle("GET /login/start", limited)
			mux.Handle("GET /callback", limited)
			mux.Handle("POST /logout", limited)
			d.authz.Sessions = authn
			d.log.Info("oidc session auth mounted", "issuer", d.cfg.OIDCIssuer)
		},
	}
}

// consoleCapability mounts the human SSR console when sessions exist.
func (d *deps) consoleCapability() capability.Capability {
	return capability.Func{
		CapName:   "console",
		EnabledFn: func() bool { return d.cfg.OIDCIssuer != "" || d.cfg.DevAuth },
		RoutesFn: func(mux *http.ServeMux) {
			if d.authz.Sessions == nil {
				d.log.Error("console skipped: no session source (oidc failed?)")
				return
			}
			console, err := web.New(
				web.Services{Config: d.svc, Changes: d.changes, Rollouts: d.rollouts,
					Inventory: d.inv, Tokens: d.tokens, Prefs: d.prefs,
					DevCreds: d.devCreds, Directory: d.dir},
				d.authz.Sessions.(web.Sessions), d.cfg.Write,
				d.cfg.ViewerGroups, d.cfg.EditorGroups, d.cfg.OwnerGroups, d.log)
			if err != nil {
				d.log.Error("console templates failed; console NOT mounted", "err", err)
				return
			}
			console.Routes(mux)
		},
	}
}

// apiCapability mounts /api/v1, the machine contract.
func (d *deps) apiCapability() capability.Capability {
	return capability.Func{
		CapName: "api",
		RoutesFn: func(mux *http.ServeMux) {
			api.New(api.Services{Config: d.svc, Changes: d.changes,
				Rollouts: d.rollouts, Inventory: d.inv, Tokens: d.tokens,
				DevCreds: d.devCreds, Prefs: d.prefs, Directory: d.dir},
				d.authz, d.cfg.APIToken, d.cfg.Write, d.log).Routes(mux)
		},
	}
}

// builderFunc adapts a function to ports.Builder (gate mode none).
type builderFunc func(ctx context.Context, repoDir string, hosts []string) error

func (f builderFunc) Build(ctx context.Context, repoDir string, hosts []string) error {
	return f(ctx, repoDir, hosts)
}

// noConvergence reports the observed plane as not configured, so a rollout
// cannot silently advance without real convergence data.
type noConvergence struct{}

func (noConvergence) RingStatus(context.Context, string, string) (rollout.RingStatus, error) {
	return rollout.RingStatus{}, fmt.Errorf("observed plane not configured, convergence unknown: %w", ports.ErrUnavailable)
}
