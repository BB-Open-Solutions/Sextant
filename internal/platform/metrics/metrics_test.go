package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func grepLines(s, substr string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
