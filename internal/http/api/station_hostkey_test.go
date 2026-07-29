package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Throwaway keys; the blob's embedded algorithm name must match the label.
const (
	testHostKey  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICorcsaT0wXjveoHvIS5fpNtBQvpBJ/8TADXMBOd+nIP root@lab-1"
	testHostKeyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIZeLH/5H0LFB6TECGsgpniOQbttXevMqd5OAAoFg0Yu root@lab-1"
)

// memFleet is an in-package fleetWriter that applies the mutation to a
// document held in memory, so the handler test observes exactly what a real
// commit would record.
type memFleet struct {
	f       *fleet.Fleet
	commits []string
	fail    error // when set, every write fails (the unwritable-document case)
}

func (m *memFleet) ApplyStructural(_ context.Context, mut fleet.Mutation, msg string, _ ports.Author) error {
	if m.fail != nil {
		return m.fail
	}
	if err := mut(m.f); err != nil {
		return err
	}
	m.commits = append(m.commits, msg)
	return nil
}

func (m *memFleet) recorded(tag string) string { return m.f.Devices[tag].ITAM.HostKeyID }

// newHostKeyServer serves the station API over one dispatched, already-claimed
// job for lab-1, with a fleet document the install report can write into.
func newHostKeyServer(t *testing.T, w fleetWriter) (*httptest.Server, *app.ImagingService) {
	t.Helper()
	store := &jobMemStore{m: map[string]imaging.Job{}}
	svc := app.NewImagingService(store, fixedClock{time.Unix(1000, 0)}, "")
	api := NewStation(nil, svc, &fakeCreds{}, stationAuth{secret: "s3cr3t", station: "nuc-1"}, "", discardLog())
	if w != nil {
		api.WithConfig(w)
	}
	mux := http.NewServeMux()
	api.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	if err := svc.Dispatch(ctx, imaging.Job{Station: "nuc-1", MAC: mac1, Tag: "lab-1", Hardware: "hw"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Report(ctx, "nuc-1", mac1, imaging.Imaging, ""); err != nil {
		t.Fatal(err)
	}
	return ts, svc
}

const mac1 = "aa:bb:cc:dd:ee:01"

func newMemFleet(recorded string) *memFleet {
	d := fleet.Device{Hardware: "hw"}
	d.ITAM.HostKeyID = recorded
	return &memFleet{f: &fleet.Fleet{Devices: map[string]fleet.Device{"lab-1": d}}}
}

func statusReport(t *testing.T, ts *httptest.Server, body string) *http.Response {
	t.Helper()
	return req(t, "POST", ts.URL+"/api/station/nuc-1/jobs/"+mac1+"/status", "s3cr3t", body)
}

func TestStationInstallRecordsHostKey(t *testing.T) {
	mf := newMemFleet("")
	ts, svc := newHostKeyServer(t, mf)

	resp := statusReport(t, ts, `{"status":"installed","hostKey":"`+testHostKey+`"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("install with host key = %d, want 204", resp.StatusCode)
	}
	if got := mf.recorded("lab-1"); got != testHostKey {
		t.Fatalf("recorded key = %q, want %q", got, testHostKey)
	}
	if j, _, _ := svc.Get(context.Background(), "nuc-1", mac1); j.Status != imaging.Installed {
		t.Fatalf("job status = %s, want installed", j.Status)
	}
	// The commit names the fingerprint, never the key material.
	if len(mf.commits) != 1 {
		t.Fatalf("commits = %v, want exactly one", mf.commits)
	}
	msg := mf.commits[0]
	if !strings.Contains(msg, "lab-1") || !strings.Contains(msg, fleet.HostKeyFingerprint(testHostKey)) {
		t.Errorf("commit message %q names neither the tag nor the fingerprint", msg)
	}
	if strings.Contains(msg, "AAAAC3") {
		t.Errorf("commit message carries key material: %q", msg)
	}
}

// Re-imaging mints a new keypair, so the install report replaces a key
// already on file - that is the whole point of the force flag.
func TestStationReimageReplacesHostKey(t *testing.T) {
	mf := newMemFleet(testHostKeyB)
	ts, _ := newHostKeyServer(t, mf)

	resp := statusReport(t, ts, `{"status":"installed","hostKey":"`+testHostKey+`"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("re-image install = %d, want 204", resp.StatusCode)
	}
	if got := mf.recorded("lab-1"); got != testHostKey {
		t.Fatalf("recorded key = %q, want the freshly imaged one", got)
	}
}

func TestStationHostKeyRejected(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"wrong shape", `{"status":"installed","hostKey":"not a key"}`, http.StatusBadRequest},
		{"unknown algorithm", `{"status":"installed","hostKey":"ssh-dss AAAAB3NzaC1kc3MAAACBAKw="}`, http.StatusBadRequest},
		{"embedded newline", `{"status":"installed","hostKey":"` + testHostKey + `\n` + testHostKeyB + `"}`, http.StatusBadRequest},
		{"private key material", `{"status":"installed","hostKey":"-----BEGIN OPENSSH PRIVATE KEY-----"}`, http.StatusBadRequest},
		// Only the install report knows the key belongs to the image just written.
		{"not an install report", `{"status":"failed","hostKey":"` + testHostKey + `"}`, http.StatusBadRequest},
		{"progress tick", `{"progress":50,"hostKey":"` + testHostKey + `"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mf := newMemFleet("")
			ts, svc := newHostKeyServer(t, mf)

			resp := statusReport(t, ts, tt.body)
			resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			if got := mf.recorded("lab-1"); got != "" {
				t.Errorf("a rejected report recorded %q", got)
			}
			// A rejected report must leave the job where it was, so the
			// station's corrected retry replays the same transition.
			if j, _, _ := svc.Get(context.Background(), "nuc-1", mac1); j.Status != imaging.Imaging {
				t.Errorf("job moved to %s on a rejected report", j.Status)
			}
		})
	}
}

// An install the console cannot record the key for is refused outright: a job
// marked installed with no key on file is exactly the frozen device this path
// exists to prevent, and the station retries.
func TestStationInstallRefusedWhenHostKeyCannotBeRecorded(t *testing.T) {
	for name, w := range map[string]fleetWriter{
		"no fleet writer wired": nil,
		"document unwritable":   &memFleet{f: &fleet.Fleet{}, fail: errors.New("repo locked")},
	} {
		t.Run(name, func(t *testing.T) {
			ts, svc := newHostKeyServer(t, w)
			resp := statusReport(t, ts, `{"status":"installed","hostKey":"`+testHostKey+`"}`)
			resp.Body.Close()
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", resp.StatusCode)
			}
			if j, _, _ := svc.Get(context.Background(), "nuc-1", mac1); j.Status != imaging.Imaging {
				t.Errorf("job moved to %s though the key was not recorded", j.Status)
			}
		})
	}
}

// An install report without a key still works: not every station reports one
// yet, and the console must not block imaging on the new field.
func TestStationInstallWithoutHostKeyUnchanged(t *testing.T) {
	mf := newMemFleet("")
	ts, svc := newHostKeyServer(t, mf)

	resp := statusReport(t, ts, `{"status":"installed"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("install = %d, want 204", resp.StatusCode)
	}
	if j, _, _ := svc.Get(context.Background(), "nuc-1", mac1); j.Status != imaging.Installed {
		t.Fatalf("job status = %s, want installed", j.Status)
	}
	if len(mf.commits) != 0 {
		t.Errorf("a report without a host key wrote the fleet document: %v", mf.commits)
	}
}
