package postgres

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
)

func TestImageJobsUpsertGetListDelete(t *testing.T) {
	s := openStore(t).ImageJobs()
	ctx := context.Background()

	if _, ok, err := s.Get(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:01"); err != nil || ok {
		t.Fatalf("empty get = %v %v", ok, err)
	}

	job := imaging.Job{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:01", Tag: "lab-1", Hardware: "hw", Status: imaging.Pending}
	if err := s.Upsert(ctx, "t1", job, t0); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:01")
	if err != nil || !ok || got.Status != imaging.Pending {
		t.Fatalf("get = %+v %v %v", got, ok, err)
	}

	pend, err := s.ListPending(ctx, "t1", "nuc-1")
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending = %+v %v", pend, err)
	}

	if applied, err := s.TransitionStatus(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:01", imaging.Pending, imaging.Installed, "", t0.Add(time.Minute)); err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if pend, _ = s.ListPending(ctx, "t1", "nuc-1"); len(pend) != 0 {
		t.Fatalf("still pending after installed: %+v", pend)
	}
	all, err := s.ListByStation(ctx, "t1", "nuc-1")
	if err != nil || len(all) != 1 || all[0].Status != imaging.Installed {
		t.Fatalf("list by station = %+v %v", all, err)
	}

	if err := s.Delete(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:01"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:01"); ok {
		t.Fatal("job survived delete")
	}
}

func TestTransitionStatusAppliesOnlyFromExpectedStatus(t *testing.T) {
	s := openStore(t).ImageJobs()
	ctx := context.Background()

	job := imaging.Job{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:02", Tag: "lab-1", Hardware: "hw", Status: imaging.Pending}
	if err := s.Upsert(ctx, "t1", job, t0); err != nil {
		t.Fatal(err)
	}

	// A transition from a stale `from` (job is pending, not imaging) is a
	// no-op: zero rows match the conditional UPDATE.
	applied, err := s.TransitionStatus(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:02", imaging.Imaging, imaging.Installed, "", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("transition applied from a status the job is not in")
	}
	if got, _, _ := s.Get(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:02"); got.Status != imaging.Pending {
		t.Fatalf("job moved despite mismatched from: %+v", got)
	}

	// The correct from applies and stamps the message/updated columns.
	applied, err = s.TransitionStatus(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:02", imaging.Pending, imaging.Failed, "disk not found", t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("transition from the job's real status did not apply")
	}
	got, _, _ := s.Get(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:02")
	if got.Status != imaging.Failed || got.Message != "disk not found" {
		t.Fatalf("job after transition = %+v", got)
	}
}

// TestTransitionStatusResetsProgressAndStep guards against a terminal record
// showing a stale in-progress percentage/label: a job ticking at progress=40
// step="installing" that transitions to a terminal status must read as reset,
// mirroring TransitionStatus's documented contract.
func TestTransitionStatusResetsProgressAndStep(t *testing.T) {
	s := openStore(t).ImageJobs()
	ctx := context.Background()

	job := imaging.Job{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:04", Tag: "lab-1", Hardware: "hw", Status: imaging.Imaging}
	if err := s.Upsert(ctx, "t1", job, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProgress(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:04", 40, "installing", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	applied, err := s.TransitionStatus(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:04",
		imaging.Imaging, imaging.Installed, "", t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("transition did not apply")
	}
	got, _, err := s.Get(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:04")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != imaging.Installed || got.Progress != 0 || got.Step != "" {
		t.Fatalf("terminal job kept stale progress/step: %+v", got)
	}
}

// TestTransitionStatusConcurrentReportsExactlyOneApplies is the atomicity
// proof for the fix: two reports racing to move the SAME job off the SAME
// from-status must not both win. Before this fix, Report's Get -> CanTransition
// -> UpdateStatus sequence let both callers pass the check-then-act guard and
// both write, silently defeating the domain's transition invariant. The
// store's conditional UPDATE (status=$from in the WHERE clause) makes exactly
// one of them match a row.
func TestTransitionStatusConcurrentReportsExactlyOneApplies(t *testing.T) {
	s := openStore(t).ImageJobs()
	ctx := context.Background()

	job := imaging.Job{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:03", Tag: "lab-1", Hardware: "hw", Status: imaging.Imaging}
	if err := s.Upsert(ctx, "t1", job, t0); err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var wg sync.WaitGroup
	var applied int64
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.TransitionStatus(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:03",
				imaging.Imaging, imaging.Installed, "", t0.Add(time.Minute))
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				atomic.AddInt64(&applied, 1)
			}
		}()
	}
	wg.Wait()

	if applied != 1 {
		t.Fatalf("applied = %d, want exactly 1 of %d concurrent transitions", applied, racers)
	}
	got, _, err := s.Get(ctx, "t1", "nuc-1", "aa:bb:cc:dd:ee:03")
	if err != nil || got.Status != imaging.Installed {
		t.Fatalf("final status = %+v %v, want installed", got, err)
	}
}
