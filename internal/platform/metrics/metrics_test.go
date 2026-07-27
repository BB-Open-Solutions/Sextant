package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMiddlewareUsesRoutePattern proves label cardinality stays bounded: the
// route label must be the mux pattern, never the raw URL with its IDs.
func TestMiddlewareUsesRoutePattern(t *testing.T) {
	m := New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /devices/{tag}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(m.Middleware(mux))
	defer srv.Close()

	for _, tag := range []string{"laptop-1", "laptop-2", "laptop-3"} {
		resp, err := http.Get(srv.URL + "/devices/" + tag)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	out := string(body)

	if !strings.Contains(out, `route="GET /devices/{tag}"`) {
		t.Errorf("metrics missing pattern-labelled route:\n%s", grepLines(out, "requests_total"))
	}
	if strings.Contains(out, "laptop-1") {
		t.Error("raw URL leaked into metric labels (cardinality explosion)")
	}
}

func TestMiddlewareRecordsStatus(t *testing.T) {
	m := New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /missing", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(m.Middleware(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `status="404"`) {
		t.Error("404 status not recorded")
	}
}

// TestOperatorMetrics covers the three PII-free operator metrics (design
// 0005): deployment identity, watcher liveness, rollout pressure at scrape.
func TestOperatorMetrics(t *testing.T) {
	m := New()
	m.SetBuildInfo("1.2.3", "3", "remote")
	m.UpstreamChecked(time.Unix(1700000000, 0))
	rings := 0.0
	m.RegisterActiveRings(func() float64 { return rings })
	rings = 2

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	info := grepLines(body, "sextant_build_info{")
	for _, want := range []string{`version="1.2.3"`, `fleet_model_version="3"`, `gate_mode="remote"`} {
		if !strings.Contains(info, want) {
			t.Errorf("build_info misses %s: %q", want, info)
		}
	}
	if !strings.Contains(body, "sextant_upstream_last_check_timestamp_seconds 1.7e+09") {
		t.Errorf("upstream timestamp not exported:\n%s", grepLines(body, "upstream"))
	}
	// GaugeFunc reads at scrape time: the post-registration value must win.
	if !strings.Contains(body, "sextant_rollout_active_rings 2") {
		t.Errorf("active rings not scraped live:\n%s", grepLines(body, "active_rings"))
	}
}

// TestUpstreamGaugeDefaultsToZero: an idle deployment must still export the
// series (0 = never checked), or "watcher dead" and "metric absent" become
// indistinguishable for alerting.
func TestUpstreamGaugeDefaultsToZero(t *testing.T) {
	m := New()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "sextant_upstream_last_check_timestamp_seconds 0") {
		t.Error("upstream gauge not pre-registered at 0")
	}
}

func grepLines(s, substr string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
