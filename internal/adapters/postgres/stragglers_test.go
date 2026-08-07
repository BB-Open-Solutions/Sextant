package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// stragglers_test.go covers the list an operator reads when a ring will not
// reach 100%.
//
// The counts next to it are tested in postgres_test.go; this is about the
// reason strings, and they are not cosmetic. A wave that stalls because a
// laptop is shut for the weekend and a wave that stalls because a release is
// broken look identical in a percentage. The wording is what tells them
// apart, so the CASE arms and their order are pinned here.

func TestRingStragglersNamesEachReason(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	up := func(tag, rev, errmsg string, phase observed.Phase, seen time.Time) {
		t.Helper()
		if _, err := s.Upsert(ctx, "default",
			observed.CheckIn{Tag: tag, Revision: rev, Phase: phase, Error: errmsg}, seen); err != nil {
			t.Fatal(err)
		}
	}
	fresh := now.Add(-time.Minute)              // inside OnlineWindow
	quiet := now.Add(-10 * time.Minute)         // past online, inside absent
	away := now.Add(-2 * observed.AbsentWindow) // past absent

	up("ok", "v2", "", observed.Running, fresh)      // converged: must not appear
	up("behind", "v1", "", observed.Running, fresh)  // off target
	up("offline", "v2", "", observed.Running, quiet) // on target, gone quiet
	up("broken", "v2", "disk full", observed.Running, fresh)
	up("away", "v1", "", observed.Running, away) // absent AND off target
	// A device of another tenant with the same tag must not surface here.
	if _, err := s.Upsert(ctx, "org-x",
		observed.CheckIn{Tag: "neighbour", Revision: "v1"}, fresh); err != nil {
		t.Fatal(err)
	}

	conv := s.NewConvergence("default", func(group string) []string {
		if group == "ring0" {
			return []string{"ok", "behind", "offline", "broken", "away", "never", "neighbour"}
		}
		return nil
	})
	conv.Now = func() time.Time { return now }

	got, err := conv.RingStragglers(ctx, []string{"ring0"}, "v2")
	if err != nil {
		t.Fatalf("stragglers: %v", err)
	}
	reasons := map[string]string{}
	for _, st := range got {
		reasons[st.Tag] = st.Reason
	}

	if _, listed := reasons["ok"]; listed {
		t.Errorf("the converged device is listed as a straggler: %q", reasons["ok"])
	}
	// "neighbour" belongs to another tenant, so from this tenant's point of
	// view it has never checked in - it is a straggler, but on its own
	// account, and never with the neighbour's state.
	if r := reasons["neighbour"]; !strings.Contains(r, "not seen yet") {
		t.Errorf("neighbour reason = %q, want the never-seen wording; another tenant's row leaked", r)
	}

	for tag, want := range map[string]string{
		"never":   "not seen yet",
		"away":    "away",
		"behind":  "not on target yet",
		"offline": "offline",
		"broken":  "error: disk full",
	} {
		if got := reasons[tag]; !strings.Contains(got, want) {
			t.Errorf("%s: reason = %q, want it to mention %q", tag, got, want)
		}
	}

	// The precedence is the point: "away" is off target too, and reporting
	// it as off target would make a holiday look like a failed rollout.
	if r := reasons["away"]; strings.Contains(r, "not on target") {
		t.Errorf("away reason = %q; a shut laptop reads as a stalled release", r)
	}
	// And the two non-blocking reasons must say so, because an operator
	// decides whether to abort a wave on this text.
	for _, tag := range []string{"never", "away"} {
		if !strings.Contains(reasons[tag], "catches up") && !strings.Contains(reasons[tag], "joins on") {
			t.Errorf("%s: reason %q does not tell the operator it resolves by itself", tag, reasons[tag])
		}
	}
}

func TestRingStragglersOfAnEmptyRing(t *testing.T) {
	s := openStore(t)
	conv := s.NewConvergence("default", func(string) []string { return nil })

	// A ring whose group resolves to nothing is a configuration state, not a
	// failure - and it must not become a query with an empty tag list, which
	// would match every device that never checked in.
	got, err := conv.RingStragglers(context.Background(), []string{"ghost"}, "v2")
	if err != nil {
		t.Fatalf("empty ring: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty ring produced %d stragglers: %+v", len(got), got)
	}
}
