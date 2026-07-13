package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

type countingDir struct {
	calls int
	err   error
}

func (c *countingDir) ListGroups(context.Context, string) ([]ports.DirectoryGroup, error) {
	c.calls++
	return []ports.DirectoryGroup{{ID: "g", Name: "grp"}}, c.err
}

type fakeClk struct{ t time.Time }

func (f *fakeClk) Now() time.Time { return f.t }

func TestCachedDirectory(t *testing.T) {
	inner := &countingDir{}
	clk := &fakeClk{t: time.Unix(1000, 0)}
	d := NewCachedDirectory(inner, time.Minute, clk)

	// First call dials; second within TTL is served from cache.
	if _, err := d.ListGroups(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	_, _ = d.ListGroups(context.Background(), "")
	if inner.calls != 1 {
		t.Fatalf("dials = %d, want 1 (cached)", inner.calls)
	}
	// After the TTL, it dials again.
	clk.t = clk.t.Add(2 * time.Minute)
	_, _ = d.ListGroups(context.Background(), "")
	if inner.calls != 2 {
		t.Fatalf("dials = %d, want 2 (TTL expired)", inner.calls)
	}
}

func TestCachedDirectoryCachesErrors(t *testing.T) {
	inner := &countingDir{err: errors.New("ldap down")}
	clk := &fakeClk{t: time.Unix(1000, 0)}
	d := NewCachedDirectory(inner, time.Minute, clk)
	// An unreachable directory is dialled at most once per TTL, not per call.
	for i := 0; i < 5; i++ {
		if _, err := d.ListGroups(context.Background(), ""); err == nil {
			t.Fatal("expected the cached error")
		}
	}
	if inner.calls != 1 {
		t.Fatalf("dials = %d, want 1 (error cached)", inner.calls)
	}
}
