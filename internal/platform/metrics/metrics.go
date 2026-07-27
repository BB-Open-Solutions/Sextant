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

	upstreamCheck prometheus.Gauge
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
		upstreamCheck: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sextant_upstream_last_check_timestamp_seconds",
			Help: "Unix time of the last completed upstream check (0 = never).",
		}),
	}
	reg.MustRegister(m.requests, m.duration, m.inflight, m.upstreamCheck)
	return m
}

// Handler serves the /metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// SetBuildInfo registers the sextant_build_info identity gauge (constant 1;
// the labels are the payload, the Prometheus build-info convention). PII-free
// by design: version, schema version and gate mode only - fit for an
// operator's dashboard without ever touching customer data (ADR 0009).
func (m *Metrics) SetBuildInfo(version, fleetModelVersion, gateMode string) {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "sextant_build_info",
		Help: "Build and deployment identity; value is always 1.",
		ConstLabels: prometheus.Labels{
			"version":             version,
			"fleet_model_version": fleetModelVersion,
			"gate_mode":           gateMode,
		},
	})
	g.Set(1)
	m.registry.MustRegister(g)
}

// UpstreamChecked records a completed upstream check. Only the leader replica
// runs the watcher, so aggregate with max() across pods.
func (m *Metrics) UpstreamChecked(t time.Time) {
	m.upstreamCheck.Set(float64(t.Unix()))
}

// RegisterActiveRings registers sextant_rollout_active_rings, evaluated at
// scrape time so every replica reports the same shared-store truth.
func (m *Metrics) RegisterActiveRings(f func() float64) {
	m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "sextant_rollout_active_rings",
		Help: "Rings promoted by the rollout run still in flight (0 when idle).",
	}, f))
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
