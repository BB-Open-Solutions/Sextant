package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

type allowBuilder struct{}

func (allowBuilder) Build(context.Context, string, []string) error { return nil }

type zeroConv struct{}

func (zeroConv) RingStatus(context.Context, []string, string) (rollout.RingStatus, error) {
	return rollout.RingStatus{}, nil
}

type tickClock struct{}

func (tickClock) Now() time.Time { return time.Now() }

// fullAPI wires every optional service so the route manifest is complete.
func fullAPI(t *testing.T) *API {
	t.Helper()
	svc, dir := seededService(t, seed)
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	allow := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	open := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	changes := app.NewChangeService(repo, st.Changes(), allow, allowBuilder{}, tickClock{}, open, svc)
	rollouts := app.NewRolloutService(svc, st.Rollouts(), zeroConv{}, tickClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	fo := newFakeObserved()
	inv := app.NewInventoryService(fo, fo, tickClock{}, "")
	toks := app.NewTokenService(newAPIMemTokenStore(), tickClock{}, 0)
	creds := app.NewDeviceCredentials(newAPIMemTokenStore(), tickClock{})

	a := New(Services{Config: svc, Changes: changes, Rollouts: rollouts, Inventory: inv,
		Tokens: toks, DevCreds: creds}, Authz{}, testToken, true, discardLog())
	a.Routes(http.NewServeMux())
	return a
}

// TestOpenAPIContract proves the published spec and the actual router never
// drift: every implemented route is documented and every documented route
// is implemented, in the shape a fully configured deployment serves.
func TestOpenAPIContract(t *testing.T) {
	a := fullAPI(t)

	implemented := map[string]bool{}
	for _, r := range a.routeManifest() {
		implemented[r] = true
	}

	var spec struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(openAPISpec, &spec); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	documented := map[string]bool{}
	for path, methods := range spec.Paths {
		for m := range methods {
			documented[strings.ToUpper(m)+" "+path] = true
		}
	}

	var missingFromSpec, missingFromImpl []string
	for r := range implemented {
		if !documented[r] {
			missingFromSpec = append(missingFromSpec, r)
		}
	}
	for r := range documented {
		if !implemented[r] {
			missingFromImpl = append(missingFromImpl, r)
		}
	}
	sort.Strings(missingFromSpec)
	sort.Strings(missingFromImpl)
	if len(missingFromSpec) > 0 {
		t.Errorf("implemented but not in openapi.json:\n  %s", strings.Join(missingFromSpec, "\n  "))
	}
	if len(missingFromImpl) > 0 {
		t.Errorf("documented but not implemented:\n  %s", strings.Join(missingFromImpl, "\n  "))
	}
}

func TestOpenAPIServedPublicly(t *testing.T) {
	srv := newTestAPI(t, false)
	resp, err := http.Get(srv.URL + "/api/v1/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("spec = %d, want 200 (public contract)", resp.StatusCode)
	}
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("spec body: %v", err)
	}
	if v["openapi"] == "" {
		t.Fatal("not an openapi document")
	}
}
