package web_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPolicyEditorFlow(t *testing.T) {
	ts, cfg := newConsole(t)
	post := func(path string, form url.Values) *http.Response {
		t.Helper()
		form.Set("csrf", "dev-csrf")
		resp, err := client().PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	// Create a policy: catalog-typed values parse (apps.office boolean).
	if resp := post("/policies", url.Values{"id": {"baseline"},
		"description": {"Baseline"},
		"settings":    {"apps.office = true\ndesktop = plasma"},
		"enforced":    {"apps.office"}}); resp.StatusCode != 303 {
		t.Fatalf("create policy = %d", resp.StatusCode)
	}
	p := cfg.Fleet().Policies["baseline"]
	if p.Settings["apps.office"] != true || p.Settings["desktop"] != "plasma" ||
		len(p.Enforced) != 1 {
		t.Fatalf("policy = %+v", p)
	}
	// Bad typed value rejected locally.
	if resp := post("/policies", url.Values{"id": {"bad"},
		"settings": {"apps.office = maybe"}}); resp.StatusCode != 400 {
		t.Fatalf("bad value = %d, want 400", resp.StatusCode)
	}

	// Filter, then assignment using it.
	if resp := post("/filters", url.Values{"id": {"laptops"}, "match": {"all"},
		"attr0": {"class"}, "op0": {"eq"}, "value0": {"laptop"}}); resp.StatusCode != 303 {
		t.Fatalf("create filter = %d", resp.StatusCode)
	}
	// A priority in the form is ignored (ADR 0026): the console no longer
	// offers the field, and a request that carries one anyway must not write
	// a number that decides nothing.
	if resp := post("/assignments", url.Values{"policy": {"baseline"},
		"target": {"group:pilot"}, "filter": {"laptops"}, "priority": {"5"}}); resp.StatusCode != 303 {
		t.Fatalf("assign = %d", resp.StatusCode)
	}
	f := cfg.Fleet()
	if len(f.Assignments) != 1 || f.Assignments[0].Filter != "laptops" {
		t.Fatalf("assignments = %+v", f.Assignments)
	}
	if f.Assignments[0].Priority != 0 {
		t.Errorf("priority %d was written from the form; the field is gone", f.Assignments[0].Priority)
	}

	// Page renders the editor state.
	resp, _ := client().Get(ts.URL + "/policies")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	for _, want := range []string{"baseline", "laptops", "Create a policy", "unassign"} {
		if !strings.Contains(page, want) {
			t.Errorf("policies page missing %q", want)
		}
	}

	// Deleting a policy that is still assigned is refused by the domain;
	// unassign first, then delete works.
	if resp := post("/policies/baseline/delete", url.Values{}); resp.StatusCode != 400 {
		t.Fatalf("delete assigned = %d, want 400", resp.StatusCode)
	}
	if resp := post("/assignments/delete", url.Values{"policy": {"baseline"},
		"target": {"group:pilot"}, "filter": {"laptops"}}); resp.StatusCode != 303 {
		t.Fatalf("unassign = %d", resp.StatusCode)
	}
	if resp := post("/filters/laptops/delete", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("delete filter = %d", resp.StatusCode)
	}
	if resp := post("/policies/baseline/delete", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("delete policy = %d", resp.StatusCode)
	}

	// Apps at a scope: dedup + firewall.
	if resp := post("/apps", url.Values{"scope": {"group:pilot"}, "kind": {"packages"},
		"names": {"vlc, firefox, vlc"}}); resp.StatusCode != 303 {
		t.Fatalf("apps set = %d", resp.StatusCode)
	}
	if got := cfg.Fleet().Groups["pilot"].Packages; len(got) != 2 {
		t.Fatalf("packages = %v", got)
	}
	if resp := post("/apps", url.Values{"scope": {"org"}, "kind": {"packages"},
		"names": {"bad name"}}); resp.StatusCode != 400 {
		t.Fatalf("injection = %d, want 400", resp.StatusCode)
	}
}

func TestProfileApplyFlow(t *testing.T) {
	ts, cfg := newConsole(t)
	post := func(path string, form url.Values) *http.Response {
		t.Helper()
		form.Set("csrf", "dev-csrf")
		resp, err := client().PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	// The page offers the overlay's profile.
	resp, _ := client().Get(ts.URL + "/policies")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Laptop workplace") {
		t.Fatal("profile card missing from /policies")
	}

	// Apply: one mutation yields policy + class filter + org assignment.
	if resp := post("/policies/profiles/laptop/apply", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("apply = %d", resp.StatusCode)
	}
	f := cfg.Fleet()
	pol, ok := f.Policies["laptop"]
	if !ok || !strings.HasPrefix(pol.Profile, "laptop@") {
		t.Fatalf("policy = %+v", pol)
	}
	if pol.Settings["desktop"] != "gnome" || pol.Settings["apps.office"] != true {
		t.Fatalf("settings = %+v", pol.Settings)
	}
	if _, ok := f.Filters["class-laptop"]; !ok {
		t.Fatalf("filters = %+v", f.Filters)
	}
	if len(f.Assignments) != 1 || f.Assignments[0].Target != "org" ||
		f.Assignments[0].Filter != "class-laptop" {
		t.Fatalf("assignments = %+v", f.Assignments)
	}

	// A hand edit through the policy editor must keep the provenance stamp.
	if resp := post("/policies", url.Values{"id": {"laptop"},
		"settings": {"desktop = plasma"}}); resp.StatusCode != 303 {
		t.Fatalf("edit = %d", resp.StatusCode)
	}
	if got := cfg.Fleet().Policies["laptop"]; got.Profile != pol.Profile {
		t.Fatalf("provenance lost on edit: %q", got.Profile)
	}

	// Unknown profile is a client error, not a crash.
	if resp := post("/policies/profiles/nope/apply", url.Values{}); resp.StatusCode != 400 {
		t.Fatalf("unknown profile = %d, want 400", resp.StatusCode)
	}
}
