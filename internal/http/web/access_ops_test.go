package web_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// TestAccessPageGrantAndRevoke covers the access-control surface end to end:
// the page renders the current bindings, an owner may grant a new one (a
// direct ConfigService.Apply, same governed write path as settings), and
// revoke removes it again. It was entirely untested (0% coverage) despite
// sharing the exact write path ApplySettings uses.
func TestAccessPageGrantAndRevoke(t *testing.T) {
	ts, cfg := newConsole(t)
	c := client()

	// The page renders with no bindings yet.
	resp, err := c.Get(ts.URL + "/access")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("access page status = %d\n%s", resp.StatusCode, body)
	}

	// Grant a viewer binding for the "pilot" group at org scope.
	resp, err = c.PostForm(ts.URL+"/access/grant", url.Values{
		"csrf": {"dev-csrf"}, "group": {"pilot-team"}, "role": {"viewer"}, "scope": {"org"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("grant status = %d, want 303", resp.StatusCode)
	}
	if len(cfg.Fleet().Access) != 1 || cfg.Fleet().Access[0].Group != "pilot-team" {
		t.Fatalf("binding not recorded: %+v", cfg.Fleet().Access)
	}

	// The page now lists the granted binding.
	resp, err = c.Get(ts.URL + "/access")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "pilot-team") {
		t.Error("access page missing the granted binding")
	}

	// Revoke removes it again.
	resp, err = c.PostForm(ts.URL+"/access/revoke", url.Values{
		"csrf": {"dev-csrf"}, "group": {"pilot-team"}, "scope": {"org"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke status = %d, want 303", resp.StatusCode)
	}
	if len(cfg.Fleet().Access) != 0 {
		t.Fatalf("binding still present after revoke: %+v", cfg.Fleet().Access)
	}

	// Revoking a binding that does not exist surfaces the mutation error.
	resp, err = c.PostForm(ts.URL+"/access/revoke", url.Values{
		"csrf": {"dev-csrf"}, "group": {"ghost"}, "scope": {"org"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusSeeOther {
		t.Fatal("revoking a non-existent binding was accepted")
	}
}

// TestAccessGrantRequiresOwnerRole: a non-owner may not grant or revoke
// access - requireWeb's Owner check on both actions.
func TestAccessGrantRequiresOwnerRole(t *testing.T) {
	ts := newScopedConsole(t, identity.User{Subject: "u", Groups: []string{"alpha-team"}})

	resp, err := http.PostForm(ts.URL+"/access/grant", url.Values{
		"csrf": {"csrf"}, "group": {"x"}, "role": {"viewer"}, "scope": {"group:alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner grant status = %d, want 403", resp.StatusCode)
	}

	resp, err = http.PostForm(ts.URL+"/access/revoke", url.Values{
		"csrf": {"csrf"}, "group": {"x"}, "scope": {"group:alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner revoke status = %d, want 403", resp.StatusCode)
	}
}
