package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		if len(out) > 0 && (strings.Contains(string(out), "nixpkgs/lib") || strings.Contains(string(out), "NIX_PATH")) {
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

// TestParity_FilterOperators: every filter operator this domain supports
// (eq/ne/prefix/in), plus AttrGroup ancestry and a label: rule, gated on the
// policyFleet fixture (lt-1/lt-2 in frontoffice, a subgroup of zaanstad;
// srv-1 directly in zaanstad with a site label). Mirrors the Go-side cases in
// filter_test.go so the nix twin is proven to agree on the same vocabulary,
// not just OpEq (which TestParity_Policies alone exercised).
func TestParity_FilterOperators(t *testing.T) {
	cases := []struct {
		name string
		rule FilterRule
	}{
		{"class ne", FilterRule{Attr: AttrClass, Op: OpNe, Value: "server"}},
		{"hardware prefix", FilterRule{Attr: AttrHardware, Op: OpPrefix, Value: "hp-"}},
		{"hardware in", FilterRule{Attr: AttrHardware, Op: OpIn, Values: []string{"t495s", "msi"}}},
		{"assignedUser ne", FilterRule{Attr: AttrAssignedUser, Op: OpNe, Value: "ada"}},
		{"label eq", FilterRule{Attr: "label:site", Op: OpEq, Value: "inspoelstraat"}},
		// AttrGroup ancestry: "zaanstad" matches all three devices (lt-1/lt-2
		// via the frontoffice subgroup, srv-1 directly); "frontoffice" only
		// matches lt-1/lt-2.
		{"group ancestor via parent", FilterRule{Attr: AttrGroup, Op: OpEq, Value: "zaanstad"}},
		{"group direct on subgroup", FilterRule{Attr: AttrGroup, Op: OpEq, Value: "frontoffice"}},
		{"group ne", FilterRule{Attr: AttrGroup, Op: OpNe, Value: "frontoffice"}},
		{"group prefix", FilterRule{Attr: AttrGroup, Op: OpPrefix, Value: "front"}},
		{"group in", FilterRule{Attr: AttrGroup, Op: OpIn, Values: []string{"frontoffice", "ghost"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := policyFleet(t)
			// Seed the filter directly: PutFilter validates exact group names
			// on console writes, but a document can also arrive via git with
			// a group that no longer exists ("ghost") - resolve-time parity
			// with the nix twin must hold for that document too.
			if f.Filters == nil {
				f.Filters = map[string]Filter{}
			}
			f.Filters["f"] = Filter{Rules: []FilterRule{tc.rule}}
			apply(t, f,
				PutPolicy("tag", Policy{Settings: map[string]any{"tagged": true}}),
				Assign(Assignment{Policy: "tag", Target: "org", Filter: "f"}),
			)
			for _, tag := range []string{"lt-1", "lt-2", "srv-1"} {
				t.Run(tag, func(t *testing.T) { assertParity(t, f, tag) })
			}
		})
	}
}

// TestParity_FilterMatchAny: a MatchAny filter (one rule suffices) selecting
// a mix of devices by hardware OR class - unexercised by any other parity
// case, which only use single-rule (implicit MatchAll) filters.
func TestParity_FilterMatchAny(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutFilter("hw-or-server", Filter{
			Match: MatchAny,
			Rules: []FilterRule{
				{Attr: AttrHardware, Op: OpEq, Value: "t495s"},
				{Attr: AttrClass, Op: OpEq, Value: "server"},
			},
		}),
		PutPolicy("tag", Policy{Settings: map[string]any{"tagged": true}}),
		Assign(Assignment{Policy: "tag", Target: "org", Filter: "hw-or-server"}),
	)
	for _, tag := range []string{"lt-1", "lt-2", "srv-1"} {
		t.Run(tag, func(t *testing.T) { assertParity(t, f, tag) })
	}
}

// TestParity_CrossHierarchyGroupOrderDecidesTies mirrors
// TestResolve_CrossHierarchyGroupOrderDecidesTies (resolve_test.go): a
// device in two unrelated group hierarchies gets its specificity from the
// ORDER groups appear in Device.Groups, not tree depth. chain.go flags this
// as the subtle case; nix/resolve.nix's scopePositions claims to match it
// exactly (see the comment there) - this proves it, for both the default
// and enforced tie-break, in both group orders.
func TestParity_CrossHierarchyGroupOrderDecidesTies(t *testing.T) {
	const groups = `
	  "a-root": {"settings": {"theme": "a", "lock": "a"}, "enforced": ["lock"]},
	  "b-root": {},
	  "b-leaf": {"parent": "b-root", "settings": {"theme": "b", "lock": "b"}, "enforced": ["lock"]}`

	fleetWithOrder := func(t *testing.T, order string) *Fleet {
		t.Helper()
		j := `{"version":3,"groups":{` + groups + `},"devices":{"d":{"groups":` + order + `,"hardware":"hw"}}}`
		f, err := Decode([]byte(j))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	assertParity(t, fleetWithOrder(t, `["b-leaf","a-root"]`), "d")
	assertParity(t, fleetWithOrder(t, `["a-root","b-leaf"]`), "d")
}
