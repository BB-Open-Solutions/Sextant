package api

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
	"sync"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newConfigOnlyService builds a ConfigService over a seeded temp repo.
func newConfigOnlyService(t *testing.T) *app.ConfigService {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "fleet.json")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// fakeObserved implements StatusStore + InventoryStore in memory.
type fakeObserved struct {
	mu     sync.Mutex
	status map[string]observed.DeviceStatus
	facts  map[string][]byte
}

func newFakeObserved() *fakeObserved {
	return &fakeObserved{status: map[string]observed.DeviceStatus{}, facts: map[string][]byte{}}
}

func (f *fakeObserved) key(tenant, tag string) string { return tenant + "/" + tag }

func (f *fakeObserved) Upsert(_ context.Context, tenant string, c observed.CheckIn, now time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status[f.key(tenant, c.Tag)]
	prevAck := st.Ack
	st.Tag = c.Tag
	if c.Revision != "" {
		st.Revision = c.Revision
	}
	if c.Phase != "" {
		st.Phase = c.Phase
	}
	if c.Ack != "" {
		st.Ack = c.Ack
	}
	st.Error = c.Error
	st.LastSeen = now
	f.status[f.key(tenant, c.Tag)] = st
	return prevAck != st.Ack, nil
}

func (f *fakeObserved) Get(_ context.Context, tenant, tag string) (observed.DeviceStatus, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.status[f.key(tenant, tag)]
	return st, ok, nil
}

func (f *fakeObserved) List(_ context.Context, tenant string) ([]observed.DeviceStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []observed.DeviceStatus
	for k, st := range f.status {
		if strings.HasPrefix(k, tenant+"/") {
			out = append(out, st)
		}
	}
	return out, nil
}

func (f *fakeObserved) Ping(context.Context) error { return nil }

func (f *fakeObserved) PutFacts(_ context.Context, tenant, tag string, facts []byte, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.facts[f.key(tenant, tag)] = facts
	return nil
}

