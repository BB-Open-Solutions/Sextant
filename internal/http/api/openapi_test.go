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

// TestOpenAPIDocumentsItsErrors closes audit finding A1's first half. The
// contract test above proves the spec lists the right PATHS; it says nothing
// about what an operation answers, and until 2026-08-07 the spec documented
// only 200 and 201 while the API used twelve status codes.
//
// A client generated from a spec with no error responses has no error
// handling, and every 4xx is a surprise. This asserts the floor: every
// operation documents the refusals that can reach any endpoint, mutating
// operations additionally document the ones only a write can produce, and
// all of them point at the single Error schema the server actually writes.
func TestOpenAPIDocumentsItsErrors(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema map[string]any `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPISpec, &spec); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if _, ok := spec.Components.Schemas["Error"]; !ok {
		t.Fatal("no Error schema: the shape every failure carries is undocumented")
	}

	everywhere := []string{"401", "403", "404", "500"}
	onWrites := []string{"400", "409", "422", "503"}
	writeMethod := map[string]bool{"post": true, "put": true, "patch": true, "delete": true}

	for path, ops := range spec.Paths {
		for method, op := range ops {
			want := everywhere
			if writeMethod[strings.ToLower(method)] {
				want = append(append([]string{}, everywhere...), onWrites...)
			}
			for _, code := range want {
				resp, ok := op.Responses[code]
				if !ok {
					t.Errorf("%s %s: no %s documented", strings.ToUpper(method), path, code)
					continue
				}
				// A described-but-typeless error is only half the answer: a
				// generated client still cannot read the body.
				c, ok := resp.Content["application/json"]
				if !ok {
					t.Errorf("%s %s %s: no application/json body", strings.ToUpper(method), path, code)
					continue
				}
				if ref, _ := c.Schema["$ref"].(string); ref != "#/components/schemas/Error" {
					t.Errorf("%s %s %s: schema is %q, want the shared Error", strings.ToUpper(method), path, code, ref)
				}
			}
		}
	}
}

// TestTheServerWritesTheDocumentedErrorShape ties the spec to the server.
// Documenting {"error": "..."} is worth nothing if the server writes
// something else, and the two live in different files with nothing between
// them.
func TestTheServerWritesTheDocumentedErrorShape(t *testing.T) {
	srv := newTestAPI(t, false)
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/devices", nil)
	resp, err := http.DefaultClient.Do(req) // no token: a documented 401
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want the documented 401", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("the server did not write JSON: %v", err)
	}
	msg, ok := body["error"].(string)
	if !ok || msg == "" {
		t.Errorf("body = %v; the spec promises a non-empty \"error\" string", body)
	}
	if len(body) != 1 {
		t.Errorf("body carries %d fields; the Error schema documents exactly one", len(body))
	}
}

// TestDocumentedSuccessShapesMatchTheServer calls the endpoints whose 200
// schema was written from a measured response and checks the server still
// answers that shape.
//
// A documented shape the server does not write is worse than no
// documentation: it is a promise an integrator builds against. The schemas
// were derived by calling these endpoints rather than by reading the
// handlers, and this keeps them that way.
func TestDocumentedSuccessShapesMatchTheServer(t *testing.T) {
	srv := newTestAPI(t, true)

	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema map[string]any `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPISpec, &spec); err != nil {
		t.Fatal(err)
	}

	// schemaFor resolves a $ref or returns the inline schema.
	schemaFor := func(m map[string]any) map[string]any {
		if ref, ok := m["$ref"].(string); ok {
			return spec.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
		}
		return m
	}

	for _, c := range []struct{ path, kind string }{
		{"/api/v1/me", "object"},
		{"/api/v1/devices", "array"},
		{"/api/v1/audit", "array"},
		{"/api/v1/hostkeys", "array"},
		{"/api/v1/secret-refs", "array"},
		{"/api/v1/access", "array"},
	} {
		t.Run(c.path, func(t *testing.T) {
			op, ok := spec.Paths[c.path]["get"]
			if !ok {
				t.Fatalf("no GET %s in the spec", c.path)
			}
			body200, ok := op.Responses["200"]
			if !ok || body200.Content["application/json"].Schema == nil {
				t.Fatalf("GET %s has no documented 200 schema", c.path)
			}
			documented := body200.Content["application/json"].Schema
			if got, _ := documented["type"].(string); got != c.kind && documented["$ref"] == nil {
				t.Errorf("documented type = %q, want %q", got, c.kind)
			}

			req, _ := http.NewRequest("GET", srv.URL+c.path, nil)
			req.Header.Set("Authorization", "Bearer "+testToken)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			var actual any
			if err := json.NewDecoder(resp.Body).Decode(&actual); err != nil {
				t.Fatal(err)
			}
			switch c.kind {
			case "array":
				if _, ok := actual.([]any); !ok {
					t.Errorf("the spec documents an array; the server answered %T", actual)
				}
			case "object":
				m, ok := actual.(map[string]any)
				if !ok {
					t.Fatalf("the spec documents an object; the server answered %T", actual)
				}
				// Every REQUIRED documented property must be present. Extra
				// properties are fine - the contract is additive-only, so a
				// server ahead of the spec is expected and a server BEHIND it
				// is the break.
				sc := schemaFor(documented)
				req, _ := sc["required"].([]any)
				for _, k := range req {
					if _, ok := m[k.(string)]; !ok {
						t.Errorf("the spec requires %q and the server did not send it", k)
					}
				}
			}
		})
	}
}
