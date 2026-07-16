package app

import (
	"context"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
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
func (s *memImageJobs) GetActiveByTag(_ context.Context, tenant, tag string) (imaging.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, j := range s.m {
		if strings.HasPrefix(k, tenant+"|") && j.Tag == tag && !j.Status.Terminal() {
			return j, true, nil
		}
	}
	return imaging.Job{}, false, nil
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

// TestImagingInstalledMessageClearsOnNextTransition locks in the safety net
// documented in Report: with no secret-store sealer, a one-shot LUKS recovery
// key reported on Installed is deliberately kept in the message so the
// provisioning wizard can reveal it on a later GET (it has no other channel).
// That plaintext must not linger past its usefulness - the very next status
// report for the job, whatever it is, must overwrite the message column and
// wipe the key back out of the store. If a future change to the keep-list
// above widens which transitions preserve message, this test catches it.
func TestImagingInstalledMessageClearsOnNextTransition(t *testing.T) {
	s := newImaging()
	ctx := context.Background()
	_ = s.Dispatch(ctx, imaging.Job{Station: "s", MAC: "aa:bb:cc:dd:ee:03", Tag: "t", Hardware: "hw"})
	_ = s.Report(ctx, "s", "aa:bb:cc:dd:ee:03", imaging.Imaging, "")

	const luksMsg = imaging.LUKSRecoveryPrefix + "z7Xq-9pLm"
	if err := s.Report(ctx, "s", "aa:bb:cc:dd:ee:03", imaging.Installed, luksMsg); err != nil {
		t.Fatalf("->installed: %v", err)
	}
	got, _, _ := s.Get(ctx, "s", "aa:bb:cc:dd:ee:03")
	if got.Message != luksMsg {
		t.Fatalf("one-shot key not kept on installed: %+v", got)
	}

	// The next transition the station reports - here sb-pending, carrying no
	// message of its own - must overwrite (not append to or skip) the message
	// column, so the plaintext key does not survive past this point.
	if err := s.Report(ctx, "s", "aa:bb:cc:dd:ee:03", imaging.SBPending, ""); err != nil {
		t.Fatalf("->sb-pending: %v", err)
	}
	got, _, _ = s.Get(ctx, "s", "aa:bb:cc:dd:ee:03")
	if got.Message != "" {
		t.Fatalf("plaintext LUKS key survived the next transition: %+v", got)
	}
}

// TestWizardIntentAndAdvance drives a device through the post-install
// ceremony purely via check-in reports, the way production advances it.
func TestWizardIntentAndAdvance(t *testing.T) {
	ctx := context.Background()
	s := newImaging()
	job := imaging.Job{Station: "s", MAC: "aa:bb:cc:dd:ee:09", Tag: "lap9", Hardware: "hw"}
	if err := s.Dispatch(ctx, job); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// Pending/imaging: the station's plane, no provision intent yet.
	if got := s.WizardIntent(ctx, "lap9"); got != "" {
		t.Fatalf("pending job should not provision, got %q", got)
	}
	if err := s.Report(ctx, "s", job.MAC, imaging.Imaging, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(ctx, "s", job.MAC, imaging.Installed, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.WizardIntent(ctx, "lap9"); got != "provision" {
		t.Fatalf("installed job should provision, got %q", got)
	}

	beat := func(sb observed.SBState, tpm2 observed.TPM2State, ack string) imaging.Status {
		t.Helper()
		if err := s.AdvanceFromDevice(ctx, observed.CheckIn{Tag: "lap9", SB: sb, TPM2: tpm2, Ack: ack}); err != nil {
			t.Fatalf("advance: %v", err)
		}
		got, _, _ := s.Get(ctx, "s", job.MAC)
		return got.Status
	}

	// First boot with staged keys -> firmware step pending.
	if st := beat(observed.SBAudit, observed.TPM2Enrolled, ""); st != imaging.SBPending {
		t.Fatalf("after first posture beat: %s", st)
	}
	// Executor enrolled the platform keys.
	if st := beat(observed.SBAudit, observed.TPM2Enrolled, observed.AckSBEnrolled); st != imaging.SBEnrolled {
		t.Fatalf("after sb-enrolled ack: %s", st)
	}
	// Executor sealed the LUKS keyslot.
	if st := beat(observed.SBEnforcing, observed.TPM2Enrolled, observed.AckTPM2Enrolled); st != imaging.TPM2Enrolled {
		t.Fatalf("after tpm2-enrolled ack: %s", st)
	}
	// Final boot, still enforcing: verified done, intent stops.
	if st := beat(observed.SBEnforcing, observed.TPM2Enrolled, ""); st != imaging.Done {
		t.Fatalf("after final beat: %s", st)
	}
	if got := s.WizardIntent(ctx, "lap9"); got != "" {
		t.Fatalf("done job should stop provisioning, got %q", got)
	}
	// A device with no active job is a no-op, not an error.
	if err := s.AdvanceFromDevice(ctx, observed.CheckIn{Tag: "lap9", SB: observed.SBEnforcing}); err != nil {
		t.Fatalf("advance on done job: %v", err)
	}
}
