package app

import (
	"context"
	"testing"
	"time"
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
