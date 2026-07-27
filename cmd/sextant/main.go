// Command sextant runs the fleet control-plane server. main is lifecycle
// only: parse config, build capabilities, mount them through the registry
// (ADR 0006), run until SIGTERM/SIGINT. All logic lives in the packages it
// belongs to.
package main

import (
	"context"

	// Embed the IANA zone database: user timezone preferences must resolve
	// even in a container image without tzdata.
	_ "time/tzdata"

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

// version is the release identity shown in sextant_build_info. Injected at
// image build time via -ldflags "-X main.version=..."; "dev" outside CI.
var version = "dev"

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
	checks.SetLogger(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", checks.Liveness())
	mux.Handle("GET /readyz", checks.Readiness())
	mux.Handle("GET /status", checks.StatusPage())
	mux.Handle("GET /metrics", m.Handler())

	caps, cleanup, err := buildCapabilities(ctx, cfg, log, checks, m)
	// Release anything already opened even when the build failed partway
	// (buildCapabilities returns its partial cleanup on error).
	for _, c := range cleanup {
		defer c()
	}
	if err != nil {
		return err
	}
	mounted := capability.Mount(mux, log, caps...)

	// Without a console, the root serves a plain identity line.
	if !slices.Contains(mounted, "console") {
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = fmt.Fprintln(w, "Sextant - declarative fleet control-plane for NixOS")
		})
	}

	handler := mw.Chain(mux,
		mw.Recover(log),
		m.Middleware,
		mw.AccessLog(log),
		mw.SecureHeaders(cfg.SecureCookies),
	)

	srv := server.New(cfg.Addr, handler, log, server.Options{ShutdownGrace: cfg.ShutdownGrace})
	return srv.Run(ctx)
}
