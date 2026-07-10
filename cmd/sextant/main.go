// Command sextant runs the fleet control-plane server. main is lifecycle
// only: parse config, build capabilities, mount them through the registry
// (ADR 0006), run until SIGTERM/SIGINT. All logic lives in the packages it
// belongs to.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/mw"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/capability"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/config"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/health"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/logging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/metrics"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/server"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", checks.Liveness())
	mux.Handle("GET /readyz", checks.Readiness())
	mux.Handle("GET /metrics", m.Handler())

	caps, cleanup, err := buildCapabilities(ctx, cfg, log, checks)
	if err != nil {
		return err
	}
	for _, c := range cleanup {
		defer c()
	}
	mounted := capability.Mount(mux, log, caps...)

	// Without a console, the root serves a plain identity line.
	if !slices.Contains(mounted, "console") {
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
