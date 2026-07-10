package api

import (
	"net/http"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
)

// token management (ADR 0008): a user manages their OWN tokens; only an
// org owner mints service accounts or revokes another principal's tokens.

func (a *API) getTokens(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	toks, err := a.tokens.List(r.Context(), p.user.Subject)
	if err != nil {
		return err
	}
	// Never expose hashes; the store's Token JSON already omits Hash.
	writeJSON(w, http.StatusOK, toks)
	return nil
}

func (a *API) postToken(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Kind    string   `json:"kind,omitempty"`    // personal (default) | service
		Ceiling string   `json:"ceiling,omitempty"` // viewer|editor|owner
		Groups  []string `json:"groups,omitempty"`  // service accounts only
		TTLDays int      `json:"ttlDays,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	p := principalFrom(r.Context())
	// No token chaining: a scoped token cannot mint further tokens, or its
	// expiry would be extendable forever. Sessions (humans) and the
	// break-glass service principal may mint.
	if p.bearer && !p.user.Service {
		return &forbidden{errNoTokenChaining}
	}
	kind := token.Personal
	if in.Kind == string(token.Service) {
		kind = token.Service
	}

	req := app.MintRequest{ID: in.ID, Name: in.Name, Kind: kind, Ceiling: in.Ceiling}
	if in.TTLDays > 0 {
		req.TTL = time.Duration(in.TTLDays) * 24 * time.Hour
	}
	switch kind {
	case token.Personal:
		// A personal token acts as its creator: snapshot the caller's
		// own subject and groups. No elevation possible.
		req.Subject = p.user.Subject
		req.Groups = p.user.Groups
	case token.Service:
		// Service accounts are principals of their own; only an org owner
		// may create them, and their bindings live in the access list.
		if err := a.require(r, "org", identity.Owner); err != nil {
			return err
		}
		if in.ID == "" {
			return reject(errServiceNeedsID)
		}
		req.Subject = "svc:" + in.ID
		req.Groups = in.Groups
	}

	tok, secret, err := a.tokens.Mint(r.Context(), req)
	if err != nil {
		return reject(err)
	}
	// The secret is shown exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":  tok,
		"secret": secret,
		"notice": "store this secret now; it is not shown again",
	})
	return nil
}

func (a *API) deleteToken(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	p := principalFrom(r.Context())
	tok, ok, err := a.tokenOwned(r, id)
	if err != nil {
		return err
	}
	// Owner of the token may revoke it; otherwise org owner is required.
	if !ok || tok.Subject != p.user.Subject {
		if err := a.require(r, "org", identity.Owner); err != nil {
			return err
		}
	}
	if err := a.tokens.Revoke(r.Context(), id); err != nil {
		return reject(err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
	return nil
}

// tokenOwned reports whether the caller owns token id. A store error or a
// miss both fall through to the org-owner requirement (fail closed).
func (a *API) tokenOwned(r *http.Request, id string) (token.Token, bool, error) {
	p := principalFrom(r.Context())
	toks, err := a.tokens.List(r.Context(), p.user.Subject)
	if err != nil {
		return token.Token{}, false, err
	}
	for _, t := range toks {
		if t.ID == id {
			return t, true, nil
		}
	}
	return token.Token{}, false, nil
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

const (
	errServiceNeedsID  = simpleErr("service account needs an id")
	errNoTokenChaining = simpleErr("a token cannot mint tokens; use a browser session")
)
