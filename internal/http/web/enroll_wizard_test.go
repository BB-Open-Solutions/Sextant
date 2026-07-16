package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// wizJobStore is a minimal in-memory ports.ImageJobStore for wizard-handler
// tests: it lets a test seed jobs in any status directly (bypassing the
// service's transition rules) and read them back per station.
type wizJobStore struct{ jobs []imaging.Job }

func (s *wizJobStore) Upsert(_ context.Context, _ string, j imaging.Job, _ time.Time) error {
	s.jobs = append(s.jobs, j)
	return nil
}
func (s *wizJobStore) ListByStation(_ context.Context, _, station string) ([]imaging.Job, error) {
	var out []imaging.Job
	for _, j := range s.jobs {
		if j.Station == station {
			out = append(out, j)
		}
	}
	return out, nil
}
func (s *wizJobStore) ListPending(context.Context, string, string) ([]imaging.Job, error) {
	return nil, nil
}
func (s *wizJobStore) GetActiveByTag(context.Context, string, string) (imaging.Job, bool, error) {
	return imaging.Job{}, false, nil
}
func (s *wizJobStore) Get(context.Context, string, string, string) (imaging.Job, bool, error) {
	return imaging.Job{}, false, nil
}
func (s *wizJobStore) UpdateStatus(context.Context, string, string, string, imaging.Status, string, time.Time) error {
	return nil
}
func (s *wizJobStore) UpdateProgress(context.Context, string, string, string, int, string, time.Time) error {
	return nil
}
func (s *wizJobStore) TransitionStatus(context.Context, string, string, string, imaging.Status, imaging.Status, string, time.Time) (bool, error) {
	return true, nil
}
func (s *wizJobStore) Delete(context.Context, string, string, string) error { return nil }

// newWizardConsole seeds a station whose jobs are in a mix of provisioning
// states so the wizard's stepper, progress, firmware step, reboot control and
// one-shot secret can all be exercised in one render. sessions lets callers
// exercise both the dev/owner path and a lower-privileged/unrelated-scope
// session against the identical seeded jobs (the negative-authz case below).
func newWizardConsole(t *testing.T, sessions web.Sessions) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{
		"fleet.json":             seedStationFleet,
		"hardware-profiles.json": seedHardwareProfiles,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}

	store := &wizJobStore{}
	seed := []imaging.Job{
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:01", Tag: "kiosk-01", Hardware: "lenovo-t495s", Status: imaging.Imaging, Progress: 68, Step: "copying nix-store"},
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:02", Tag: "kiosk-02", Hardware: "lenovo-t495s", Status: imaging.Installed, Message: "luks-recovery-key: z7Xq-9pLm-R2wK"},
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:03", Tag: "kiosk-03", Hardware: "lenovo-t495s", Status: imaging.SBPending},
		{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:04", Tag: "kiosk-04", Hardware: "lenovo-t495s", Status: imaging.Failed, Message: "disko failed"},
	}
	for _, j := range seed {
		_ = store.Upsert(context.Background(), app.DefaultTenant, j, time.Unix(1, 0))
	}

	srv, err := web.New(web.Services{
		Config:  cfg,
		Imaging: app.NewImagingService(store, clockNow{}, ""),
	}, sessions, true, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestEnrollWizardRendersProvisioningState(t *testing.T) {
	ts := newWizardConsole(t, web.DevSessions{})
	c := client()

	resp, _ := c.Get(ts.URL + "/enroll/nuc-1/wizard")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("wizard = %d", resp.StatusCode)
	}
	s := string(body)

	// Live progress for the imaging device.
	if !strings.Contains(s, "68%") || !strings.Contains(s, "copying nix-store") {
		t.Error("progress bar / step text not rendered")
	}
	// One-shot LUKS recovery key for the just-installed device.
	if !strings.Contains(s, "z7Xq-9pLm-R2wK") {
		t.Error("LUKS recovery key not surfaced on the installed device")
	}
	// Secure Boot phase: the manual firmware step with the Lenovo entry key.
	if !strings.Contains(s, "Reset to Setup Mode") {
		t.Error("Secure Boot firmware steps not rendered")
	}
	if !strings.Contains(s, "F1") {
		t.Error("brand-specific BIOS key (Lenovo F1) not derived")
	}
	// The reboot-to-BIOS control (Editor, dev session is owner) for a live device.
	if !strings.Contains(s, `name="intent" value="reboot"`) {
		t.Error("reboot control not offered")
	}
	// Failure detail is surfaced.
	if !strings.Contains(s, "disko failed") {
		t.Error("failure message not shown")
	}
	// Stepper labels.
	for _, want := range []string{"Secure Boot", "TPM2"} {
		if !strings.Contains(s, want) {
			t.Errorf("stepper missing phase %q", want)
		}
	}
}

// TestEnrollWizardDeniesLowerPrivilegeAndNeverLeaksLUKSKey is the negative
// counterpart to TestEnrollWizardRendersProvisioningState: a session with no
// org-Editor binding (a viewer bound to an unrelated scope, e.g. the same
// low-privilege user visibility_test.go uses for read-confidentiality) must
// be refused with 403 before the handler ever populates row.LUKS, so the
// one-shot recovery key for kiosk-02 must not appear in the response body.
func TestEnrollWizardDeniesLowerPrivilegeAndNeverLeaksLUKSKey(t *testing.T) {
	outsider := identity.User{Subject: "outsider", Groups: []string{"unrelated-team"}}
	ts := newWizardConsole(t, scopedSessions{outsider})
	c := client()

	resp, _ := c.Get(ts.URL + "/enroll/nuc-1/wizard")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wizard for unrelated-scope viewer = %d, want 403", resp.StatusCode)
	}
	if strings.Contains(string(body), "z7Xq-9pLm-R2wK") {
		t.Fatal("LUKS recovery key leaked to a viewer with no org-Editor binding")
	}
}

func TestEnrollWizardNeedsImaging(t *testing.T) {
	// Without the imaging store the wizard is unavailable, not a panic.
	dir := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seedStationFleet), 0o644)
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.New(web.Services{Config: cfg}, web.DevSessions{}, true, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, _ := client().Get(ts.URL + "/enroll/nuc-1/wizard")
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("wizard without imaging store = %d, want 503", resp.StatusCode)
	}
}
