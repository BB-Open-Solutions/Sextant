// Package mw holds transport middleware: panic recovery, access logging and
// security headers. Each middleware does one thing; Chain composes them.
package mw

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares outermost-first: Chain(h, a, b) == a(b(h)).
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Recover converts a handler panic into a 500 and logs the stack, keeping one
// bad request from killing the process.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic in handler",
						"panic", rec,
						"method", r.Method,
						"path", r.URL.Path,
						"stack", string(debug.Stack()),
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog writes one structured line per request. Health probes are skipped
// to keep the log signal-dense.
//
// The log line is written from a deferred func so a handler panic (which
// unwinds straight past a plain post-ServeHTTP log call, since mw.Recover
// sits OUTSIDE this middleware) still produces one line - defaulting status
// to 500, then re-panicking so Recover still catches it and writes the
// response. Without this, the exact requests that become 500s were the ones
// missing from the access log.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			panicked := true
			defer func() {
				if panicked {
					sw.status = http.StatusInternalServerError
				}
				log.Info("request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", sw.status,
					"bytes", sw.bytes,
					"dur", time.Since(start).String(),
				)
			}()
			next.ServeHTTP(sw, r)
			panicked = false
		})
	}
}

// SecureHeaders sets the browser security baseline. The CSP allows only
// same-origin content: all assets are embedded and served by this binary.
// hsts emits Strict-Transport-Security: it must be driven by config, not by
// r.TLS, because the deployment terminates TLS at the ingress so the request
// this process sees is plain HTTP and r.TLS is always nil - gating on it would
// mean HSTS is never sent in production. Callers pass true when the service is
// reached over HTTPS (the same signal as Secure cookies).
func SecureHeaders(hsts bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy",
				"default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController
// can reach it through this wrapper (and through metrics.statusWriter, which
// wraps every request too) - needed for Flush/Hijack/SetWriteDeadline on any
// streaming or large-download handler further down the chain.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
