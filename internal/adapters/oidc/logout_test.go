package oidc

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Logout was at 0%, and it is mounted directly rather than through the web
// package's action wrapper, so it carries its own CSRF check. Two things can
// go wrong and they point in opposite directions:
//
// Too permissive, and a cross-site auto-submitting form logs a visitor out
// without their consent. Annoying rather than catastrophic, but it is a state
// change caused by a third party and there is no reason to allow it.
//
// Too weak in the other direction, and logout returns its redirect while the
// session cookie still works. The user sees "signed out" on a shared machine
// and the next person has their session. That failure is completely silent
// from the browser: the redirect looks identical either way.
//
// So every case below asserts what happened to the SESSION, not what status
// came back.

// establishedSession runs the real login and callback flow and returns the
// session cookie plus its CSRF token.
func establishedSession(t *testing.T) (*Authenticator, *http.Cookie, string) {
	t.Helper()
	idp := newFakeIDP(t)
	a := newTestAuthenticator(t, idp)

	rec, state, nonce := login(t, a)
	idp.setIDTokenClaims(map[string]any{
		"nonce": nonce, "sub": "user-1",
		"email": "ada@example.com", "name": "Ada Lovelace",
	})
	cbReq := httptest.NewRequest(http.MethodGet, "/callback?state="+state+"&code=test-code", nil)
	for _, c := range rec.Result().Cookies() {
		cbReq.AddCookie(c)
	}
	cbRec := httptest.NewRecorder()
	a.Callback(cbRec, cbReq)

	sc := sessionCookie(cbRec.Result().Cookies())
	if sc == nil || sc.Value == "" {
		t.Fatalf("no session established: %d %s", cbRec.Code, cbRec.Body.String())
	}
	probe := httptest.NewRequest(http.MethodGet, "/", nil)
	probe.AddCookie(sc)
	_, csrf, ok := a.SessionUser(probe)
	if !ok || csrf == "" {
		t.Fatal("fixture: the session did not decode")
	}
	return a, sc, csrf
}

// stillValid reports whether the session cookie still authenticates, after
// applying whatever Set-Cookie the response carried. That second part is the
// whole test: clearing works by SENDING a replacement cookie, so a check that
// only re-presents the original would call a broken logout a success.
func stillValid(t *testing.T, a *Authenticator, original *http.Cookie, resp *http.Response) bool {
	t.Helper()
	cookie := original
	if replaced := sessionCookie(resp.Cookies()); replaced != nil {
		cookie = replaced
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	_, _, ok := a.SessionUser(r)
	return ok
}

func TestLogoutRefusesWithoutTheSessionsOwnCSRFToken(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*http.Request, string)
	}{
		{"no token at all", func(*http.Request, string) {}},
		{"an empty token", func(r *http.Request, _ string) { r.Header.Set("X-CSRF-Token", "") }},
		{"somebody else's token", func(r *http.Request, _ string) {
			r.Header.Set("X-CSRF-Token", "not-the-one-in-this-session")
		}},
		{"a prefix of the right token", func(r *http.Request, csrf string) {
			r.Header.Set("X-CSRF-Token", csrf[:len(csrf)-1])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, sc, csrf := establishedSession(t)

			req := httptest.NewRequest(http.MethodPost, "/logout", nil)
			req.AddCookie(sc)
			tc.set(req, csrf)
			rec := httptest.NewRecorder()
			a.Logout(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
			// The status matters less than this: a refused logout must leave
			// the session alone, or the CSRF check is decoration.
			if !stillValid(t, a, sc, rec.Result()) {
				t.Error("the session was cleared despite the request being refused")
			}
		})
	}
}

func TestLogoutWithTheRightTokenActuallyEndsTheSession(t *testing.T) {
	// Both places the handler looks, because a form post and a fetch send it
	// differently and only one of them is exercised by clicking the button.
	for _, via := range []string{"form", "header"} {
		t.Run(via, func(t *testing.T) {
			a, sc, csrf := establishedSession(t)

			req := httptest.NewRequest(http.MethodPost, "/logout", nil)
			req.AddCookie(sc)
			if via == "form" {
				req = httptest.NewRequest(http.MethodPost, "/logout?csrf="+csrf, nil)
				req.AddCookie(sc)
			} else {
				req.Header.Set("X-CSRF-Token", csrf)
			}
			rec := httptest.NewRecorder()
			a.Logout(rec, req)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
			}
			if loc := rec.Header().Get("Location"); loc != "/login" {
				t.Errorf("Location = %q, want /login", loc)
			}
			if stillValid(t, a, sc, rec.Result()) {
				t.Error("logout redirected but the session still authenticates; " +
					"on a shared machine the next person has it")
			}
		})
	}
}

// A visitor with no session, or one that has expired, is not being attacked:
// there is nothing to protect. Refusing here would strand somebody on a page
// whose only escape is the button that does not work.
func TestLogoutWithoutASessionSucceedsQuietly(t *testing.T) {
	idp := newFakeIDP(t)
	a := newTestAuthenticator(t, idp)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	a.Logout(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d: a visitor with no session has nothing to "+
			"protect and should just be sent to the login page", rec.Code, http.StatusSeeOther)
	}
}
