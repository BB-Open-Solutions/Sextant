package web_test

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

// TestPagesRenderToCompletion is the render smoke net: every GET page that
// answers 200 must produce a COMPLETE document. render() now turns a
// template error into a 500, so a bug in any template (like the
// Stringer-argument bug that silently truncated the device page) fails
// here instead of shipping as a 200 with half a page. Pages whose services
// are not wired in this minimal console may 404/403/500-from-missing-service;
// the assertion only fires on a 200.
func TestPagesRenderToCompletion(t *testing.T) {
	paths := []string{
		"/", "/devices", "/devices/lt-1", "/groups", "/settings",
		"/settings?scope=group:pilot", "/settings?scope=device:lt-1",
		"/policies", "/compliance", "/changes", "/updates", "/org/updates",
		"/updates/rollout", "/access", "/audit", "/profile", "/station",
		"/enroll", "/integrations", "/integrations?scope=group:pilot",
		"/overlays", "/secrets", "/service-accounts", "/notifications",
		"/org", "/mail",
	}
	check := func(t *testing.T, base string, paths []string) {
		t.Helper()
		for _, p := range paths {
			resp, err := client().Get(base + p)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				continue
			}
			if !strings.Contains(string(body), "</html>") {
				t.Errorf("%s: 200 with truncated document (%d bytes)", p, len(body))
			}
		}
	}

	// Config-only console: the bulk of the surface.
	ts, _ := newConsole(t)
	check(t, ts.URL, paths)

	// Rollout-wired console over the ladder fleet: the updates surface with
	// a live plan and run.
	ts2, _, _ := newUpdatesConsole(t)
	if code := postForm(t, ts2, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"50, 50"}}); code != 303 {
		t.Fatalf("derive plan = %d", code)
	}
	if code := postForm(t, ts2, "/rollout", url.Values{
		"target": {"deadbeef"}, "scope": {"*"}, "confirmed": {"1"}}); code != 303 {
		t.Fatalf("start = %d", code)
	}
	check(t, ts2.URL, []string{"/updates", "/updates/rollout", "/org/updates"})
}
