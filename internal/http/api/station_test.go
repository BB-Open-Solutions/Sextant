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

func newStationServer(t *testing.T, auth StationAuthenticator, shared string) (*httptest.Server, *stationMemStore) {
	t.Helper()
	store := &stationMemStore{sets: map[string][]discovery.Discovered{}}
	svc := app.NewDiscoveryService(store, fixedClock{time.Unix(1000, 0)}, "")
	api := NewStation(svc, nil, nil, auth, shared, discardLog())
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
	ts, store := newStationServer(t, stationAuth{secret: "s3cr3t", station: "nuc-1"}, "")
	defer ts.Close()

	// No credential -> 401.
	if resp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "", goodReport); resp.StatusCode != 401 {
		t.Fatalf("no auth = %d, want 401", resp.StatusCode)
	}
	// A credential for a DIFFERENT station -> 401 (bound to its own tag).
	if resp := stationPost(t, ts.URL+"/api/station/nuc-2/report", "s3cr3t", goodReport); resp.StatusCode != 401 {
		t.Fatalf("cross-station = %d, want 401", resp.StatusCode)
	}
	if len(store.sets) != 0 {
		t.Fatal("an unauthorized report reached the store")
	}
}

func TestStationReportAcceptsAndStores(t *testing.T) {
	ts, store := newStationServer(t, stationAuth{secret: "s3cr3t", station: "nuc-1"}, "")
	defer ts.Close()

	resp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "s3cr3t", goodReport)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report = %d, want 204", resp.StatusCode)
	}
	if got := store.sets["default|nuc-1"]; len(got) != 1 || got[0].MAC != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("report not stored: %+v", got)
	}
}

func TestStationReportRejectsBadPayload(t *testing.T) {
	ts, _ := newStationServer(t, stationAuth{secret: "s3cr3t", station: "nuc-1"}, "")
	defer ts.Close()

	// Malformed MAC -> 400 (domain validation), even with valid auth.
	bad := `{"devices":[{"mac":"nope","phase":"discovered"}]}`
	if resp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "s3cr3t", bad); resp.StatusCode != 400 {
		t.Fatalf("bad MAC = %d, want 400", resp.StatusCode)
	}
	if resp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "s3cr3t", "{"); resp.StatusCode != 400 {
		t.Fatalf("bad json = %d, want 400", resp.StatusCode)
	}
}

func TestStationReportDisabledWithoutAuth(t *testing.T) {
	ts, _ := newStationServer(t, nil, "")
	defer ts.Close()
	if resp := stationPost(t, ts.URL+"/api/station/nuc-1/report", "x", goodReport); resp.StatusCode != 403 {
		t.Fatalf("disabled = %d, want 403", resp.StatusCode)
	}
}
