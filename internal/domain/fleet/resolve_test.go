package fleet

import (
	"encoding/json"
	"testing"
)

// The scope-resolution cases in this file are ported from the proven PoC
// resolver (dawo-fleet-console internal/fleet/resolve_test.go), adapted to
// schema v3 (no v1 feature/desktop sugar: everything is a settings key).
// They are the parity anchor: with zero policies the compiled chain must
// resolve exactly like the PoC scope chain.

const v3JSON = `{
  "version": 3,
  "org": {
    "settings": { "apps.office": true, "secureboot": false, "desktop": "plasma" },
    "enforced": ["secureboot"]
  },
  "groups": {
    "pilot":  { "settings": { "apps.comms": true, "apps.office": false } },
    "design": { "settings": { "desktop": "gnome", "apps.creative": true } }
  },
  "devices": {
    "dev-a": { "groups": ["pilot"], "hardware": "hw", "settings": { "secureboot": true } },
    "dev-b": { "groups": ["pilot","design"], "hardware": "hw", "settings": { "desktop": "plasma" } }
  }
}`

func load(t *testing.T) *Fleet {
	t.Helper()
	f, err := Decode([]byte(v3JSON))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func want(t *testing.T, r map[string]Resolution, key string, val any, src string, enf bool) {
	t.Helper()
	got, ok := r[key]
	if !ok {
		t.Fatalf("%s: missing", key)
	}
	if got.Value != val || got.Source.String() != src || got.Enforced != enf {
		t.Fatalf("%s = {%v %s enforced=%v}; want {%v %s enforced=%v}",
			key, got.Value, got.Source, got.Enforced, val, src, enf)
	}
}

func TestResolve_DefaultMostSpecificWins(t *testing.T) {
	f := load(t)
	r := f.Resolve("dev-a")
	// org sets apps.office=true (default), group:pilot overrides false.
	want(t, r, "apps.office", false, "group:pilot", false)
	want(t, r, "apps.comms", true, "group:pilot", false)
	// desktop only from org.
	want(t, r, "desktop", "plasma", "org", false)
}

func TestResolve_EnforcedMostGeneralWins(t *testing.T) {
	f := load(t)
	r := f.Resolve("dev-a")
	// org enforces secureboot=false; the device's own true is locked out.
	want(t, r, "secureboot", false, "org", true)
}

func TestResolve_MultiGroupAndDeviceDesktop(t *testing.T) {
	f := load(t)
	r := f.Resolve("dev-b")
	// design sets desktop gnome, device overrides plasma (most specific).
	want(t, r, "desktop", "plasma", "device", false)
	want(t, r, "apps.creative", true, "group:design", false)
	want(t, r, "apps.office", false, "group:pilot", false)
}

// TestResolve_GroupHierarchy: a subgroup inherits its parent, and the parent
// can enforce a floor the subgroup and device cannot override.
func TestResolve_GroupHierarchy(t *testing.T) {
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
	if got := f.GroupAncestry("frontoffice"); len(got) != 2 || got[0] != "zaanstad" || got[1] != "frontoffice" {
		t.Fatalf("ancestry = %v, want [zaanstad frontoffice]", got)
	}
	r := f.Resolve("d1")
	// parent enforces secureboot=true, beating the device's own false.
	want(t, r, "secureboot", true, "group:zaanstad", true)
	// parent sets desktop gnome (default), device overrides (most specific).
	want(t, r, "desktop", "plasma", "device", false)
	want(t, r, "apps.comms", true, "group:frontoffice", false)
	want(t, r, "apps.office", true, "org", false)
}

// TestResolve_DeviceEnforced: device-level enforced is read from the flat
// Device.Enforced field. Parity anchor for the nix twin.
func TestResolve_DeviceEnforced(t *testing.T) {
	const j = `{"version":3,"groups":{"g":{}},"devices":{"d":{"groups":["g"],"hardware":"hw",
	  "settings":{"desktop":"gnome"},"enforced":["desktop"]}}}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	want(t, f.Resolve("d"), "desktop", "gnome", "device", true)
}

// TestResolve_CrossHierarchyGroupOrderDecidesTies: when a device belongs to
// two UNRELATED group hierarchies, scopePositions (chain.go) assigns
// specificity by the order groups appear in Device.Groups - a first-seen scan
// of each group's ancestry - NOT by how deep a group sits in its own tree.
// This is intentional (it matches the nix twin, nix/resolve.nix) and is the
// documented, tested contract this case pins down: two fixtures with the
// SAME group trees but a swapped Device.Groups order produce OPPOSITE
// winners, for both default (most-specific-wins) and enforced
// (most-general-wins) semantics.
//
//	a-root:          a lone root group, tree depth 1.
//	b-root -> b-leaf: a two-level chain, tree depth 2 at the leaf.
//
// If depth decided ties, b-leaf (deeper) would always beat a-root regardless
// of array order. It does not: array order decides, and reversing it flips
// both outcomes.
func TestResolve_CrossHierarchyGroupOrderDecidesTies(t *testing.T) {
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

	// b-leaf first: ancestry scan assigns group:b-root=1, group:b-leaf=2,
	// then group:a-root=3 - a-root ends up MOST specific despite being the
	// shallower tree, so it wins the default key; b-leaf ends up MORE
	// general than a-root, so it wins the enforced key.
	fBLeafFirst := fleetWithOrder(t, `["b-leaf","a-root"]`)
	want(t, fBLeafFirst.Resolve("d"), "theme", "a", "group:a-root", false)
	want(t, fBLeafFirst.Resolve("d"), "lock", "b", "group:b-leaf", true)

	// Same trees, reversed device order: a-root first, so a-root=1,
	// group:b-root=2, group:b-leaf=3 - the winners flip on BOTH keys, purely
	// from array order, with the tree structure unchanged.
	fARootFirst := fleetWithOrder(t, `["a-root","b-leaf"]`)
	want(t, fARootFirst.Resolve("d"), "theme", "b", "group:b-leaf", false)
	want(t, fARootFirst.Resolve("d"), "lock", "a", "group:a-root", true)
}

func TestSetScopeSettingAndEnforce(t *testing.T) {
	f := load(t)
	if err := SetScopeSetting("group:pilot", "apps.media", true)(f); err != nil {
		t.Fatal(err)
	}
	if err := SetScopeEnforce("group:pilot", "apps.media", true)(f); err != nil {
		t.Fatal(err)
	}
	want(t, f.Resolve("dev-a"), "apps.media", true, "group:pilot", true)

	// enforcing a key with no value at that scope is rejected.
	if err := SetScopeEnforce("org", "apps.dev", true)(f); err == nil {
		t.Fatal("expected error enforcing unset key")
	}
	// unknown scope rejected.
	if err := SetScopeSetting("group:nope", "x", 1)(f); err == nil {
		t.Fatal("expected error for unknown group")
	}
}

func TestClearScopeSettingRevertsToInherited(t *testing.T) {
	f := load(t)
	// Lift the org enforce: the device's own secureboot=true surfaces.
	if err := SetScopeEnforce("org", "secureboot", false)(f); err != nil {
		t.Fatal(err)
	}
	want(t, f.Resolve("dev-a"), "secureboot", true, "device", false)

	// Clear the device override: falls back to org default false.
	if err := ClearScopeSetting("device:dev-a", "secureboot")(f); err != nil {
		t.Fatal(err)
	}
	want(t, f.Resolve("dev-a"), "secureboot", false, "org", false)
}

func TestSetGroupParent_CycleRejected(t *testing.T) {
	const j = `{"version":3,"groups":{"a":{},"b":{"parent":"a"}},"devices":{}}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetGroupParent("a", "b")(f); err == nil {
		t.Fatal("expected cycle rejection for a->b")
	}
	if err := SetGroupParent("b", "")(f); err != nil {
		t.Fatalf("detach should succeed: %v", err)
	}
}

func TestAcceptanceFor(t *testing.T) {
	const j = `{"version":3,
	  "org":{"accepted":{"secureboot":"org-wide: legacy hardware"}},
	  "groups":{"pilot":{}},
	  "devices":{"d1":{"groups":["pilot"],"hardware":"hw"}}}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	if r, sc, ok := f.AcceptanceFor("d1", "secureboot"); !ok || sc != "org" || r == "" {
		t.Fatalf("AcceptanceFor = (%q,%q,%v), want org reason", r, sc, ok)
	}
	if err := SetAcceptance("device:d1", "secureboot", "this device: kiosk")(f); err != nil {
		t.Fatal(err)
	}
	if r, sc, _ := f.AcceptanceFor("d1", "secureboot"); sc != "device" || r != "this device: kiosk" {
		t.Errorf("device acceptance = (%q,%q)", r, sc)
	}
	if _, _, ok := f.AcceptanceFor("d1", "apps.office"); ok {
		t.Error("unexpected acceptance for apps.office")
	}
	if err := SetAcceptance("org", "x", "  ")(f); err == nil {
		t.Error("empty-reason acceptance should be rejected")
	}
	if err := ClearAcceptance("device:d1", "secureboot")(f); err != nil {
		t.Fatal(err)
	}
	if _, sc, _ := f.AcceptanceFor("d1", "secureboot"); sc != "org" {
		t.Errorf("after clear, scope = %q, want org", sc)
	}
}

func TestDecodeRejectsWrongVersion(t *testing.T) {
	if _, err := Decode([]byte(`{"version":2,"groups":{},"devices":{}}`)); err == nil {
		t.Fatal("v2 document must be rejected")
	}
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Fatal("garbage must be rejected")
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	f := load(t)
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	g, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(f.Resolve("dev-b"))
	bb, _ := json.Marshal(g.Resolve("dev-b"))
	if string(a) != string(bb) {
		t.Error("resolution differs after encode/decode round trip")
	}
}
