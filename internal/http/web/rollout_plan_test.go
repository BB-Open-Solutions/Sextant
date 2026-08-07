package web_test

import (
	"net/http"
	"net/url"
	"testing"
)

// rollout_plan_test.go covers the form that defines the rollout ladder: how
// many waves, which group each is, how long it soaks and how healthy it has
// to be before the next one goes. It was the largest uncovered block left in
// the logic layer, and every value on it decides how carefully a change
// reaches a fleet.

func planPost(t *testing.T, base string, form url.Values) *http.Response {
	t.Helper()
	form.Set("csrf", "dev-csrf")
	resp, err := client().PostForm(base+"/rollout/plan", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestDefiningTheRolloutLadder(t *testing.T) {
	ts, cfg, _ := newChangeConsole(t)

	resp := planPost(t, ts.URL, url.Values{
		"group0": {"test"}, "name0": {"Test devices"}, "soak0": {"60"}, "approval0": {"1"},
		"group1": {"small"}, "name1": {"Wave 1"}, "soak1": {"30"}, "healthy1": {"95"},
		"group2": {"big"}, "name2": {"Wave 2"}, "maxDevices2": {"10"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("save plan = %d", resp.StatusCode)
	}

	pol := cfg.Fleet().Rollout
	if pol == nil || len(pol.Rings) != 3 {
		t.Fatalf("plan = %+v, want three rings", pol)
	}
	if pol.Rings[0].Group != "test" || pol.Rings[0].SoakMinutes != 60 || !pol.Rings[0].RequireApproval {
		t.Errorf("first ring = %+v", pol.Rings[0])
	}
	if pol.Rings[1].MinHealthyPercent != 95 {
		t.Errorf("the health floor did not land: %+v", pol.Rings[1])
	}
	if pol.Rings[2].MaxDevices != 10 {
		t.Errorf("the device cap did not land: %+v", pol.Rings[2])
	}
	// Order is the ladder. A plan whose waves are reordered promotes to the
	// wrong population, which is the one thing this form must not get wrong.
	if pol.Rings[1].Group != "small" || pol.Rings[2].Group != "big" {
		t.Errorf("ring order = %s, %s", pol.Rings[1].Group, pol.Rings[2].Group)
	}
}

// TestBlankRowsAreSkippedNotDropped: the page renders spare rows so an
// operator can add a wave without saving twice. A blank spare must be
// ignored - but a filled row AFTER a blank one must still arrive, or a
// wave silently disappears from the ladder.
func TestBlankRowsAreSkippedNotDropped(t *testing.T) {
	ts, cfg, _ := newChangeConsole(t)
	resp := planPost(t, ts.URL, url.Values{
		"group0": {"test"},
		"group1": {""}, // the spare
		"group2": {"big"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("save = %d", resp.StatusCode)
	}
	pol := cfg.Fleet().Rollout
	if pol == nil || len(pol.Rings) != 2 {
		t.Fatalf("plan = %+v, want two rings", pol)
	}
	if pol.Rings[1].Group != "big" {
		t.Errorf("the wave after the blank row was dropped: %+v", pol.Rings)
	}
}

func TestARolloutPlanRefusesValuesItCannotUse(t *testing.T) {
	ts, cfg, _ := newChangeConsole(t)
	cases := []struct {
		name string
		form url.Values
	}{
		{"soak that is not a number", url.Values{"group0": {"test"}, "soak0": {"an hour"}}},
		{"health that is not a number", url.Values{"group0": {"test"}, "healthy0": {"most"}}},
		{"negative device cap", url.Values{"group0": {"test"}, "maxDevices0": {"-1"}}},
		{"device cap that is not a number", url.Values{"group0": {"test"}, "maxDevices0": {"lots"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if resp := planPost(t, ts.URL, c.form); resp.StatusCode == 303 {
				t.Errorf("accepted %v", c.form)
			}
			if cfg.Fleet().Rollout != nil {
				t.Errorf("a refused plan was written anyway: %+v", cfg.Fleet().Rollout)
			}
		})
	}
}

// TestClearingThePlanIsPossible: an empty submission removes the ladder,
// which is how an organisation goes back to unstaged delivery.
func TestClearingThePlanIsPossible(t *testing.T) {
	ts, cfg, _ := newChangeConsole(t)
	if resp := planPost(t, ts.URL, url.Values{"group0": {"test"}}); resp.StatusCode != 303 {
		t.Fatal("could not set a plan to clear")
	}
	if cfg.Fleet().Rollout == nil {
		t.Fatal("precondition: no plan was set")
	}
	if resp := planPost(t, ts.URL, url.Values{"group0": {""}}); resp.StatusCode != 303 {
		t.Fatalf("clear = %d", resp.StatusCode)
	}
	if pol := cfg.Fleet().Rollout; pol != nil && len(pol.Rings) > 0 {
		t.Errorf("the plan survived clearing: %+v", pol)
	}
}
