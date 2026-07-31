package oidc

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestNewSecureCookieKeySize(t *testing.T) {
	if _, err := newSecureCookie("s", make([]byte, 16), true, 3600); err == nil {
		t.Fatal("accepted a 16-byte key")
	}
	if _, err := newSecureCookie("s", testKey(), true, 3600); err != nil {
		t.Fatalf("rejected a valid 32-byte key: %v", err)
	}
}

func TestSecureCookieSealOpenRoundtrip(t *testing.T) {
	c, _ := newSecureCookie("sess", testKey(), true, 3600)
	type payload struct {
		Sub    string
		Groups []string
	}
	in := payload{Sub: "u-1", Groups: []string{"a", "b"}}
	sealed, err := c.seal(in)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed == "" {
		t.Fatal("empty ciphertext")
	}
	var out payload
	if err := c.open(sealed, &out); err != nil {
		t.Fatalf("open: %v", err)
	}
	if out.Sub != "u-1" || len(out.Groups) != 2 || out.Groups[1] != "b" {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
}

func TestSecureCookieOpenRejectsTampered(t *testing.T) {
	c, _ := newSecureCookie("sess", testKey(), true, 3600)
	sealed, _ := c.seal(map[string]string{"x": "y"})
	var out map[string]string
	// Flip a character in the ciphertext -> GCM auth fails.
	//
	// The replacement must be guaranteed DIFFERENT. Hard-coding "A" made this
	// a no-op roughly one run in sixty-four - whenever the sealed value
	// already began with an A - and the test then failed while nothing was
	// wrong. A security assertion that goes red at random is worse than none:
	// it teaches people to re-run until it passes.
	repl := byte('A')
	if sealed[0] == repl {
		repl = 'B'
	}
	bad := string(repl) + sealed[1:]
	if err := c.open(bad, &out); err == nil {
		t.Fatal("opened a tampered cookie")
	}
	// A different key cannot open it either.
	other, _ := newSecureCookie("sess", make([]byte, 32), true, 3600)
	if err := other.open(sealed, &out); err == nil {
		t.Fatal("opened with the wrong key")
	}
	// Garbage / too short.
	if err := c.open("!!!not-base64!!!", &out); err == nil {
		t.Fatal("opened non-base64")
	}
	if err := c.open("AAAA", &out); err == nil {
		t.Fatal("opened a too-short cookie")
	}
}

// TestSecureCookieAADBindsToName proves a ciphertext sealed by one
// secureCookie (e.g. the flow cookie) cannot be opened by another sharing
// the same key but a different name (e.g. the session cookie) - the fix for
// cross-cookie substitution, since sextant_session and sextant_flow are
// both sealed from the same SessionKey.
func TestSecureCookieAADBindsToName(t *testing.T) {
	key := testKey()
	sess, _ := newSecureCookie("sextant_session", key, false, 3600)
	flow, _ := newSecureCookie("sextant_flow", key, false, 600)

	sealedFlow, err := flow.seal(flowData{
		State: "s", Nonce: "n", Verifier: "v",
		Exp: time.Now().Add(10 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// The flow ciphertext must not open under the session cookie's name,
	// even with the identical key.
	var sd sessionData
	if err := sess.open(sealedFlow, &sd); err == nil {
		t.Fatal("flow cookie opened as a session cookie")
	}

	// It still opens correctly under its own name.
	var fd flowData
	if err := flow.open(sealedFlow, &fd); err != nil {
		t.Fatalf("flow cookie failed to open under its own name: %v", err)
	}
	if fd.State != "s" || fd.Nonce != "n" || fd.Verifier != "v" {
		t.Fatalf("flow roundtrip mismatch: %+v", fd)
	}
}

func TestSecureCookieSetGetClear(t *testing.T) {
	c, _ := newSecureCookie("sess", testKey(), false, 3600)
	rec := httptest.NewRecorder()
	if err := c.set(rec, map[string]string{"sub": "u-9"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Feed the Set-Cookie back into a request and read it.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, ck := range rec.Result().Cookies() {
		req.AddCookie(ck)
	}
	var out map[string]string
	if err := c.get(req, &out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if out["sub"] != "u-9" {
		t.Fatalf("get returned %v", out)
	}
	// clear emits an expiring cookie.
	rec2 := httptest.NewRecorder()
	c.clear(rec2)
	cs := rec2.Result().Cookies()
	if len(cs) != 1 || cs[0].MaxAge >= 0 {
		t.Fatalf("clear cookie = %+v", cs)
	}
}

func TestClaimsHelpers(t *testing.T) {
	if str(42) != "" || str("hi") != "hi" {
		t.Fatal("str wrong")
	}
	m := map[string]any{"preferred_username": "", "name": "Ada", "email": "ada@x"}
	if got := firstStr(m, "preferred_username", "name", "email"); got != "Ada" {
		t.Fatalf("firstStr = %q, want Ada (first non-empty)", got)
	}
	if got := firstStr(m, "missing"); got != "" {
		t.Fatalf("firstStr missing = %q", got)
	}
}

// TestRandString covers the normal path only: crypto/rand.Read on a
// healthy OS CSPRNG does not fail, and there is no supported way to force a
// deterministic failure from it in a unit test without replacing the
// package-level rand.Reader (which the standard library does not expose
// for that purpose). The failure path - panic instead of returning an
// all-zero/predictable token - is exercised by inspection of randString's
// implementation; see the comment there.
func TestRandString(t *testing.T) {
	a, b := randString(), randString()
	if a == "" || a == b {
		t.Fatalf("randString not random: %q %q", a, b)
	}
	// URL-safe base64: no +/= characters.
	for _, r := range a {
		if r == '+' || r == '/' || r == '=' {
			t.Fatalf("randString not URL-safe: %q", a)
		}
	}
}