func (f *fakeObserved) GetFacts(_ context.Context, tenant, tag string) ([]byte, time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.facts[f.key(tenant, tag)]
	return b, time.Time{}, ok, nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func newCheckinServer(t *testing.T, token string) (*httptest.Server, *fakeObserved) {
	t.Helper()
	fo := newFakeObserved()
	inv := app.NewInventoryService(fo, fo, fixedClock{time.Now()}, "")
	mux := http.NewServeMux()
	NewCheckin(inv, nil, token).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fo
}

func post(t *testing.T, url, token, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestCheckinAuth(t *testing.T) {
	srv, _ := newCheckinServer(t, "device-token")
	url := srv.URL + "/api/checkin"
	if got := post(t, url, "", `{"tag":"lt-1"}`); got != 401 {
		t.Errorf("no token = %d", got)
	}
	if got := post(t, url, "wrong", `{"tag":"lt-1"}`); got != 401 {
		t.Errorf("wrong token = %d", got)
	}
	if got := post(t, url, "device-token", `{"tag":"lt-1","revision":"v1","phase":"running"}`); got != 204 {
		t.Errorf("valid = %d, want 204", got)
	}
}

func TestCheckinRejectsMissingBearerBeforeParsingBody(t *testing.T) {
	srv, _ := newCheckinServer(t, "tok")
	url := srv.URL + "/api/checkin"

	// No Authorization header at all, and a body that is not valid JSON:
	// if the body were decoded before the auth check, this would come back
	// 400 (bad check-in body) rather than 401 - 401 proves the missing
	// bearer is rejected before the body is read/parsed.
	if got := post(t, url, "", "not json at all, and should never be parsed"); got != 401 {
		t.Errorf("no bearer with garbage body = %d, want 401", got)
	}
}

func TestCheckinDisabledWithoutToken(t *testing.T) {
	srv, _ := newCheckinServer(t, "")
	if got := post(t, srv.URL+"/api/checkin", "anything", `{"tag":"x"}`); got != 403 {
		t.Errorf("disabled = %d, want 403", got)
	}
}

func TestCheckinValidationAndFacts(t *testing.T) {
	srv, fo := newCheckinServer(t, "tok")
	url := srv.URL + "/api/checkin"

	if got := post(t, url, "tok", `{"revision":"v1"}`); got != 400 {
		t.Errorf("missing tag = %d", got)
	}
	if got := post(t, url, "tok", `{"tag":"x","phase":"flying"}`); got != 400 {
		t.Errorf("bad phase = %d", got)
	}
	if got := post(t, url, "tok", `not json`); got != 400 {
		t.Errorf("garbage = %d", got)
	}
	// Facts must be valid JSON.
	if got := post(t, url, "tok", `{"tag":"x","facts":{"cpu":"ryzen"}}`); got != 204 {
		t.Errorf("facts = %d", got)
	}
	if string(fo.facts["default/x"]) != `{"cpu":"ryzen"}` {
		t.Errorf("facts stored = %s", fo.facts["default/x"])
	}
}

func TestStatusEndpoints(t *testing.T) {
	fo := newFakeObserved()
	now := time.Now()
	inv := app.NewInventoryService(fo, fo, fixedClock{now}, "")
	_ = inv.CheckIn(context.Background(),
		observed.CheckIn{Tag: "lt-1", Revision: "v1", Phase: observed.Running}, nil)

	srv := newTestAPI(t, true) // config-only server has no inventory routes
	if code, _ := call(t, srv, "GET", "/api/v1/status", nil, testToken); code != 404 {
		t.Errorf("status without inventory service = %d, want 404 (unregistered)", code)
	}

	// Server with inventory mounted.
	mux := http.NewServeMux()
	svcSrv := newConfigOnlyService(t)
	New(Services{Config: svcSrv, Inventory: inv}, Authz{}, testToken, false, discardLog()).Routes(mux)
	s2 := httptest.NewServer(mux)
	t.Cleanup(s2.Close)

	code, body := call(t, s2, "GET", "/api/v1/status/lt-1", nil, testToken)
	if code != 200 {
		t.Fatalf("status = %d: %v", code, body)
	}
	if body["online"] != true || body["revision"] != "v1" {
		t.Errorf("body = %v", body)
	}
	if code, _ := call(t, s2, "GET", "/api/v1/status/ghost", nil, testToken); code != 404 {
		t.Errorf("unknown device status = %d", code)
	}
}

// fakeDevAuth verifies a device credential against a claimed tag.
type fakeDevAuth struct{ creds map[string]string } // secret -> tag

func (f *fakeDevAuth) AuthenticateTag(_ context.Context, secret, tag string) bool {
	got, ok := f.creds[secret]
	return ok && got == tag
}

func TestCheckinPerDeviceCredentialClosesImpersonation(t *testing.T) {
	fo := newFakeObserved()
	inv := app.NewInventoryService(fo, fo, fixedClock{time.Now()}, "")
	devs := &fakeDevAuth{creds: map[string]string{
		"cred-lt1": "lt-1",
		"cred-lt2": "lt-2",
	}}
	mux := http.NewServeMux()
	NewCheckin(inv, devs, "").Routes(mux) // no shared token: device creds only
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// lt-1 checks in as itself: ok.
	if got := post(t, srv.URL+"/api/checkin", "cred-lt1",
		`{"tag":"lt-1","revision":"v1","phase":"running"}`); got != 204 {
		t.Errorf("lt-1 self check-in = %d, want 204", got)
	}
	// lt-1's credential reporting tag lt-2: rejected (the gap is closed).
	if got := post(t, srv.URL+"/api/checkin", "cred-lt1",
		`{"tag":"lt-2","revision":"v1","phase":"running"}`); got != 401 {
		t.Errorf("lt-1 impersonating lt-2 = %d, want 401", got)
	}
	// Unknown credential: rejected.
	if got := post(t, srv.URL+"/api/checkin", "cred-ghost",
		`{"tag":"lt-1"}`); got != 401 {
		t.Errorf("unknown credential = %d, want 401", got)
	}
}

func TestCheckinSharedTokenBridgeStillWorks(t *testing.T) {
	fo := newFakeObserved()
	inv := app.NewInventoryService(fo, fo, fixedClock{time.Now()}, "")
	// No device creds, only the shared bridge token (migration mode).
	mux := http.NewServeMux()
	NewCheckin(inv, nil, "bridge-tok").Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if got := post(t, srv.URL+"/api/checkin", "bridge-tok",
		`{"tag":"any-device","revision":"v1"}`); got != 204 {
		t.Errorf("bridge token = %d, want 204", got)
	}
	if got := post(t, srv.URL+"/api/checkin", "wrong", `{"tag":"x"}`); got != 401 {
		t.Errorf("wrong shared token = %d, want 401", got)
	}
}

func TestCheckinRetiredDeviceGone(t *testing.T) {
	fo := newFakeObserved()
	inv := app.NewInventoryService(fo, fo, fixedClock{time.Now()}, "")
	mux := http.NewServeMux()
	// Even a valid credential (here: the bridge token) cannot resurrect a
	// retired tag - lifecycle beats auth.
	NewCheckin(inv, nil, "bridge-tok").
		WithLifecycle(func(tag string) bool { return tag == "parked" }).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if got := post(t, srv.URL+"/api/checkin", "bridge-tok",
		`{"tag":"parked","revision":"v1"}`); got != 410 {
		t.Errorf("retired check-in = %d, want 410", got)
	}
	if got := post(t, srv.URL+"/api/checkin", "bridge-tok",
		`{"tag":"active-1","revision":"v1"}`); got != 204 {
		t.Errorf("active check-in = %d, want 204", got)
	}
}

func TestCheckinReturnsIntent(t *testing.T) {
	fo := newFakeObserved()
	inv := app.NewInventoryService(fo, fo, fixedClock{time.Now()}, "")
	mux := http.NewServeMux()
	NewCheckin(inv, nil, "bridge-tok").
		WithIntent(func(_ context.Context, tag string) string {
			if tag == "stolen" {
				return "lock"
			}
			return ""
		}).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// A device with a pending intent gets 200 + the intent; others 204.
	if got := post(t, srv.URL+"/api/checkin", "bridge-tok",
		`{"tag":"stolen","revision":"v1"}`); got != 200 {
		t.Errorf("intent check-in = %d, want 200", got)
	}
	if got := post(t, srv.URL+"/api/checkin", "bridge-tok",
		`{"tag":"ordinary","revision":"v1"}`); got != 204 {
		t.Errorf("ordinary check-in = %d, want 204", got)
	}
	// The device may echo an ack; it is accepted.
	if got := post(t, srv.URL+"/api/checkin", "bridge-tok",
		`{"tag":"stolen","revision":"v1","ack":"lock"}`); got != 200 {
		t.Errorf("ack check-in = %d", got)
	}
}
