package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// parity_test.go is the Go<->nix parity harness: nix/resolve.nix must
// resolve EXACTLY like this package on shared fixtures, or the console
// would show a different truth than the generator builds. The harness
// shells the real nix evaluator; it skips when nix or the vendored
// nixpkgs lib is unavailable (CI provides both via the flake devShell).

// nixResolution mirrors the JSON shape nix emits.
type nixResolution struct {
	Value    any    `json:"value"`
	Source   Source `json:"source"`
	Enforced bool   `json:"enforced"`
}

// runNixResolve evaluates nix/resolve.nix over a fleet document.
func runNixResolve(t *testing.T, f *Fleet, tag string) map[string]nixResolution {
	t.Helper()
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("nix not available")
	}
	doc, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fleetPath := filepath.Join(dir, "fleet.json")
	if err := os.WriteFile(fleetPath, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	// resolve.nix lives two levels up from this package.
	resolvePath, err := filepath.Abs(filepath.Join("..", "..", "..", "nix", "resolve.nix"))
	if err != nil {
		t.Fatal(err)
	}
	expr := fmt.Sprintf(
		`(import %s { }).resolve (builtins.fromJSON (builtins.readFile %s)) %q`,
		resolvePath, fleetPath, tag)
	out, err := exec.Command("nix", "eval", "--impure", "--json", "--expr", expr).CombinedOutput()
	if err != nil {
		if len(out) > 0 && (contains(out, "nixpkgs/lib") || contains(out, "NIX_PATH")) {
			t.Skipf("nixpkgs lib unavailable: %s", out)
		}
		t.Fatalf("nix eval: %v\n%s", err, out)
	}
	var got map[string]nixResolution
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse nix output: %v\n%s", err, out)
	}
	return got
}

func contains(b []byte, s string) bool {
	return len(b) >= len(s) && (string(b) == s || len(b) > 0 && indexOf(string(b), s) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// assertParity compares Go and nix resolution for one device.
func assertParity(t *testing.T, f *Fleet, tag string) {
	t.Helper()
	nixGot := runNixResolve(t, f, tag)
	goGot := f.Resolve(tag)

	if len(nixGot) != len(goGot) {
		t.Fatalf("key sets differ: nix=%d go=%d\nnix=%v\ngo=%v",
			len(nixGot), len(goGot), keys(nixGot), keysR(goGot))
	}
	for k, g := range goGot {
		n, ok := nixGot[k]
		if !ok {
			t.Errorf("%s: missing on the nix side", k)
			continue
		}
		// JSON round-trips Go ints to float64; normalize via JSON.
		if !jsonEqual(g.Value, n.Value) ||
			g.Source != n.Source || g.Enforced != n.Enforced {
			t.Errorf("%s:\n  go  = {%v %v enforced=%v}\n  nix = {%v %v enforced=%v}",
				k, g.Value, g.Source, g.Enforced, n.Value, n.Source, n.Enforced)
		}
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func keys(m map[string]nixResolution) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysR(m map[string]Resolution) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestParity_ScopeChain: the ported PoC semantics (inline scopes only).
func TestParity_ScopeChain(t *testing.T) {
	f := load(t)
	assertParity(t, f, "dev-a")
	assertParity(t, f, "dev-b")
}

// TestParity_GroupHierarchy: parent-enforced floor through a subgroup.
func TestParity_GroupHierarchy(t *testing.T) {
	const j = `{
	  "version": 3,
	  "org": {"settings": {"apps.office": true, "desktop": "plasma"}},
	  "groups": {
	    "zaanstad":    {"settings": {"desktop": "gnome", "secureboot": true}, "enforced": ["secureboot"]},
	    "frontoffice": {"parent": "zaanstad", "settings": {"apps.comms": true}}
	  },
	  "devices": {"d1": {"groups": ["frontoffice"], "hardware": "hw",
	    "settings": {"secureboot": false, "desktop": "plasma"}}}
	}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	assertParity(t, f, "d1")
}

// TestParity_Policies: the full model - policies, filters, priorities,
// enforcement, device targeting, dangling references.
func TestParity_Policies(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutFilter("laptops", Filter{Rules: []FilterRule{{Attr: AttrClass, Op: OpEq, Value: "laptop"}}}),
		PutPolicy("hardening", Policy{
			Settings: map[string]any{"secureboot": true, "apps.office": false},
			Enforced: []string{"secureboot"},
		}),
		PutPolicy("vpn", Policy{Settings: map[string]any{"netbird.enable": true}}),
		PutPolicy("special", Policy{Settings: map[string]any{"apps.office": true}}),
		Assign(Assignment{Policy: "hardening", Target: "org"}),
		Assign(Assignment{Policy: "vpn", Target: "org", Filter: "laptops"}),
		Assign(Assignment{Policy: "special", Target: "group:frontoffice", Priority: 5}),
		SetScopeSetting("device:lt-1", "secureboot", false),
		SetScopeSetting("group:zaanstad", "desktop", "gnome"),
	)
	for _, tag := range []string{"lt-1", "lt-2", "srv-1"} {
		t.Run(tag, func(t *testing.T) { assertParity(t, f, tag) })
	}
}

// TestParity_TieBreaks: equal specificity and priority resolve by
// assignment order, identically on both sides.
func TestParity_TieBreaks(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("p1", Policy{Settings: map[string]any{"x": "a"}}),
		PutPolicy("p2", Policy{Settings: map[string]any{"x": "b"}}),
		Assign(Assignment{Policy: "p1", Target: "org"}),
		Assign(Assignment{Policy: "p2", Target: "org"}),
		PutPolicy("inline-vs", Policy{Settings: map[string]any{"desktop": "kde"}}),
		Assign(Assignment{Policy: "inline-vs", Target: "org", Priority: 99}),
	)
	assertParity(t, f, "lt-1")
}
