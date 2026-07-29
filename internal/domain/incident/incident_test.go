package incident

import (
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
)

func TestDetect(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	obs := []Observation{
		// healthy: on target, online -> no incident
		{Tag: "ok-1", Group: "backoffice", Deployed: "rev-a", Target: "rev-a", Online: true, LastSeen: now},
		// behind: online but wrong revision
		{Tag: "behind-1", Group: "backoffice", Deployed: "rev-old", Target: "rev-a", Online: true, LastSeen: now},
		// offline
		{Tag: "off-1", Group: "field", Deployed: "rev-a", Target: "rev-a", Online: false, LastSeen: now.Add(-observed.InactiveWindow - time.Hour)},
		// offline but within the inactive window: a vacation laptop, NOT an
		// incident (operator decision 2026-07-29).
		{Tag: "vacation-1", Group: "field", Deployed: "rev-a", Target: "rev-a", Online: false, LastSeen: now.Add(-time.Hour)},
		// never seen
		{Tag: "new-1", Group: "field", Deployed: "", Target: "", Online: false},
		// errored (critical)
		{Tag: "err-1", Group: "backoffice", Deployed: "rev-a", Target: "rev-a", Online: true, LastSeen: now, Error: "build failed"},
		// wipe failed (critical)
		{Tag: "wipe-1", Group: "", Deployed: "rev-a", Online: true, LastSeen: now, Ack: "wipe-failed"},
	}
	got := Detect(obs, now)

	kinds := map[string]Kind{}
	for _, i := range got {
		kinds[i.Tag] = i.Kind
	}
	if _, ok := kinds["ok-1"]; ok {
		t.Error("healthy device should raise no incident")
	}
	if kinds["behind-1"] != Behind {
		t.Errorf("behind-1 kind = %q", kinds["behind-1"])
	}
	if kinds["off-1"] != Offline {
		t.Errorf("off-1 kind = %q", kinds["off-1"])
	}
	if _, hit := kinds["vacation-1"]; hit {
		t.Errorf("vacation-1 raised an incident; offline within the window must stay quiet")
	}
	if kinds["new-1"] != NeverSeen {
		t.Errorf("new-1 kind = %q", kinds["new-1"])
	}

	// Critical incidents sort first.
	if got[0].Severity != Critical {
		t.Fatalf("most-severe first violated: %+v", got[0])
	}
	// Scope key: grouped device -> group:<g>, ungrouped -> org.
	for _, i := range got {
		if i.Tag == "wipe-1" && i.Scope != "org" {
			t.Errorf("ungrouped incident scope = %q, want org", i.Scope)
		}
		if i.Tag == "behind-1" && i.Scope != "group:backoffice" {
			t.Errorf("grouped incident scope = %q", i.Scope)
		}
	}
}

func TestDetectBehindNeedsTarget(t *testing.T) {
	now := time.Unix(1000, 0)
	// No target (following HEAD): a differing deployed revision is NOT judged behind.
	got := Detect([]Observation{
		{Tag: "d", Group: "g", Deployed: "x", Target: "", Online: true, LastSeen: now},
	}, now)
	for _, i := range got {
		if i.Kind == Behind {
			t.Fatal("device with no target must not be flagged behind")
		}
	}
}

