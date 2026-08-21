package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

func TestEvidenceExport(t *testing.T) {
	svc, _ := newService(t, nil)
	es := NewEvidenceService(svc, nil, SystemClock{})
	ctx := context.Background()
	now := time.Now()

	// A window around now carries the seed commit.
	ev, err := es.Export(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Commits) != 1 || ev.Commits[0].Subject != "seed" {
		t.Fatalf("commits = %+v", ev.Commits)
	}
	if ev.Changes == nil || ev.Rollouts == nil {
		t.Fatal("empty sections must be arrays, not null (JSON contract)")
	}

	// A window before the repo existed is empty, not an error.
	ev, err = es.Export(ctx, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if err != nil || len(ev.Commits) != 0 {
		t.Fatalf("past window = %+v, %v", ev.Commits, err)
	}

	// Degenerate period refused.
	if _, err := es.Export(ctx, now, now); err == nil {
		t.Fatal("from == to accepted")
	}
}

// An evidence bundle is written to be shown to somebody else, so it must not
// understate what was in force. It carried only the four-eyes flag, which told
// an auditor about separation of duties and nothing about whether direct edits
// were forbidden or a rollout could start without a gated test wave - the two
// controls the question is usually about.
func TestEvidenceCarriesEveryAssuranceControl(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := context.Background()
	if err := svc.ApplyStructural(ctx, fleet.SetAssurance(fleet.Assurance{
		RequireFourEyes:      true,
		RequireChangeRequest: true,
		RequireTestWave:      true,
		ManualRolloutOnly:    true,
	}), "assurance: everything on", engineAuthor()); err != nil {
		t.Fatal(err)
	}

	es := NewEvidenceService(svc, nil, SystemClock{})
	now := time.Now()
	ev, err := es.Export(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := EvidenceControls{
		RequireFourEyes: true, RequireChangeRequest: true,
		RequireTestWave: true, ManualRolloutOnly: true,
	}
	if ev.Controls != want {
		t.Fatalf("controls = %+v, want every control reported: %+v", ev.Controls, want)
	}

	// And off must be reported as off rather than omitted: a reader has to be
	// able to tell "this control was not in force" from "this export does not
	// know about that control".
	if err := svc.ApplyStructural(ctx, fleet.SetAssurance(fleet.Assurance{}),
		"assurance: everything off", engineAuthor()); err != nil {
		t.Fatal(err)
	}
	ev, err = es.Export(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Controls != (EvidenceControls{}) {
		t.Fatalf("controls = %+v, want all four false", ev.Controls)
	}
	raw, err := json.Marshal(ev.Controls)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"requireFourEyes", "requireChangeRequest", "requireTestWave", "manualRolloutOnly"} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("%s is omitted when false; an auditor cannot tell absent from off", k)
		}
	}
}
