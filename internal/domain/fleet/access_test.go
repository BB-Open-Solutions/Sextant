package fleet

import "testing"

// Grant/Revoke are the access-control write path; these cover the guards a
// reviewer expects to be the most tested: validation, the unknown-group-scope
// check, replace-not-duplicate, and revoke-of-nonexistent.

func TestGrantAddsAndReplaces(t *testing.T) {
	f := &Fleet{Version: Version, Groups: map[string]Group{"zaanstad": {}}}
	if err := Grant(AccessBinding{Group: "zaan-eds", Role: "editor", Scope: "group:zaanstad"})(f); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	if len(f.Access) != 1 || f.Access[0].Role != "editor" {
		t.Fatalf("access = %+v, want one editor binding", f.Access)
	}
	// Re-granting the same (group, scope) with a new role replaces it, so a
	// grant is also a role change - not a second binding.
	if err := Grant(AccessBinding{Group: "zaan-eds", Role: "owner", Scope: "group:zaanstad"})(f); err != nil {
		t.Fatal(err)
	}
	if len(f.Access) != 1 || f.Access[0].Role != "owner" {
		t.Fatalf("grant should replace the existing pair, got %+v", f.Access)
	}
}

func TestGrantRejectsInvalid(t *testing.T) {
	f := &Fleet{Version: Version, Groups: map[string]Group{"zaanstad": {}}}
	cases := map[string]AccessBinding{
		"empty group":     {Group: "", Role: "owner", Scope: "org"},
		"unknown role":    {Group: "g", Role: "superuser", Scope: "org"},
		"device scope":    {Group: "g", Role: "owner", Scope: "device:lt-1"},
		"unknown group":   {Group: "g", Role: "owner", Scope: "group:does-not-exist"},
		"empty scope str": {Group: "g", Role: "owner", Scope: ""},
	}
	for name, b := range cases {
		if err := Grant(b)(f); err == nil {
			t.Errorf("%s: expected a rejection", name)
		}
	}
	if len(f.Access) != 0 {
		t.Fatalf("no invalid grant should persist, got %+v", f.Access)
	}
}

func TestRevoke(t *testing.T) {
	f := &Fleet{Version: Version, Groups: map[string]Group{"zaanstad": {}}}
	if err := Grant(AccessBinding{Group: "g", Role: "owner", Scope: "org"})(f); err != nil {
		t.Fatal(err)
	}
	if err := Revoke("g", "org")(f); err != nil {
		t.Fatalf("revoke of an existing binding: %v", err)
	}
	if len(f.Access) != 0 {
		t.Fatalf("revoke should remove the binding, got %+v", f.Access)
	}
	if err := Revoke("g", "org")(f); err == nil {
		t.Fatal("revoke of a nonexistent binding should error")
	}
}
