package oidc

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// TestGroupsFromClaims covers the provider shapes the extractor tolerates.
// Ported behaviour from the proven PoC (Keycloak, Zitadel, Entra).
func TestGroupsFromClaims(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
		claim  string
		want   []string
	}{
		{
			"keycloak flat array",
			map[string]any{"groups": []any{"admins", "editors"}},
			"groups",
			[]string{"admins", "editors"},
		},
		{
			"zitadel roles map at configured claim",
			map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{
				"fleet-admin": map[string]any{"1": "org.example"},
			}},
			"urn:zitadel:iam:org:project:roles",
			[]string{"fleet-admin"},
		},
		{
			"zitadel roles map found by suffix",
			map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{
				"fleet-admin": map[string]any{},
			}},
			"groups", // configured claim absent; suffix match picks it up
			[]string{"fleet-admin"},
		},
		{
			"entra app roles flat array",
			map[string]any{"roles": []any{"Console.Admin"}},
			"groups",
			[]string{"Console.Admin"},
		},
		{
			"merged and deduped",
			map[string]any{
				"groups": []any{"a", "b"},
				"roles":  []any{"b", "c"},
			},
			"groups",
			[]string{"a", "b", "c"},
		},
		{
			"non-string entries ignored",
			map[string]any{"groups": []any{"ok", 42, nil}},
			"groups",
			[]string{"ok"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := groupsFromClaims(tc.claims, tc.claim)
			slices.Sort(got)
			slices.Sort(tc.want)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("groups = %v, want %v", got, tc.want)
			}
		})
	}
}

func key32() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestSecureCookieRoundTripAndTamper(t *testing.T) {
	c, err := newSecureCookie("t", key32(), false, 60)
	if err != nil {
		t.Fatal(err)
	}
	type payload struct{ A string }
	rec := httptest.NewRecorder()
	if err := c.set(rec, payload{A: "secret"}); err != nil {
		t.Fatal(err)
	}
	ck := rec.Result().Cookies()[0]
	if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
		t.Error("cookie flags wrong")
	}

	// Round trip.
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(ck)
	var out payload
	if err := c.get(r, &out); err != nil || out.A != "secret" {
		t.Fatalf("round trip = %+v, %v", out, err)
	}

	// Tampering breaks authentication.
	r2 := httptest.NewRequest("GET", "/", nil)
	tampered := *ck
	tampered.Value = "x" + tampered.Value[1:]
	r2.AddCookie(&tampered)
	if err := c.get(r2, &out); err == nil {
		t.Fatal("tampered cookie accepted")
	}

	// Wrong key size refused.
	if _, err := newSecureCookie("t", []byte("short"), false, 60); err == nil {
		t.Fatal("short key accepted")
	}
}

// TestSessionUser exercises the session read path without a live IdP: seal
// a session directly and let SessionUser open it.
func TestSessionUser(t *testing.T) {
	sess, _ := newSecureCookie("sextant_session", key32(), false, 3600)
	a := &Authenticator{sess: sess}

	sd := sessionData{Subject: "sub-1", Name: "Ada", Email: "ada@x",
		Groups: []string{"fo-editors"}, CSRF: "csrf-token",
		Exp: time.Now().Add(time.Hour).Unix()}
	rec := httptest.NewRecorder()
	if err := sess.set(rec, sd); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(rec.Result().Cookies()[0])

	u, csrf, ok := a.SessionUser(r)
	if !ok || u.Subject != "sub-1" || csrf != "csrf-token" || len(u.Groups) != 1 {
		t.Fatalf("session = %+v %q %v", u, csrf, ok)
	}

	// Expired session rejected.
	sd.Exp = time.Now().Add(-time.Minute).Unix()
	rec2 := httptest.NewRecorder()
	_ = sess.set(rec2, sd)
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(rec2.Result().Cookies()[0])
	if _, _, ok := a.SessionUser(r2); ok {
		t.Fatal("expired session accepted")
	}

	// No cookie.
	if _, _, ok := a.SessionUser(httptest.NewRequest("GET", "/", nil)); ok {
		t.Fatal("cookieless request accepted")
	}
}

func TestVerifyCSRF(t *testing.T) {
	if !VerifyCSRF("tok", "tok") {
		t.Error("matching token rejected")
	}
	if VerifyCSRF("tok", "other") || VerifyCSRF("", "") {
		t.Error("bad token accepted")
	}
}
