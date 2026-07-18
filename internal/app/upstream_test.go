package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

type memUpstream struct{ rev string }

func (m *memUpstream) LastUpstream(context.Context) (string, error) { return m.rev, nil }
func (m *memUpstream) PutUpstream(_ context.Context, r string) error {
	m.rev = r
	return nil
}

func TestUpstreamWatcherStagesOneCRPerRelease(t *testing.T) {
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var opened []string
	svc := NewUpstreamService("https://example/core.git",
		func(context.Context, string) (string, error) { return head, nil },
		func(_ context.Context, id, title string, _ ports.Author) (change.CR, error) {
			opened = append(opened, id+"|"+title)
			return change.CR{ID: id}, nil
		},
		&memUpstream{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx := context.Background()
	// First check adopts the current head as baseline: no CR for a
	// "release" that is merely the existing state.
	if err := svc.CheckOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 0 {
		t.Fatalf("baseline sync staged a CR: %v", opened)
	}
	// A subsequent new head IS a release.
	head = "1111111111111111111111111111111111111111"
	if err := svc.CheckOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != "core-111111111111|Core update 111111111111" {
		t.Fatalf("opened = %v", opened)
	}
	// Same head again: nothing new to stage.
	if err := svc.CheckOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 {
		t.Fatalf("re-check staged again: %v", opened)
	}
	// A new release stages the next CR.
	head = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := svc.CheckOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 {
		t.Fatalf("new head not staged: %v", opened)
	}
}

func TestUpstreamWatcherSurvivesExistingCR(t *testing.T) {
	// A baseline is already known, so the new head takes the CR path.
	seen := &memUpstream{rev: "0000000000000000000000000000000000000000"}
	svc := NewUpstreamService("https://example/core.git",
		func(context.Context, string) (string, error) {
			return "cccccccccccccccccccccccccccccccccccccccc", nil
		},
		func(context.Context, string, string, ports.Author) (change.CR, error) {
			return change.CR{}, fmt.Errorf(`change "core-cccccccccccc" already exists`)
		},
		seen, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// An operator already opened the CR by hand: the watcher records the
	// revision as handled instead of erroring forever.
	if err := svc.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen.rev != "cccccccccccccccccccccccccccccccccccccccc" {
		t.Fatalf("existing CR did not mark the revision as handled: %q", seen.rev)
	}
}

func TestUpstreamDeliveryStagesLockAndSubmits(t *testing.T) {
	head := "2222222222222222222222222222222222222222"
	var edited, submitted []string
	svc := NewUpstreamService("https://example/core.git",
		func(context.Context, string) (string, error) { return head, nil },
		func(_ context.Context, id, _ string, _ ports.Author) (change.CR, error) {
			return change.CR{ID: id}, nil
		},
		&memUpstream{rev: "old"}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithDelivery(
			func(_ context.Context, input string) ([]byte, string, error) {
				if input != "dawo" {
					t.Fatalf("bumped input %q", input)
				}
				return []byte(`{"nodes":{}}`), head, nil
			},
			func(_ context.Context, id, path string, _ []byte, _ string, _ ports.Author) error {
				edited = append(edited, id+"|"+path)
				return nil
			},
			func(_ context.Context, id string) (change.CR, error) {
				submitted = append(submitted, id)
				return change.CR{ID: id}, nil
			},
			"dawo")

	if err := svc.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(edited) != 1 || edited[0] != "core-222222222222|flake.lock" {
		t.Fatalf("edited = %v", edited)
	}
	if len(submitted) != 1 || submitted[0] != "core-222222222222" {
		t.Fatalf("submitted = %v", submitted)
	}
}

func TestUpstreamDeliveryBumpFailureLeavesDraft(t *testing.T) {
	var submitted []string
	svc := NewUpstreamService("https://example/core.git",
		func(context.Context, string) (string, error) {
			return "3333333333333333333333333333333333333333", nil
		},
		func(_ context.Context, id, _ string, _ ports.Author) (change.CR, error) {
			return change.CR{ID: id}, nil
		},
		&memUpstream{rev: "old"}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithDelivery(
			func(context.Context, string) ([]byte, string, error) {
				return nil, "", fmt.Errorf("runner down")
			},
			func(context.Context, string, string, []byte, string, ports.Author) error {
				t.Fatal("editFile called after failed bump")
				return nil
			},
			func(_ context.Context, id string) (change.CR, error) {
				submitted = append(submitted, id)
				return change.CR{}, nil
			},
			"dawo")

	if err := svc.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(submitted) != 0 {
		t.Fatalf("submitted after failed bump: %v", submitted)
	}
}
