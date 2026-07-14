package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
)

// fakeSink records what the station API asks it to seal.
type fakeSink struct {
	enabled bool
	stored  map[string]string
}

func (f *fakeSink) Enabled() bool { return f.enabled }
func (f *fakeSink) Store(_ context.Context, tag string, kind secret.Kind, plaintext, _ string) error {
	f.stored[tag+"|"+string(kind)] = plaintext
	return nil
}

// installReportsLUKS drives one job to imaging and reports it installed with a
// LUKS recovery key in the message, returning the resulting stored job.
func installReportsLUKS(t *testing.T, sink *fakeSink) imaging.Job {
	t.Helper()
	store := &jobMemStore{m: map[string]imaging.Job{}}
	svc := app.NewImagingService(store, fixedClock{time.Unix(1000, 0)}, "")
	ctx := context.Background()
	if err := svc.Dispatch(ctx, imaging.Job{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:01", Tag: "lab-1", Hardware: "lenovo-t495s"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Report(ctx, "nuc-1", "aa:bb:cc:dd:ee:01", imaging.Imaging, ""); err != nil {
		t.Fatal(err)
	}

	stationAPI := NewStation(nil, svc, &fakeCreds{}, stationAuth{secret: "s3cr3t", station: "nuc-1"}, "", discardLog())
	if sink != nil {
		stationAPI.WithSecrets(sink)
	}
	mux := http.NewServeMux()
	stationAPI.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp := req(t, http.MethodPost, ts.URL+"/api/station/nuc-1/jobs/aa:bb:cc:dd:ee:01/status",
		"s3cr3t", `{"status":"installed","message":"`+imaging.LUKSRecoveryPrefix+`z7Xq-9pLm"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status report = %d, want 204", resp.StatusCode)
	}
	job, _, _ := svc.Get(ctx, "nuc-1", "aa:bb:cc:dd:ee:01")
	return job
}

func TestStationSealsLUKSWhenStoreEnabled(t *testing.T) {
	sink := &fakeSink{enabled: true, stored: map[string]string{}}
	job := installReportsLUKS(t, sink)

	if got := sink.stored["lab-1|luks"]; got != "z7Xq-9pLm" {
		t.Fatalf("LUKS key not sealed into the store: %q", got)
	}
	if job.Message != "" {
		t.Fatalf("plaintext LUKS key left in the job message: %q", job.Message)
	}
}

func TestStationKeepsLUKSMessageWithoutStore(t *testing.T) {
	// No secret store: the key stays in the message for a one-shot copy.
	job := installReportsLUKS(t, nil)
	if job.Message != imaging.LUKSRecoveryPrefix+"z7Xq-9pLm" {
		t.Fatalf("without a store the one-shot key must stay in the message, got %q", job.Message)
	}

	// A disabled sink behaves the same (key not persisted, kept in message).
	job = installReportsLUKS(t, &fakeSink{enabled: false, stored: map[string]string{}})
	if job.Message == "" {
		t.Fatal("a disabled sink must not strip the one-shot key")
	}
}
