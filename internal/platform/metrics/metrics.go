// Package metrics owns the Prometheus registry and the HTTP metrics that
// every request flows through. The registry is created here (not the global
// default) so tests can run multiple instances without collisions.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics bundles the registry and the request instruments.
type Metrics struct {
	registry *prometheus.Registry

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inflight prometheus.Gauge
}

// New creates a registry pre-populated with Go runtime and process collectors
// plus the HTTP request instruments.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sextant_http_requests_total",
			Help: "HTTP requests by route pattern, method and status class.",
		}, []string{"route", "method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sextant_http_request_duration_seconds",
			Help:    "HTTP request duration by route pattern and method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sextant_http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
	}
	reg.MustRegister(m.requests, m.duration, m.inflight)
	return m
}

// Handler serves the /metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Middleware instruments an http.Handler. Route label uses the mux pattern
// (r.Pattern, Go 1.22+), not the raw URL, to keep label cardinality bounded.
//
// The count/duration observation runs from a deferred func, not just after a
// normal return: mw.Recover sits OUTSIDE this middleware in the chain, so a
// handler panic unwinds straight through here without the two lines after
// next.ServeHTTP ever running - meaning the exact requests that become 500s
// were invisible in sextant_http_requests_total and the duration histogram.
// The defer observes on both the normal and the panicking path, then
// re-panics so Recover still catches it and writes the response.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		m.inflight.Inc()
		defer m.inflight.Dec()

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		panicked := true
		defer func() {
			if panicked {
				sw.status = http.StatusInternalServerError
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			m.requests.WithLabelValues(route, r.Method, strconv.Itoa(sw.status)).Inc()
			m.duration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
		}()
		next.ServeHTTP(sw, r)
		panicked = false
	})
}

// statusWriter records the response status for instrumentation.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController
// (Flush/Hijack/SetWriteDeadline) can reach it through this wrapper - every
// request is wrapped here AND by mw.AccessLog's statusWriter, and without an
// Unwrap chain a streaming/long-poll handler further down the chain would
// silently lose Flusher/Hijacker access.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
