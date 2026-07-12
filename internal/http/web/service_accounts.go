package web

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
)

// service_accounts.go: the owner-only admin surface for non-human principals
// (ADR 0008). A service account is a token of kind=service with its OWN
// subject (svc:<id>) and a snapshot of IdP groups; its rights come from the
// access bindings those groups match - never from a human's session. This
// page lists them, mints new ones (secret shown once), and revokes any.

const svcAcctCookie = "sextant_svc_minted"

// svcAcctRow is one service account plus its resolved org role, so an owner
// sees at a glance what rights the principal actually carries.
type svcAcctRow struct {
	token.Token
	OrgRole string
}

// serviceAccountsPage lists every service account with a mint form. Org
// Owner only: these principals can hold org-wide rights, so viewing and
// managing them is the highest-trust surface in the console.
func (s *Server) serviceAccountsPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	data := map[string]any{"Title": "Service accounts", "Nav": "service-accounts"}

	if s.svc.Tokens == nil {
		data["NoStore"] = true
		s.render(w, "service_accounts", data, v)
		return
	}

	toks, err := s.svc.Tokens.ListServiceAccounts(r.Context())
	if err != nil {
		data["Error"] = err.Error()
	}
	sort.Slice(toks, func(i, j int) bool { return toks[i].ID < toks[j].ID })
	rows := make([]svcAcctRow, 0, len(toks))
	for _, t := range toks {
		rows = append(rows, svcAcctRow{Token: t, OrgRole: v.rv.RoleAt(t.User(), "org").String()})
	}
	data["Accounts"] = rows

	// Bindable groups: the distinct IdP groups that actually grant a role in
	// the access list. Picking from these (a choice, not free text) keeps an
	// owner from typing a group that binds to nothing.
	f := s.svc.Config.Fleet()
	seen := map[string]bool{}
	var bindable []string
	for _, b := range f.Access {
		if b.Group != "" && !seen[b.Group] {
			seen[b.Group] = true
			bindable = append(bindable, b.Group)
		}
	}
	sort.Strings(bindable)
	data["BindableGroups"] = bindable

	// One-shot minted secret, carried over the redirect in a short-lived
	// HttpOnly cookie scoped to this page (never in a URL, never logged).
	if c, err := r.Cookie(svcAcctCookie); err == nil && c.Value != "" {
		data["MintedSecret"] = c.Value
		http.SetCookie(w, &http.Cookie{Name: svcAcctCookie, Value: "",
			Path: "/service-accounts", MaxAge: -1, HttpOnly: true, Secure: true})
	}
	s.render(w, "service_accounts", data, v)
}

// postServiceAccountMint creates a service account (kind=service). Org Owner
// only. The subject is svc:<id>; the groups snapshot decides its rights via
// the access list. The secret is shown exactly once.
func (s *Server) postServiceAccountMint(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if s.svc.Tokens == nil {
		return fmt.Errorf("service accounts need the database (postgres not configured)")
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		return fmt.Errorf("service account id required")
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = id
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	// Only groups the owner was offered (bound in the access list) are
	// accepted; a hand-crafted POST cannot bind to an unbound group.
	f := s.svc.Config.Fleet()
	bound := map[string]bool{}
	for _, b := range f.Access {
		bound[b.Group] = true
	}
	var groups []string
	for _, g := range r.Form["groups"] {
		if bound[g] {
			groups = append(groups, g)
		}
	}
	req := app.MintRequest{
		ID: id, Name: name, Kind: token.Service,
		Subject: "svc:" + id, Groups: groups,
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
	http.SetCookie(w, &http.Cookie{Name: svcAcctCookie, Value: secret,
		Path: "/service-accounts", MaxAge: 60, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/service-accounts", http.StatusSeeOther)
	return nil
}

// postServiceAccountRevoke revokes a service account by id. Org Owner only.
// It verifies the target really is a service account before deleting, so
// this surface can never revoke a personal or device credential by id.
func (s *Server) postServiceAccountRevoke(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if s.svc.Tokens == nil {
		return fmt.Errorf("service accounts need the database (postgres not configured)")
	}
	id := r.PathValue("id")
	toks, err := s.svc.Tokens.ListServiceAccounts(r.Context())
	if err != nil {
		return err
	}
	isService := false
	for _, t := range toks {
		if t.ID == id {
			isService = true
			break
		}
	}
	if !isService {
		return fmt.Errorf("unknown service account %q", id)
	}
	if err := s.svc.Tokens.Revoke(r.Context(), id); err != nil {
		return err
	}
	http.Redirect(w, r, "/service-accounts", http.StatusSeeOther)
	return nil
}
