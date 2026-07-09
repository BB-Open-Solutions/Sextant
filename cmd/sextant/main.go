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

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/nix"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/api"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/mw"
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
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "Sextant - declarative fleet control-plane for NixOS")
	})

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
		// The convergence source arrives with the observed plane (Postgres);
		// until then a rollout can start but ticks report the gap honestly.
		rollouts := app.NewRolloutService(svc, st.Rollouts(),
			noConvergence{}, clock, log)
		go rollouts.Run(ctx, 30*time.Second)

		api.New(api.Services{Config: svc, Changes: changes, Rollouts: rollouts},
			cfg.APIToken, cfg.Write, log).Routes(mux)
		checks.Register("config-repo", func(context.Context) error {
			_, err := repo.ReadFile(app.FleetFile)
			return err
		})
		log.Info("config plane mounted", "repo", cfg.RepoDir,
			"write", cfg.Write, "gate", cfg.GateMode, "remote", cfg.GitRemote,
			"state", stateDir, "api", cfg.APIToken != "")
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
