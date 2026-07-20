package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// policy_ops.go: policy, assignment and filter editors plus per-scope app
// lists. Authorization mirrors the API: policies and filters are org-wide
// objects (org Owner); an assignment needs Owner at its target scope; app
// lists need Editor at their scope.

// parsePolicySettings reads "key = value" lines. Values go through the
// catalog's typed parser when the key is documented; otherwise JSON, then
// plain string. The gate remains the final validator.
func (s *Server) parsePolicySettings(text string) (map[string]any, error) {
	out := map[string]any{}
	cat := s.svc.Config.Catalog()
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", i+1)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if entry, known := cat.Lookup(key); known {
			typed, err := entry.ParseValue(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			out[key] = typed
			continue
		}
		var typed any
		if err := json.Unmarshal([]byte(val), &typed); err == nil {
			out[key] = typed
		} else {
			out[key] = val
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("policy needs at least one setting")
	}
	return out, nil
}

// postPolicyPut creates or replaces a policy.
func (s *Server) postPolicyPut(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	id := strings.TrimSpace(r.FormValue("id"))
	settings, err := s.parsePolicySettings(r.FormValue("settings"))
	if err != nil {
		return err
	}
	var enforced []string
	for _, k := range strings.Split(r.FormValue("enforced"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			enforced = append(enforced, k)
		}
	}
	p := fleet.Policy{Description: strings.TrimSpace(r.FormValue("description")),
		Settings: settings, Enforced: enforced}
	// A form edit must not strip what the form does not carry: the label and
	// the profile provenance survive hand edits, so the console keeps
	// comparing an edited policy against its source profile.
	if prev, ok := s.svc.Config.Fleet().Policies[id]; ok {
		p.Name, p.Profile = prev.Name, prev.Profile
	}
	if err := s.applyGated(r, v, fleet.PutPolicy(id, p),
		"policies: put "+id); err != nil {
		return err
	}
	http.Redirect(w, r, "/policies", http.StatusSeeOther)
	return nil
}

// postProfileApply instantiates an overlay profile as a regular policy plus
// class filter and org assignment (fleet.ApplyProfile). Re-applying is the
// drift-repair path: it refreshes the policy to the profile's current
// content and touches nothing else.
func (s *Server) postProfileApply(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	name := r.PathValue("name")
	p, ok := s.svc.Config.Profiles().Get(name)
	if !ok {
		return fmt.Errorf("unknown profile %q", name)
	}
	if err := s.applyGated(r, v, fleet.ApplyProfile(p),
		"policies: apply profile "+p.Provenance()); err != nil {
		return err
	}
	http.Redirect(w, r, "/policies", http.StatusSeeOther)
	return nil
}

// postPolicyDelete removes a policy (refused while assigned, by the domain).
func (s *Server) postPolicyDelete(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	id := r.PathValue("id")
	if err := s.applyGated(r, v, fleet.DeletePolicy(id),
		"policies: delete "+id); err != nil {
		return err
	}
	http.Redirect(w, r, "/policies", http.StatusSeeOther)
	return nil
}

// postAssignmentAdd binds a policy to a target scope.
func (s *Server) postAssignmentAdd(w http.ResponseWriter, r *http.Request, v view) error {
	in := fleet.Assignment{
		Policy: r.FormValue("policy"),
		Target: r.FormValue("target"),
		Filter: r.FormValue("filter"),
	}
	if p := r.FormValue("priority"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("priority expects a number")
		}
		in.Priority = n
	}
	if err := s.requireWeb(v, in.Target, identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("policies: assign %s to %s", in.Policy, in.Target)
	if err := s.applyGated(r, v, fleet.Assign(in), msg,
		app.AffectedHosts(s.svc.Config.Fleet(), in.Target)...); err != nil {
		return err
	}
	http.Redirect(w, r, "/policies", http.StatusSeeOther)
	return nil
}

// postAssignmentDelete unbinds a policy from a target.
func (s *Server) postAssignmentDelete(w http.ResponseWriter, r *http.Request, v view) error {
	policy, target, filter := r.FormValue("policy"), r.FormValue("target"), r.FormValue("filter")
	if err := s.requireWeb(v, target, identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("policies: unassign %s from %s", policy, target)
	if err := s.applyGated(r, v, fleet.Unassign(policy, target, filter), msg,
		app.AffectedHosts(s.svc.Config.Fleet(), target)...); err != nil {
		return err
	}
	http.Redirect(w, r, "/policies", http.StatusSeeOther)
	return nil
}

// postFilterPut creates or replaces a filter from up to three rule rows.
func (s *Server) postFilterPut(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	id := strings.TrimSpace(r.FormValue("id"))
	fl := fleet.Filter{Match: r.FormValue("match")}
	for i := 0; i < 3; i++ {
		attr := strings.TrimSpace(r.FormValue(fmt.Sprintf("attr%d", i)))
		op := r.FormValue(fmt.Sprintf("op%d", i))
		val := strings.TrimSpace(r.FormValue(fmt.Sprintf("value%d", i)))
		if attr == "" {
			continue
		}
		rule := fleet.FilterRule{Attr: attr, Op: op}
		if op == "in" {
			for _, part := range strings.Split(val, ",") {
				if part = strings.TrimSpace(part); part != "" {
					rule.Values = append(rule.Values, part)
				}
			}
		} else {
			rule.Value = val
		}
		fl.Rules = append(fl.Rules, rule)
	}
	if err := s.applyGated(r, v, fleet.PutFilter(id, fl),
		"filters: put "+id); err != nil {
		return err
	}
	http.Redirect(w, r, "/policies", http.StatusSeeOther)
	return nil
}

// postFilterDelete removes a filter (refused while referenced, by the domain).
func (s *Server) postFilterDelete(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	id := r.PathValue("id")
	if err := s.applyGated(r, v, fleet.DeleteFilter(id),
		"filters: delete "+id); err != nil {
		return err
	}
	http.Redirect(w, r, "/policies", http.StatusSeeOther)
	return nil
}

// postScopeApps replaces one app list at a scope from a comma-separated
// form value.
func (s *Server) postScopeApps(w http.ResponseWriter, r *http.Request, v view) error {
	scope := r.FormValue("scope")
	kind := fleet.AppKind(r.FormValue("kind"))
	if err := s.requireWeb(v, scope, identity.Editor); err != nil {
		return err
	}
	var names []string
	for _, n := range strings.Split(r.FormValue("names"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	msg := fmt.Sprintf("apps: set %s at %s (%d)", kind, scope, len(names))
	if err := s.applyGated(r, v, fleet.SetScopeApps(scope, kind, names),
		msg, app.AffectedHosts(s.svc.Config.Fleet(), scope)...); err != nil {
		return err
	}
	http.Redirect(w, r, "/settings?scope="+url.QueryEscape(scope), http.StatusSeeOther)
	return nil
}
