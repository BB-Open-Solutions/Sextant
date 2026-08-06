package app

import (
	"context"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
)

// imaging_mac_test.go covers the four entry points that were at 0%: List,
// ReportProgress, Cancel and Remove.
//
// They read as thin pass-throughs, and each one carries the same load-bearing
// detail: it normalises the MAC before touching the store. A station reports
// whatever its DHCP lease file spells - upper case, hyphens, either - while
// the console stored whatever the operator typed. Miss the normalisation on
// ONE of these and that path silently addresses a job that does not exist:
// progress never appears, a cancel does nothing, a remove leaves the row.
// Nothing errors, because "no such job" is a legitimate answer.
//
// So these tests deliberately address every entry point in a DIFFERENT
// spelling from the one the job was created with.

func TestImagingEntryPointsNormaliseTheMAC(t *testing.T) {
	ctx := context.Background()
	const (
		stored = "aa:bb:cc:dd:ee:01" // as dispatched
		shouty = "AA:BB:CC:DD:EE:01" // as a station might report
		hyphen = "AA-BB-CC-DD-EE-01" // as another one might
	)

	t.Run("ReportProgress finds the job under another spelling", func(t *testing.T) {
		s := newImaging()
		if err := s.Dispatch(ctx, imaging.Job{Station: "nuc-1", MAC: stored, Tag: "lab-1", Hardware: "lenovo-t495s"}); err != nil {
			t.Fatal(err)
		}
		if err := s.ReportProgress(ctx, "nuc-1", shouty, 42, "partitioning"); err != nil {
			t.Fatalf("ReportProgress: %v", err)
		}
		job, ok, err := s.Get(ctx, "nuc-1", stored)
		if err != nil || !ok {
			t.Fatalf("get: ok=%v err=%v", ok, err)
		}
		if job.Progress != 42 || job.Step != "partitioning" {
			t.Errorf("progress did not land: %d %q - the station's report went nowhere", job.Progress, job.Step)
		}
	})

	t.Run("Cancel finds the job under another spelling", func(t *testing.T) {
		s := newImaging()
		if err := s.Dispatch(ctx, imaging.Job{Station: "nuc-1", MAC: stored, Tag: "lab-1", Hardware: "lenovo-t495s"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Cancel(ctx, "nuc-1", hyphen); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		job, ok, _ := s.Get(ctx, "nuc-1", stored)
		if !ok {
			t.Fatal("job disappeared")
		}
		if job.Status != imaging.Canceled {
			t.Errorf("status = %v, want Canceled - the operator cancelled and nothing happened", job.Status)
		}
	})

	t.Run("Remove finds the job under another spelling", func(t *testing.T) {
		s := newImaging()
		if err := s.Dispatch(ctx, imaging.Job{Station: "nuc-1", MAC: stored, Tag: "lab-1", Hardware: "lenovo-t495s"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Remove(ctx, "nuc-1", shouty); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, ok, _ := s.Get(ctx, "nuc-1", stored); ok {
			t.Error("the job survived Remove; the register keeps a job nobody will image")
		}
	})
}

// TestImagingListIsScopedToItsStation: List backs the station page. A station
// that could see another station's queue would claim jobs meant for hardware
// in a different building.
func TestImagingListIsScopedToItsStation(t *testing.T) {
	ctx := context.Background()
	s := newImaging()
	for _, j := range []imaging.Job{
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:01", Tag: "a", Hardware: "lenovo-t495s"},
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:02", Tag: "b", Hardware: "lenovo-t495s"},
		{Station: "nuc-2", MAC: "aa:bb:cc:dd:ee:03", Tag: "c", Hardware: "lenovo-t495s"},
	} {
		if err := s.Dispatch(ctx, j); err != nil {
			t.Fatal(err)
		}
	}

	one, err := s.List(ctx, "nuc-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(one) != 2 {
		t.Errorf("nuc-1 sees %d jobs, want 2", len(one))
	}
	for _, j := range one {
		if j.Station != "nuc-1" {
			t.Errorf("nuc-1's list contains a job for %q", j.Station)
		}
	}
	// A station with no work gets an empty list, not an error: that is the
	// steady state for every station most of the time.
	empty, err := s.List(ctx, "nuc-unknown")
	if err != nil {
		t.Errorf("List for an unknown station errored: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("unknown station sees %d jobs", len(empty))
	}
}
