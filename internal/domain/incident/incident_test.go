package incident

import (
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
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
