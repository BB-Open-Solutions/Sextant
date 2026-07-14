package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
)

// memImageJobs is an in-memory ports.ImageJobStore for service tests. mu
// guards m so TransitionStatus can offer the same compare-and-swap guarantee
// the postgres store gives via its conditional UPDATE.
type memImageJobs struct {
	mu sync.Mutex
	m  map[string]imaging.Job
}

func key(tenant, station, mac string) string { return tenant + "|" + station + "|" + mac }

func (s *memImageJobs) Upsert(_ context.Context, tenant string, j imaging.Job, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key(tenant, j.Station, j.MAC)] = j
	return nil
}
func (s *memImageJobs) ListByStation(_ context.Context, tenant, station string) ([]imaging.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []imaging.Job
	for _, j := range s.m {
		if j.Station == station {
			out = append(out, j)
		}
	}
	return out, nil
}
func (s *memImageJobs) ListPending(_ context.Context, tenant, station string) ([]imaging.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []imaging.Job
	for _, j := range s.m {
		if j.Station == station && (j.Status == imaging.Pending || j.Status == imaging.Imaging) {
			out = append(out, j)
		}
	}
	return out, nil
}
func (s *memImageJobs) Get(_ context.Context, tenant, station, mac string) (imaging.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.m[key(tenant, station, mac)]
	return j, ok, nil
}
func (s *memImageJobs) UpdateStatus(_ context.Context, tenant, station, mac string, st imaging.Status, msg string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.m[key(tenant, station, mac)]
	j.Status, j.Message = st, msg
	s.m[key(tenant, station, mac)] = j
	return nil
}

func (s *memImageJobs) UpdateProgress(_ context.Context, tenant, station, mac string, progress int, step string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.m[key(tenant, station, mac)]
	j.Progress, j.Step = progress, step
	s.m[key(tenant, station, mac)] = j
	return nil
}

// TransitionStatus mirrors the postgres compare-and-swap: the map write only
// applies if the stored status still equals from, under the same lock as
// every other access, so concurrent callers race honestly instead of just
// interleaving on an unsynchronized map.
func (s *memImageJobs) TransitionStatus(_ context.Context, tenant, station, mac string, from, to imaging.Status, msg string, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.m[key(tenant, station, mac)]
	if !ok || j.Status != from {
		return false, nil
	}
	j.Status, j.Message = to, msg
	s.m[key(tenant, station, mac)] = j
	return true, nil
}
func (s *memImageJobs) Delete(_ context.Context, tenant, station, mac string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key(tenant, station, mac))
	return nil
}

func newImaging() *ImagingService {
	return NewImagingService(&memImageJobs{m: map[string]imaging.Job{}}, clockAt{time.Unix(1, 0)}, "")
}

type clockAt struct{ t time.Time }

func (c clockAt) Now() time.Time { return c.t }

func TestImagingDispatchAndLifecycle(t *testing.T) {
	s := newImaging()
	ctx := context.Background()
	job := imaging.Job{Station: "nuc-1", MAC: "AA:BB:CC:DD:EE:01", Tag: "lab-1", Hardware: "lenovo-t495s"}
	if err := s.Dispatch(ctx, job); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// MAC normalised, status defaulted to pending, shows up pending.
	got, ok, _ := s.Get(ctx, "nuc-1", "aa:bb:cc:dd:ee:01")
	if !ok || got.Status != imaging.Pending {
		t.Fatalf("job not pending: %+v ok=%v", got, ok)
	}
	pend, _ := s.Pending(ctx, "nuc-1")
	if len(pend) != 1 {
		t.Fatalf("pending = %d", len(pend))
	}

	// Legal progression pending->imaging->installed.
	if err := s.Report(ctx, "nuc-1", "aa:bb:cc:dd:ee:01", imaging.Imaging, ""); err != nil {
		t.Fatalf("->imaging: %v", err)
	}
	if err := s.Report(ctx, "nuc-1", "aa:bb:cc:dd:ee:01", imaging.Installed, ""); err != nil {
		t.Fatalf("->installed: %v", err)
	}
	// Installed is terminal: no further transition.
	if err := s.Report(ctx, "nuc-1", "aa:bb:cc:dd:ee:01", imaging.Imaging, ""); err == nil {
		t.Fatal("transition out of installed accepted")
	}
	// No longer pending.
	pend, _ = s.Pending(ctx, "nuc-1")
	if len(pend) != 0 {
		t.Fatalf("still pending after installed: %d", len(pend))
	}
}

func TestImagingReportUnknownJob(t *testing.T) {
	s := newImaging()
	if err := s.Report(context.Background(), "nuc-1", "aa:bb:cc:dd:ee:99", imaging.Imaging, ""); err == nil {
		t.Fatal("reporting on an unknown job should error")
	}
}

func TestImagingFailAndRetry(t *testing.T) {
	s := newImaging()
	ctx := context.Background()
	_ = s.Dispatch(ctx, imaging.Job{Station: "s", MAC: "aa:bb:cc:dd:ee:02", Tag: "t", Hardware: "hw"})
	if err := s.Report(ctx, "s", "aa:bb:cc:dd:ee:02", imaging.Failed, "disk not found"); err != nil {
		t.Fatalf("->failed: %v", err)
	}
	got, _, _ := s.Get(ctx, "s", "aa:bb:cc:dd:ee:02")
	if got.Status != imaging.Failed || got.Message != "disk not found" {
		t.Fatalf("failed job: %+v", got)
	}
	// Retry: failed->pending clears the message.
	if err := s.Report(ctx, "s", "aa:bb:cc:dd:ee:02", imaging.Pending, ""); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, _, _ = s.Get(ctx, "s", "aa:bb:cc:dd:ee:02")
	if got.Status != imaging.Pending || got.Message != "" {
		t.Fatalf("retry did not clear: %+v", got)
	}
}
