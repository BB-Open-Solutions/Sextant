package web_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestRolloutPlanEditor(t *testing.T) {
	ts, cfg := newConsole(t)
	post := func(path string, form url.Values) *http.Response {
		t.Helper()
		form.Set("csrf", "dev-csrf")
		resp, err := client().PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	// Save a two-ring plan.
	if resp := post("/rollout/plan", url.Values{
		"group0": {"pilot"}, "soak0": {"30"}, "healthy0": {"90"},
	}); resp.StatusCode != 303 {
		t.Fatalf("save plan = %d", resp.StatusCode)
	}
	f := cfg.Fleet()
	if f.Rollout == nil || len(f.Rollout.Rings) != 1 ||
		f.Rollout.Rings[0].SoakMinutes != 30 || f.Rollout.Rings[0].MinHealthyPercent != 90 {
		t.Fatalf("plan = %+v", f.Rollout)
	}

	// Unknown group refused; empty form clears.
	if resp := post("/rollout/plan", url.Values{"group0": {"ghost"}}); resp.StatusCode != 400 {
		t.Fatalf("ghost ring = %d, want 400", resp.StatusCode)
	}
	if resp := post("/rollout/plan", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("clear plan = %d", resp.StatusCode)
	}
	if cfg.Fleet().Rollout != nil {
		t.Fatal("plan not cleared")
	}
}
