package identity

import "testing"

func TestRoleOrderingAndParse(t *testing.T) {
	if !Owner.Meets(Editor) || !Editor.Meets(Viewer) || !Viewer.Meets(Viewer) {
		t.Error("role ordering broken")
	}
	if Viewer.Meets(Editor) || None.Meets(Viewer) {
		t.Error("lower role meets higher")
	}
	for _, s := range []string{"viewer", "editor", "owner"} {
		r, err := ParseRole(s)
		if err != nil || r.String() != s {
			t.Errorf("round trip %q -> %v, %v", s, r, err)
		}
	}
	if _, err := ParseRole("admin"); err == nil {
		t.Error("unknown role accepted")
	}
}

func TestBindingValidate(t *testing.T) {
	ok := Binding{Group: "fo-admins", Role: "editor", Scope: "group:frontoffice"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Binding{
		{Role: "editor", Scope: "org"},
		{Group: "g", Role: "sudo", Scope: "org"},
		{Group: "g", Role: "editor", Scope: "device:lt-1"},
		{Group: "g", Role: "editor", Scope: "everywhere"},
	}
	for i, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

// testResolver: org -> zaanstad -> frontoffice tree; lt-1 in frontoffice.
func testResolver(bindings []Binding) Resolver {
	return Resolver{
		Ancestry: func(g string) []string {
			switch g {
			case "frontoffice":
				return []string{"zaanstad", "frontoffice"}
			case "zaanstad":
				return []string{"zaanstad"}
			}
			return nil
		},
		DeviceGroups: func(tag string) []string {
			if tag == "lt-1" {
				return []string{"frontoffice"}
			}
			return nil
		},
		Bindings: bindings,
	}
}

func TestRoleAtScopeAncestry(t *testing.T) {
	rv := testResolver([]Binding{
		{Group: "za-admins", Role: "owner", Scope: "group:zaanstad"},
		{Group: "fo-editors", Role: "editor", Scope: "group:frontoffice"},
		{Group: "auditors", Role: "viewer", Scope: "org"},
	})

	zaAdmin := User{Subject: "a", Groups: []string{"za-admins"}}
	foEditor := User{Subject: "b", Groups: []string{"fo-editors"}}
	auditor := User{Subject: "c", Groups: []string{"auditors"}}
	nobody := User{Subject: "d", Groups: []string{"random"}}

	cases := []struct {
		name string
		u    User
		ref  string
		want Role
	}{
		// A group binding flows down its subtree.
		{"za admin at zaanstad", zaAdmin, "group:zaanstad", Owner},
		{"za admin at subgroup", zaAdmin, "group:frontoffice", Owner},
		{"za admin at device in subtree", zaAdmin, "device:lt-1", Owner},
		{"za admin NOT at org", zaAdmin, "org", None},
		// A subgroup binding does not flow up.
		{"fo editor at frontoffice", foEditor, "group:frontoffice", Editor},
		{"fo editor at device", foEditor, "device:lt-1", Editor},
		{"fo editor NOT at parent", foEditor, "group:zaanstad", None},
		{"fo editor NOT at org", foEditor, "org", None},
		// An org binding governs everything.
		{"auditor at org", auditor, "org", Viewer},
		{"auditor at leaf device", auditor, "device:lt-1", Viewer},
		// No membership, no role.
		{"nobody anywhere", nobody, "device:lt-1", None},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rv.RoleAt(tc.u, tc.ref); got != tc.want {
				t.Fatalf("RoleAt = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHighestRoleWins(t *testing.T) {
	rv := testResolver([]Binding{
		{Group: "staff", Role: "viewer", Scope: "org"},
		{Group: "staff", Role: "editor", Scope: "group:frontoffice"},
	})
	u := User{Subject: "s", Groups: []string{"staff"}}
	if got := rv.RoleAt(u, "device:lt-1"); got != Editor {
		t.Fatalf("device role = %v, want editor (group binding beats org viewer)", got)
	}
	if got := rv.RoleAt(u, "org"); got != Viewer {
		t.Fatalf("org role = %v, want viewer", got)
	}
}

func TestBaselinesAreOrgWide(t *testing.T) {
	rv := testResolver(nil)
	rv.BaselineOwner = []string{"platform-ops"}
	rv.BaselineEditor = []string{"helpdesk"}
	rv.BaselineViewer = []string{"read-all"}

	if got := rv.RoleAt(User{Groups: []string{"platform-ops"}}, "device:lt-1"); got != Owner {
		t.Fatalf("baseline owner = %v", got)
	}
	if got := rv.RoleAt(User{Groups: []string{"helpdesk"}}, "group:zaanstad"); got != Editor {
		t.Fatalf("baseline editor = %v", got)
	}
	if got := rv.RoleAt(User{Groups: []string{"read-all"}}, "org"); got != Viewer {
		t.Fatalf("baseline viewer = %v", got)
	}
}

func TestServiceIsOwnerEverywhere(t *testing.T) {
	rv := testResolver(nil)
	svc := User{Subject: "svc:api", Service: true}
	if rv.RoleAt(svc, "org") != Owner || rv.RoleAt(svc, "device:lt-1") != Owner {
		t.Fatal("service principal must be owner")
	}
	if !rv.CanViewAnything(svc) {
		t.Fatal("service can view")
	}
}

func TestCanViewAnything(t *testing.T) {
	rv := testResolver([]Binding{
		{Group: "fo-editors", Role: "editor", Scope: "group:frontoffice"},
	})
	if !rv.CanViewAnything(User{Groups: []string{"fo-editors"}}) {
		t.Error("scoped editor should reach the console")
	}
	if rv.CanViewAnything(User{Groups: []string{"random"}}) {
		t.Error("roleless user reached the console")
	}
}
