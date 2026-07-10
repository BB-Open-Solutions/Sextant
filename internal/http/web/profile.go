package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
)

// profile.go: personal settings behind the profile menu - presentation
// preferences and the user's own API tokens. Everything here is
// self-service: it never touches another principal or the fleet document.

// profilePage renders identity, roles, preferences and own tokens.
// ?secret= flash is intentionally NOT used; a freshly minted secret is
// carried through an in-memory one-shot per session CSRF (see mint).
func (s *Server) profilePage(w http.ResponseWriter, r *http.Request, v view) {
	data := map[string]any{"Title": "Profile", "Nav": "profile"}

	// Effective roles per visible scope, live-derived like /api/v1/me.
	roles := map[string]string{}
	if role := v.roleAt("org"); role >= identity.Viewer {
		roles["org"] = role.String()
	}
	f := s.svc.Config.Fleet()
	for g := range f.Groups {
		if role := v.roleAt("group:" + g); role >= identity.Viewer {
			roles["group:"+g] = role.String()
		}
	}
	data["Roles"] = roles

	if s.svc.Prefs != nil {
		prefs, _, err := s.svc.Prefs.GetPrefs(r.Context(), app.DefaultTenant, v.User.Subject)
		if err != nil {
			data["Error"] = err.Error()
		}
		data["Prefs"], data["HasPrefs"] = prefs, true
		data["Locales"] = identity.SupportedLocales
	}

	if s.svc.Tokens != nil {
		toks, err := s.svc.Tokens.List(r.Context(), v.User.Subject)
		if err != nil {
			data["Error"] = err.Error()
		}
		mine := make([]token.Token, 0, len(toks))
		for _, t := range toks {
			if t.Kind == token.Personal {
				mine = append(mine, t)
			}
		}
		data["Tokens"], data["HasTokens"] = mine, true
	}

	// One-shot minted secret (set by the mint handler via query-less
	// redirect + short-lived cookie).
	if c, err := r.Cookie("sextant_minted"); err == nil && c.Value != "" {
		data["MintedSecret"] = c.Value
		http.SetCookie(w, &http.Cookie{Name: "sextant_minted", Value: "",
			Path: "/profile", MaxAge: -1, HttpOnly: true, Secure: true})
	}
	s.render(w, "profile", data, v)
}

// postProfilePrefs saves timezone/locale after domain validation.
func (s *Server) postProfilePrefs(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Prefs == nil {
		return fmt.Errorf("preferences need the database (postgres not configured)")
	}
	p := identity.Preferences{
		Timezone: strings.TrimSpace(r.FormValue("timezone")),
		Locale:   r.FormValue("locale"),
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if err := s.svc.Prefs.PutPrefs(r.Context(), app.DefaultTenant, v.User.Subject, p, time.Now()); err != nil {
		return err
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
	return nil
}

// postProfileTokenMint mints a personal token for the session user. The
// secret is shown exactly once, carried over the redirect in a short-lived
// HttpOnly cookie scoped to /profile (never in a URL, never logged).
func (s *Server) postProfileTokenMint(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Tokens == nil {
		return fmt.Errorf("tokens need the database (postgres not configured)")
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return fmt.Errorf("token name required")
	}
	// Random id: the console never asks users to invent slugs.
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	req := app.MintRequest{
		ID:   "pt-" + hex.EncodeToString(buf),
		Name: name, Kind: token.Personal,
		Subject: v.User.Subject, Groups: v.User.Groups,
		Ceiling: r.FormValue("ceiling"),
	}
	if d := r.FormValue("ttlDays"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return fmt.Errorf("ttlDays expects a positive number of days")
		}
		req.TTL = time.Duration(n) * 24 * time.Hour
	}
	_, secret, err := s.svc.Tokens.Mint(r.Context(), req)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "sextant_minted", Value: secret,
		Path: "/profile", MaxAge: 60, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
	return nil
}

// postProfileTokenRevoke revokes one of the user's OWN tokens; anything
// else 404s indistinguishably from a missing id.
func (s *Server) postProfileTokenRevoke(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Tokens == nil {
		return fmt.Errorf("tokens need the database (postgres not configured)")
	}
	id := r.PathValue("id")
	toks, err := s.svc.Tokens.List(r.Context(), v.User.Subject)
	if err != nil {
		return err
	}
	owned := false
	for _, t := range toks {
		if t.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		return fmt.Errorf("unknown token %q", id)
	}
	if err := s.svc.Tokens.Revoke(r.Context(), id); err != nil {
		return err
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
	return nil
}
