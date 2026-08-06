package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
)

// stationMemStore is an in-package ports.DiscoveredStore for the handler test.
type stationMemStore struct {
	sets map[string][]discovery.Discovered
}

func (m *stationMemStore) Report(_ context.Context, tenant, station string, d []discovery.Discovered, _ time.Time) error {
	m.sets[tenant+"|"+station] = d
	return nil
}
func (m *stationMemStore) List(_ context.Context, tenant, station string) ([]discovery.Discovered, error) {
	return m.sets[tenant+"|"+station], nil
}
func (m *stationMemStore) Remove(_ context.Context, tenant, station, mac string) error { return nil }

// stationAuth is a fake StationAuthenticator: only (goodSecret, goodStation)
// authenticates.
type stationAuth struct{ secret, station string }

func (a stationAuth) AuthenticateTag(_ context.Context, secret, claimedTag string) bool {
	return secret == a.secret && claimedTag == a.station
}

// HasCredential: the one station this fake knows about has one.
func (a stationAuth) HasCredential(_ context.Context, tag string) (bool, error) {
	return tag == a.station, nil
}

func newStationServer(t *testing.T, auth StationAuthenticator) (*httptest.Server, *stationMemStore) {
	t.Helper()
	store := &stationMemStore{sets: map[string][]discovery.Discovered{}}
	svc := app.NewDiscoveryService(store, fixedClock{time.Unix(1000, 0)}, "")
	api := NewStation(svc, nil, nil, auth, "", discardLog())
	mux := http.NewServeMux()
	api.Routes(mux)
	return httptest.NewServer(mux), store
}

func stationPost(t *testing.T, url, bearer, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

const goodReport = `{"devices":[{"mac":"aa:bb:cc:dd:ee:01","phase":"discovered"}]}`

func TestStationReportRequiresAuth(t *testing.T) {
	ts, store := newStationServer(t, stationAuth{secret: "s3cr3t", station: "nuc-1"})
	defer ts.Close()

	// No credential -> 401.
	noAuthResp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "", goodReport)
	noAuthResp.Body.Close()
	if noAuthResp.StatusCode != 401 {
		t.Fatalf("no auth = %d, want 401", noAuthResp.StatusCode)
	}
	// A credential for a DIFFERENT station -> 401 (bound to its own tag).
	crossResp := stationPost(t, ts.URL+"/api/station/nuc-2/report", "s3cr3t", goodReport)
	crossResp.Body.Close()
	if crossResp.StatusCode != 401 {
		t.Fatalf("cross-station = %d, want 401", crossResp.StatusCode)
	}
	if len(store.sets) != 0 {
		t.Fatal("an unauthorized report reached the store")
	}
}

func TestStationReportAcceptsAndStores(t *testing.T) {
	ts, store := newStationServer(t, stationAuth{secret: "s3cr3t", station: "nuc-1"})
	defer ts.Close()

	resp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "s3cr3t", goodReport)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report = %d, want 204", resp.StatusCode)
	}
	if got := store.sets["default|nuc-1"]; len(got) != 1 || got[0].MAC != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("report not stored: %+v", got)
	}
}

func TestStationReportRejectsUnauthorizedBeforeParsingBody(t *testing.T) {
	ts, store := newStationServer(t, stationAuth{secret: "s3cr3t", station: "nuc-1"})
	defer ts.Close()

	// No credential, and a body that is not even valid JSON: if the body
	// were decoded before the auth check, this would come back 400 (bad
	// report body) rather than 401 - 401 proves auth runs first.
	resp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "", "not json at all")
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("no auth with garbage body = %d, want 401", resp.StatusCode)
	}
	if len(store.sets) != 0 {
		t.Fatal("an unauthorized report reached the store")
	}
}

func TestStationReportRejectsBadPayload(t *testing.T) {
	ts, _ := newStationServer(t, stationAuth{secret: "s3cr3t", station: "nuc-1"})
	defer ts.Close()

	// Malformed MAC -> 400 (domain validation), even with valid auth.
	bad := `{"devices":[{"mac":"nope","phase":"discovered"}]}`
	badMACResp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "s3cr3t", bad)
	badMACResp.Body.Close()
	if badMACResp.StatusCode != 400 {
		t.Fatalf("bad MAC = %d, want 400", badMACResp.StatusCode)
	}
	badJSONResp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "s3cr3t", "{")
	badJSONResp.Body.Close()
	if badJSONResp.StatusCode != 400 {
		t.Fatalf("bad json = %d, want 400", badJSONResp.StatusCode)
	}
}

func TestStationReportDisabledWithoutAuth(t *testing.T) {
	ts, _ := newStationServer(t, nil)
	defer ts.Close()
	resp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "x", goodReport)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("disabled = %d, want 403", resp.StatusCode)
	}
}

// TestStationBridgeTokenCannotSpeakForACredentialedStation mirrors the
// check-in narrowing (checkin_test.go) on the station report path. This
// endpoint had no bridge-token test at all before 2026-08-06, which is how
// the shared token kept working for a credentialed station unnoticed.
func TestStationBridgeTokenCannotSpeakForACredentialedStation(t *testing.T) {
	store := &stationMemStore{sets: map[string][]discovery.Discovered{}}
	svc := app.NewDiscoveryService(store, fixedClock{time.Unix(1000, 0)}, "")
	// nuc-1 has its own credential; nuc-new does not.
	auth := stationAuth{secret: "s3cr3t", station: "nuc-1"}
	api := NewStation(svc, nil, nil, auth, "bridge-tok", discardLog())
	mux := http.NewServeMux()
	api.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	body := `{"devices":[{"mac":"aa:bb:cc:dd:ee:ff","phase":"pxe"}]}`

	// The migration case still works: a station with no credential of its own.
	resp := stationPost(t, srv.URL+"/api/station/nuc-new/report", "bridge-tok", body)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("bridge report for an un-credentialed station = %d, want 204", resp.StatusCode)
	}
	// The downgrade is closed: nuc-1 has its own credential.
	resp = stationPost(t, srv.URL+"/api/station/nuc-1/report", "bridge-tok", body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bridge report for a credentialed station = %d, want 401", resp.StatusCode)
	}
	// And nuc-1's own credential still works, so that 401 is the bridge being
	// narrowed rather than the station being locked out.
	resp = stationPost(t, srv.URL+"/api/station/nuc-1/report", "s3cr3t", body)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("nuc-1 with its own credential = %d, want 204", resp.StatusCode)
	}
}
