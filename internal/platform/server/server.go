// Package server owns the HTTP server lifecycle: hardened timeouts and a
// graceful shutdown that drains in-flight requests. Writes in Sextant end in
// git commits, so cutting a request mid-flight is never acceptable on a
// normal SIGTERM.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Options tune the server; zero values fall back to safe defaults.
type Options struct {
	// ShutdownGrace bounds how long Run waits for in-flight requests after
	// the context is cancelled. Default 15s.
	ShutdownGrace time.Duration
	// ReadHeaderTimeout guards against slow-loris header writes. Default 10s.
	ReadHeaderTimeout time.Duration
	// ReadTimeout bounds reading the full request including body. Default 60s.
	ReadTimeout time.Duration
	// WriteTimeout bounds writing the full response. Default 60s.
	WriteTimeout time.Duration
	// IdleTimeout closes idle keep-alive connections. Default 120s.
	IdleTimeout time.Duration
}

func (o Options) withDefaults() Options {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.ShutdownGrace, 15*time.Second)
	def(&o.ReadHeaderTimeout, 10*time.Second)
	def(&o.ReadTimeout, 60*time.Second)
	def(&o.WriteTimeout, 60*time.Second)
	def(&o.IdleTimeout, 120*time.Second)
	return o
}

// Server is a lifecycle-managed HTTP server.
type Server struct {
	srv   *http.Server
	grace time.Duration
	log   *slog.Logger

	// addrCh reports the bound address once listening (useful with ":0").
	addrCh chan string
}

// New builds a Server with hardened timeouts.
func New(addr string, handler http.Handler, log *slog.Logger, opts Options) *Server {
	opts = opts.withDefaults()
	return &Server{
		srv: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: opts.ReadHeaderTimeout,
			ReadTimeout:       opts.ReadTimeout,
			WriteTimeout:      opts.WriteTimeout,
			IdleTimeout:       opts.IdleTimeout,
		},
		grace:  opts.ShutdownGrace,
		log:    log,
		addrCh: make(chan string, 1),
	}
}

// Addr returns the bound listen address once Run has started listening, or
// "" if Run failed to listen. A receive from a closed channel yields the
// zero value, so this cannot block forever on a listen failure.
func (s *Server) Addr() string { return <-s.addrCh }

// Run serves until ctx is cancelled, then shuts down gracefully within the
// configured grace period. It returns nil on a clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		// Unblock any Addr() caller instead of leaving it waiting forever
		// on a send that will now never happen.
		close(s.addrCh)
		return fmt.Errorf("listen %s: %w", s.srv.Addr, err)
	}
	s.addrCh <- ln.Addr().String()
	s.log.Info("http server listening", "addr", ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		// Serve never returns nil; ErrServerClosed only after Shutdown, which
		// we did not call on this path, so this is a real failure.
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	s.log.Info("shutting down", "grace", s.grace.String())
	shutCtx, cancel := context.WithTimeout(context.Background(), s.grace)
	defer cancel()
	if err := s.srv.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	s.log.Info("shutdown complete")
	return nil
}
