package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
)

// memElevationStore is the queue in memory; the store's own behaviour is
// covered against a real Postgres elsewhere.
type memElevationStore struct{ m map[string]elevation.Request }

func (s *memElevationStore) Put(_ context.Context, _ string, r elevation.Request) error {
	s.m[r.ID] = r
	return nil
}

func (s *memElevationStore) Get(_ context.Context, _, id string) (elevation.Request, bool, error) {
	r, ok := s.m[id]
	return r, ok, nil
}

func (s *memElevationStore) Pending(_ context.Context, _ string) ([]elevation.Request, error) {
	out := make([]elevation.Request, 0, len(s.m))
	for _, r := range s.m {
		out = append(out, r)
	}
	return out, nil
}

type movableClock struct{ t time.Time }

func (c *movableClock) Now() time.Time { return c.t }

func elevationServer(t *testing.T) (*httptest.Server, *app.ElevationService, *movableClock) {
	t.Helper()
	fo := newFakeObserved()
	clock := &movableClock{t: time.Now()}
	inv := app.NewInventoryService(fo, fo, fixedClock{clock.t}, "")
	store := &memElevationStore{m: map[string]elevation.Request{}}
	svc := app.NewElevationService(store, clock, "")
	devs := &fakeDevAuth{creds: map[string]string{"cred-lt1": "lt-1", "cred-lt2": "lt-2"}}
	mux := http.NewServeMux()
	NewCheckin(inv, devs, "").
		WithElevation(svc).
		WithClock(func() time.Time { return clock.t }).
		Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, svc, clock
}

// raise always targets lt-1. What varies is the CREDENTIAL, which is the
// point: presenting lt-2's credential for lt-1's path must be refused.
func raise(t *testing.T, srv *httptest.Server, cred, body string) (int, elevationAnswer) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/api/device/lt-1/elevation", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+cred)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out elevationAnswer
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func poll(t *testing.T, srv *httptest.Server, cred, tag, id string) (int, elevationAnswer) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/api/device/"+tag+"/elevation/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+cred)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out elevationAnswer
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestElevationRaiseThenPollUntilApproved(t *testing.T) {
	srv, svc, clock := elevationServer(t)

	code, got := raise(t, srv, "cred-lt1",
		`{"user":"bbuijs","action":"org.freedesktop.NetworkManager.settings.modify.system","reason":"office wifi"}`)
	if code != 201 || got.ID == "" {
		t.Fatalf("raise = %d %+v, want 201 with an id", code, got)
	}
	if got.Granted {
		t.Fatal("a request granted the elevation the moment it was made")
	}

	code, got = poll(t, srv, "cred-lt1", "lt-1", got.ID)
	if code != 200 || got.State != string(elevation.Pending) || got.Granted {
		t.Fatalf("poll before an answer = %d %+v, want pending and not granted", code, got)
	}

	if _, err := svc.Decide(context.Background(), got.ID, true, "beheerder"); err != nil {
		t.Fatal(err)
	}
	code, got = poll(t, srv, "cred-lt1", "lt-1", got.ID)
	if code != 200 || !got.Granted {
		t.Fatalf("poll after approval = %d %+v, want granted", code, got)
	}

	// And the grant expires with its request rather than lingering.
	clock.t = clock.t.Add(elevation.TTL)
	_, got = poll(t, srv, "cred-lt1", "lt-1", got.ID)
	if got.Granted {
		t.Fatal("an approval still granted after its window closed")
	}
}

// The device in the path is the authenticated one. Without this a device could
// raise a request in another's name, have it approved, and claim the answer.
func TestADeviceCannotActAsAnother(t *testing.T) {
	srv, _, _ := elevationServer(t)
	if code, _ := raise(t, srv, "cred-lt2", `{"user":"bbuijs"}`); code != 401 {
		t.Errorf("lt-2 raising as lt-1 = %d, want 401", code)
	}
	code, got := raise(t, srv, "cred-lt1", `{"user":"bbuijs"}`)
	if code != 201 {
		t.Fatalf("raise = %d", code)
	}
	// The id alone must not be enough for another device to read the answer,
	// and the refusal must not reveal whether the id exists - otherwise a
	// device could probe for other devices' request ids.
	if c, _ := poll(t, srv, "cred-lt2", "lt-2", got.ID); c != 404 {
		t.Errorf("lt-2 polling lt-1's request = %d, want 404", c)
	}
}

func TestElevationRequiresAuthentication(t *testing.T) {
	srv, _, _ := elevationServer(t)
	req, _ := http.NewRequest("POST", srv.URL+"/api/device/lt-1/elevation",
		bytes.NewBufferString(`{"user":"bbuijs"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("an unauthenticated raise = %d, want 401", resp.StatusCode)
	}
}

func TestElevationRejectsAnAnonymousAsk(t *testing.T) {
	srv, _, _ := elevationServer(t)
	if code, _ := raise(t, srv, "cred-lt1", `{}`); code != 400 {
		t.Errorf("a request with no user = %d, want 400 - nobody could tell who to approve", code)
	}
	if code, _ := raise(t, srv, "cred-lt1", `not json`); code != 400 {
		t.Errorf("malformed body = %d, want 400", code)
	}
}

// Without the service wired the endpoints must say the console cannot help,
// not 404 - a device that gets 404 will conclude it is calling the wrong path
// and the person debugging it will look in the wrong place.
func TestElevationWithoutTheServiceSaysUnavailable(t *testing.T) {
	fo := newFakeObserved()
	inv := app.NewInventoryService(fo, fo, fixedClock{time.Now()}, "")
	devs := &fakeDevAuth{creds: map[string]string{"cred-lt1": "lt-1"}}
	mux := http.NewServeMux()
	NewCheckin(inv, devs, "").Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if code, _ := raise(t, srv, "cred-lt1", `{"user":"bbuijs"}`); code != 503 {
		t.Errorf("raise without the service = %d, want 503", code)
	}
}