func TestDetectRollout(t *testing.T) {
	promoted := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	base := RunObservation{Ring: "Canary", Target: "abcdef1234567890", Since: promoted}
	cases := []struct {
		name    string
		stalled time.Duration
		tags    []string
		want    bool // an incident is raised
		detail  []string
	}{
		{
			name:    "well inside the window stays quiet",
			stalled: time.Minute,
		},
		{
			name:    "one minute short of the window stays quiet",
			stalled: rollout.StallWindow - time.Minute,
		},
		{
			name:    "at the window it speaks",
			stalled: rollout.StallWindow,
			tags:    []string{"dev-1"},
			want:    true,
			detail:  []string{"45m", "abcdef123456", "dev-1"},
		},
		{
			name:    "past the window it names the off-target devices",
			stalled: 80 * time.Minute,
			tags:    []string{"dev-1", "dev-2"},
			want:    true,
			detail:  []string{"1h20m", "2 device(s)", "dev-1, dev-2"},
		},
		{
			name:    "a long list is capped at three",
			stalled: 2 * time.Hour,
			tags:    []string{"dev-1", "dev-2", "dev-3", "dev-4", "dev-5"},
			want:    true,
			detail:  []string{"dev-1, dev-2, dev-3 and 2 more"},
		},
		{
			name:    "no off-target list still reports the stall",
			stalled: 2 * time.Hour,
			want:    true,
			detail:  []string{"2h00m"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := base
			run.Stalled, run.OffTarget = c.stalled, c.tags
			got := DetectRollout(run)
			if !c.want {
				if len(got) != 0 {
					t.Fatalf("raised %d incident(s) below the stall window: %+v", len(got), got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d incidents, want 1", len(got))
			}
			in := got[0]
			if in.Kind != RolloutStalled || in.Severity != Warning {
				t.Errorf("kind/severity = %q/%d", in.Kind, in.Severity)
			}
			if in.Scope != "org" || in.Tag != "" {
				t.Errorf("a fleet-level incident must be org-scoped and tagless: %+v", in)
			}
			if in.Title != "Rollout stalled on ring Canary" {
				t.Errorf("title = %q", in.Title)
			}
			if !in.Since.Equal(promoted) {
				t.Errorf("since = %s, want the promotion time %s", in.Since, promoted)
			}
			if in.Action == "" {
				t.Error("a stalled run must carry an action")
			}
			for _, want := range c.detail {
				if !strings.Contains(in.Detail, want) {
					t.Errorf("detail %q does not mention %q", in.Detail, want)
				}
			}
		})
	}
}

func TestDetectUnknownConfig(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	// The wrong-source signature: online, on a revision the repo cannot
	// place, while the console demonstrably CAN place the pin and its tip.
	stray := Observation{
		Tag: "d", Group: "g", Deployed: "ffff0000ffff0000", DeployedRelease: 0,
		Target: "rev-target", TargetRelease: 145,
		Head: "rev-head", HeadRelease: 146,
		Online: true, LastSeen: now,
	}
	mutate := func(f func(*Observation)) Observation {
		o := stray
		f(&o)
		return o
	}
	cases := []struct {
		name string
		obs  Observation
		want bool
	}{
		{name: "device on an unrecognised revision", obs: stray, want: true},
		{
			name: "an ordinary behind device is behind, not unrecognised",
			obs:  mutate(func(o *Observation) { o.Deployed, o.DeployedRelease = "rev-old", 142 }),
		},
		{
			name: "device on target",
			obs:  mutate(func(o *Observation) { o.Deployed, o.DeployedRelease = "rev-target", 145 }),
		},
		{
			name: "device on target whose release lookup lagged",
			obs:  mutate(func(o *Observation) { o.Deployed = "rev-target" }),
		},
		{
			name: "offline device proves nothing",
			obs:  mutate(func(o *Observation) { o.Online = false }),
		},
		{
			name: "device sitting on the repo's own tip before it is counted",
			obs:  mutate(func(o *Observation) { o.Deployed = "rev-head" }),
		},
		{
			name: "console cannot count releases at all (broken lookup)",
			obs:  mutate(func(o *Observation) { o.TargetRelease, o.HeadRelease = 0, 0 }),
		},
		{
			name: "console has no readable tip",
			obs:  mutate(func(o *Observation) { o.Head, o.HeadRelease = "", 0 }),
		},
		{
			name: "device following HEAD (no pin) is never judged",
			obs:  mutate(func(o *Observation) { o.Target, o.TargetRelease = "", 0 }),
		},
		{
			name: "device that has not reported a revision",
			obs:  mutate(func(o *Observation) { o.Deployed = "" }),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var hit *Incident
			got := Detect([]Observation{c.obs}, now)
			for i := range got {
				if got[i].Kind == UnknownConfig {
					hit = &got[i]
				}
			}
			if c.want != (hit != nil) {
				t.Fatalf("unknown-config raised = %v, want %v", hit != nil, c.want)
			}
			if hit == nil {
				return
			}
			if hit.Severity != Warning {
				t.Errorf("severity = %d, want warning", hit.Severity)
			}
			if hit.Title != "d runs an unrecognised configuration" {
				t.Errorf("title = %q", hit.Title)
			}
			if !strings.Contains(hit.Detail, "ffff0000ffff") {
				t.Errorf("detail %q does not name the revision", hit.Detail)
			}
			if hit.Scope != "group:g" {
				t.Errorf("scope = %q", hit.Scope)
			}
		})
	}
}

// A device following an unknown source must not ALSO be reported behind:
// "the update has not landed, check the rollout" is the wrong instruction
// for a device that is not listening to the rollout.
func TestUnknownConfigSupersedesBehind(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	got := Detect([]Observation{{
		Tag: "d", Group: "g", Deployed: "ffff0000ffff0000",
		Target: "rev-target", TargetRelease: 145,
		Head: "rev-head", HeadRelease: 146,
		Online: true, LastSeen: now,
	}}, now)
	for _, in := range got {
		if in.Kind == Behind {
			t.Fatalf("device on an unknown revision also reported behind: %+v", in)
		}
	}
}
