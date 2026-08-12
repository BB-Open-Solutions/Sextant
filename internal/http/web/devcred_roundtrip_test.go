package web_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The credential is shown once. A cookie that survived the render would put
// the secret back on screen on every reload and leave it in the browser store
// in between, which is the opposite of once.
//
// Driven through the real device page rather than by writing the deletion
// this test then reads back. The first version did exactly that, and it
// proved only that http.SetCookie works: a mutation to the handler would have
// sailed past it. The cookie NAME is the literal a browser sees, and
// devcred_cookie_test.go asserts it still matches the constant.
const devCredCookieName = "sextant_devcred"

const oneDeviceFleet = `{
  "version": 3,
  "groups": {"pilot": {}},
  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hw"}}
}`

func TestTheDevicePageShowsTheCredentialOnceAndClearsIt(t *testing.T) {
	ts := newConsoleWithFleet(t, oneDeviceFleet)
	c := client()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/devices/lt-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "one-shot-secret-value"
	req.AddCookie(&http.Cookie{Name: devCredCookieName, Value: secret})

	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("device page = %d", resp.StatusCode)
	}

	// Shown: the whole point of the cookie.
	if !strings.Contains(string(body), secret) {
		t.Error("the credential did not reach the page, so the operator never sees it")
	}

	// And retired in the same response.
	var cleared *http.Cookie
	for _, sc := range resp.Cookies() {
		if sc.Name == devCredCookieName {
			cleared = sc
		}
	}
	if cleared == nil {
		t.Fatal("the page rendered the secret and did not clear the cookie; " +
			"it comes back on every reload")
	}
	if cleared.Value != "" {
		t.Errorf("the clearing cookie still carries %q", cleared.Value)
	}
	if cleared.MaxAge >= 0 {
		t.Errorf("MaxAge = %d; only a negative value deletes a cookie, and zero "+
			"means 'session cookie', which keeps it until the browser closes",
			cleared.MaxAge)
	}
	// A Set-Cookie on a different path creates a second cookie rather than
	// removing the first, so the deletion has to match the scope exactly.
	if cleared.Path != "/devices/lt-1" {
		t.Errorf("Path = %q, want /devices/lt-1; a mismatched path leaves the "+
			"original cookie in place", cleared.Path)
	}
}

// No cookie, no secret on the page and nothing to clear. Worth stating: a
// handler that emitted a deletion unconditionally would be harmless, but one
// that rendered an empty Credential block would show every operator an empty
// secret box on every visit.
func TestTheDevicePageWithoutACredentialCookieShowsNothing(t *testing.T) {
	ts := newConsoleWithFleet(t, oneDeviceFleet)
	resp, err := client().Get(ts.URL + "/devices/lt-1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("device page = %d", resp.StatusCode)
	}
	for _, sc := range resp.Cookies() {
		if sc.Name == devCredCookieName && sc.Value != "" {
			t.Errorf("a credential cookie was set on a plain visit: %q", sc.Value)
		}
	}
	if strings.Contains(string(body), "one-shot-secret-value") {
		t.Error("a secret from another request leaked onto the page")
	}
}
