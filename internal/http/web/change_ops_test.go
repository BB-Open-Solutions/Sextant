package web_test

import (
	"fmt"
	"net/url"
	"testing"
)

func TestRolloutPlanEditor(t *testing.T) {
	ts, cfg := newConsole(t)
	post := func(path string, form url.Values) int {
		t.Helper()
		form.Set("csrf", "dev-csrf")
		resp, err := client().PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Save a two-ring plan.
	if code := post("/rollout/plan", url.Values{
		"group0": {"pilot"}, "soak0": {"30"}, "healthy0": {"90"},
	}); code != 303 {
		t.Fatalf("save plan = %d", code)
	}
	f := cfg.Fleet()
	if f.Rollout == nil || len(f.Rollout.Rings) != 1 ||
		f.Rollout.Rings[0].SoakMinutes != 30 || f.Rollout.Rings[0].MinHealthyPercent != 90 {
		t.Fatalf("plan = %+v", f.Rollout)
	}

	// A ring at a high index must survive: rows are dynamic, the old
	// fixed count of five silently deleted rings 6+ on save.
	deep := url.Values{"group6": {"pilot"}}
	for i := 0; i < 6; i++ {
		deep.Set(fmt.Sprintf("group%d", i), "")
	}
	if code := post("/rollout/plan", deep); code != 303 {
		t.Fatalf("deep ring = %d", code)
	}
	if got := cfg.Fleet().Rollout; got == nil || len(got.Rings) != 1 || got.Rings[0].Group != "pilot" {
		t.Fatalf("deep ring plan = %+v", got)
	}

	// Unknown group refused; empty form clears.
	if code := post("/rollout/plan", url.Values{"group0": {"ghost"}}); code != 400 {
		t.Fatalf("ghost ring = %d, want 400", code)
	}
	if code := post("/rollout/plan", url.Values{}); code != 303 {
		t.Fatalf("clear plan = %d", code)
	}
	if cfg.Fleet().Rollout != nil {
		t.Fatal("plan not cleared")
	}
}
