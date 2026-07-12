package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// change_ops.go: editing inside a change request and the rollout ring
// plan. With these, the review flow is complete in the console: open a
// change, stage edits on its branch, submit, diff, merge under four-eyes.

// postChangeEdit stages one setting edit on the change's branch. Values
// parse through the catalog when the key is documented, mirroring the
// settings editor.
func (s *Server) postChangeEdit(w http.ResponseWriter, r *http.Request, v view) error {
	id := r.PathValue("id")
	scope := r.FormValue("scope")
	key := strings.TrimSpace(r.FormValue("key"))
	if err := s.requireWeb(v, scope, identity.Editor); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("setting key required")
	}

	var mut fleet.Mutation
	var msg string
	if r.FormValue("clear") == "1" {
		mut = fleet.ClearScopeSetting(scope, key)
		msg = fmt.Sprintf("settings: clear %s at %s", key, scope)
	} else {
		raw := strings.TrimSpace(r.FormValue("value"))
		if raw == "" {
			return fmt.Errorf("no value given for %s", key)
		}
		var val any
		if entry, known := s.svc.Config.Catalog().Lookup(key); known {
			typed, err := entry.ParseValue(raw)
			if err != nil {
				return err
			}
			val = typed
		} else {
			val = parseValue(raw)
		}
		mut = fleet.SetScopeSetting(scope, key, val)
		msg = fmt.Sprintf("settings: set %s at %s", key, scope)
	}
	if err := s.svc.Changes.Edit(r.Context(), id, mut, msg, webAuthor(v),
		app.AffectedHosts(s.svc.Config.Fleet(), scope)...); err != nil {
		return err
	}
	http.Redirect(w, r, "/changes", http.StatusSeeOther)
	return nil
}

// postRolloutPlan replaces the ring plan from up to five ring rows; an
// empty form clears the plan.
func (s *Server) postRolloutPlan(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	// Rows are dynamic (the page renders plan size + blanks); read every
	// submitted groupN key rather than a fixed count, so no ring silently
	// drops. Bounded by the form-size cap upstream.
	if err := r.ParseForm(); err != nil {
		return err
	}
	var plan *fleet.RolloutPolicy
	for i := 0; ; i++ {
		key := fmt.Sprintf("group%d", i)
		if _, present := r.Form[key]; !present {
			break
		}
		group := strings.TrimSpace(r.FormValue(key))
		if group == "" {
			continue
		}
		ring := fleet.RolloutRing{
			Group:           group,
			Name:            strings.TrimSpace(r.FormValue(fmt.Sprintf("name%d", i))),
			RequireApproval: r.FormValue(fmt.Sprintf("approval%d", i)) != "",
		}
		if soak := r.FormValue(fmt.Sprintf("soak%d", i)); soak != "" {
			n, err := strconv.Atoi(soak)
			if err != nil {
				return fmt.Errorf("ring %d: soak expects minutes", i+1)
			}
			ring.SoakMinutes = n
		}
		if h := r.FormValue(fmt.Sprintf("healthy%d", i)); h != "" {
			n, err := strconv.Atoi(h)
			if err != nil {
				return fmt.Errorf("ring %d: minHealthyPercent expects a number", i+1)
			}
			ring.MinHealthyPercent = n
		}
		if plan == nil {
			plan = &fleet.RolloutPolicy{}
		}
		plan.Rings = append(plan.Rings, ring)
	}
	msg := "rollout: replace plan"
	if plan == nil {
		msg = "rollout: clear plan"
	}
	if err := s.svc.Config.Apply(r.Context(), fleet.SetRolloutPlan(plan), msg, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/rollout", http.StatusSeeOther)
	return nil
}
