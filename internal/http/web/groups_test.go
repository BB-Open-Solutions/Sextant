package web_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGroupsPageLifecycle(t *testing.T) {
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

	// Create root group and a child.
	if resp := post("/groups", url.Values{"name": {"zaanstad"}}); resp.StatusCode != 303 {
		t.Fatalf("add root = %d", resp.StatusCode)
	}
	if resp := post("/groups", url.Values{"name": {"front"}, "parent": {"zaanstad"},
		"idpGroup": {"idp-front"}}); resp.StatusCode != 303 {
		t.Fatalf("add child = %d", resp.StatusCode)
	}
	f := cfg.Fleet()
	if f.Groups["front"].Parent != "zaanstad" || f.Groups["front"].IdpGroup != "idp-front" {
		t.Fatalf("child = %+v", f.Groups["front"])
	}

	// Tree renders with indentation and both nodes.
	resp, _ := client().Get(ts.URL + "/groups")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	for _, want := range []string{"zaanstad", "front", "Create a group"} {
		if !strings.Contains(page, want) {
			t.Errorf("groups page missing %q", want)
		}
	}

	// Re-parent to root, remap idp, then remove.
	if resp := post("/groups/front/update", url.Values{"reparent": {"1"}, "parent": {""}}); resp.StatusCode != 303 {
		t.Fatalf("reparent = %d", resp.StatusCode)
	}
	if cfg.Fleet().Groups["front"].Parent != "" {
		t.Fatal("not re-parented")
	}
	if resp := post("/groups/front/update", url.Values{"setidp": {"1"}, "idpGroup": {"idp-new"}}); resp.StatusCode != 303 {
		t.Fatalf("remap = %d", resp.StatusCode)
	}
	if cfg.Fleet().Groups["front"].IdpGroup != "idp-new" {
		t.Fatal("idp not remapped")
	}
	if resp := post("/groups/front/remove", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("remove = %d", resp.StatusCode)
	}
	if _, ok := cfg.Fleet().Groups["front"]; ok {
		t.Fatal("group survived removal")
	}
	// Removing a group with devices is refused (pilot holds lt-1).
	if resp := post("/groups/pilot/remove", url.Values{}); resp.StatusCode != 400 {
		t.Fatalf("remove referenced = %d, want 400", resp.StatusCode)
	}
}
