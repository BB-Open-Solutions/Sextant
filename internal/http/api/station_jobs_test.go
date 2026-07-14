package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
)

// jobMemStore is an in-package ports.ImageJobStore for the handler test.
type jobMemStore struct{ m map[string]imaging.Job }

func jk(station, mac string) string { return station + "|" + mac }

func (s *jobMemStore) Upsert(_ context.Context, _ string, j imaging.Job, _ time.Time) error {
	s.m[jk(j.Station, j.MAC)] = j
	return nil
}
func (s *jobMemStore) ListByStation(_ context.Context, _, station string) ([]imaging.Job, error) {
	return s.filter(station, false), nil
}
func (s *jobMemStore) ListPending(_ context.Context, _, station string) ([]imaging.Job, error) {
	return s.filter(station, true), nil
}
func (s *jobMemStore) filter(station string, pendingOnly bool) []imaging.Job {
	var out []imaging.Job
	for _, j := range s.m {
		if j.Station != station {
			continue
		}
		if pendingOnly && j.Status != imaging.Pending && j.Status != imaging.Imaging {
			continue
		}
		out = append(out, j)
	}
	return out
}
func (s *jobMemStore) Get(_ context.Context, _, station, mac string) (imaging.Job, bool, error) {
	j, ok := s.m[jk(station, mac)]
	return j, ok, nil
}
func (s *jobMemStore) UpdateStatus(_ context.Context, _, station, mac string, st imaging.Status, msg string, _ time.Time) error {
	j := s.m[jk(station, mac)]
	j.Status, j.Message = st, msg
	s.m[jk(station, mac)] = j
	return nil
}
func (s *jobMemStore) UpdateProgress(_ context.Context, _, station, mac string, progress int, step string, _ time.Time) error {
	j := s.m[jk(station, mac)]
	j.Progress, j.Step = progress, step
	s.m[jk(station, mac)] = j
	return nil
}
func (s *jobMemStore) TransitionStatus(_ context.Context, _, station, mac string, from, to imaging.Status, msg string, _ time.Time) (bool, error) {
	j, ok := s.m[jk(station, mac)]
	if !ok || j.Status != from {
		return false, nil
	}
	j.Status, j.Message = to, msg
	s.m[jk(station, mac)] = j
	return true, nil
}
func (s *jobMemStore) Delete(_ context.Context, _, station, mac string) error {
	delete(s.m, jk(station, mac))
	return nil
}

// fakeCreds hands out a fixed secret per tag and records issuance.
type fakeCreds struct{ issued []string }

func (f *fakeCreds) Issue(_ context.Context, tag string) (string, error) {
	f.issued = append(f.issued, tag)
	return "sxt_" + tag + "_secret", nil
}

func newJobsServer(t *testing.T) (*httptest.Server, *app.ImagingService, *fakeCreds) {
	t.Helper()
	store := &jobMemStore{m: map[string]imaging.Job{}}
	svc := app.NewImagingService(store, fixedClock{time.Unix(1000, 0)}, "")
	creds := &fakeCreds{}
	api := NewStation(nil, svc, creds, stationAuth{secret: "s3cr3t", station: "nuc-1"}, "", discardLog())
	mux := http.NewServeMux()
	api.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, svc, creds
}

func req(t *testing.T, method, url, bearer, body string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	r, _ := http.NewRequest(method, url, rdr)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestStationJobLifecycle(t *testing.T) {
	ts, svc, creds := newJobsServer(t)
	ctx := context.Background()

	// Operator dispatched a job (via the service).
	if err := svc.Dispatch(ctx, imaging.Job{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:01", Tag: "lab-1", Hardware: "lenovo-t495s"}); err != nil {
		t.Fatal(err)
	}

	// Auth is required.
	noAuthResp := req(t, "GET", ts.URL+"/api/station/nuc-1/jobs", "", "")
	noAuthResp.Body.Close()
	if noAuthResp.StatusCode != 401 {
		t.Fatalf("no auth = %d, want 401", noAuthResp.StatusCode)
	}

	// Poll: the pending job appears, without a credential.
	resp := req(t, "GET", ts.URL+"/api/station/nuc-1/jobs", "s3cr3t", "")
	var jobs []jobView
	json.NewDecoder(resp.Body).Decode(&jobs)
	resp.Body.Close()
	if len(jobs) != 1 || jobs[0].Tag != "lab-1" || jobs[0].Credential != "" {
		t.Fatalf("poll = %+v", jobs)
	}

	// Claim: job goes to imaging and the response carries the device credential.
	resp = req(t, "POST", ts.URL+"/api/station/nuc-1/jobs/claim", "s3cr3t", "")
	var claimed []jobView
	json.NewDecoder(resp.Body).Decode(&claimed)
	resp.Body.Close()
	if len(claimed) != 1 || claimed[0].Credential != "sxt_lab-1_secret" || claimed[0].Status != "imaging" {
		t.Fatalf("claim = %+v", claimed)
	}
	if len(creds.issued) != 1 || creds.issued[0] != "lab-1" {
		t.Fatalf("credential not issued for the device: %v", creds.issued)
	}
	if j, _, _ := svc.Get(ctx, "nuc-1", "aa:bb:cc:dd:ee:01"); j.Status != imaging.Imaging {
		t.Fatalf("job not marked imaging: %s", j.Status)
	}

	// Report installed.
	resp = req(t, "POST", ts.URL+"/api/station/nuc-1/jobs/aa:bb:cc:dd:ee:01/status", "s3cr3t", `{"status":"installed"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status installed = %d, want 204", resp.StatusCode)
	}
	if j, _, _ := svc.Get(ctx, "nuc-1", "aa:bb:cc:dd:ee:01"); j.Status != imaging.Installed {
		t.Fatalf("job not installed: %s", j.Status)
	}

	// An illegal transition (installed -> imaging) is refused with 409.
	resp = req(t, "POST", ts.URL+"/api/station/nuc-1/jobs/aa:bb:cc:dd:ee:01/status", "s3cr3t", `{"status":"imaging"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("illegal transition = %d, want 409", resp.StatusCode)
	}
}
