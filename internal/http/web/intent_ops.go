package web

import (
	"fmt"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// intent_ops.go: the red-zone remote-action panel (design 0004). Arming a
// lock or wipe is org-owner reach; wipe demands a typed confirmation (the
// device tag) so a mis-click cannot destroy a machine.

// postDeviceIntent arms lock, reboot, wipe or diagnostics from the console.
// Reboot and diagnostics are non-destructive so they are Editor reach; lock
// and wipe stay org-owner.
func (s *Server) postDeviceIntent(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	intent := r.FormValue("intent")
	force := r.FormValue("force") != ""
	role := identity.Owner
	if intent == fleet.IntentReboot || intent == fleet.IntentDiagnostics {
		role = identity.Editor
	}
	if err := s.requireWeb(v, "org", role); err != nil {
		return err
	}
	// Deployment kill switch (design 0010): a tenant that forbids log
	// collection cannot even request it.
	if intent == fleet.IntentDiagnostics && s.svc.Diagnostics == nil {
		return fmt.Errorf("diagnostics collection is disabled in this deployment")
	}
	// Wipe is irreversible: require the operator to type the tag exactly.
	if intent == fleet.IntentWipe && r.FormValue("confirm") != tag {
		return fmt.Errorf("type the device tag exactly to confirm a wipe")
	}
	// Grace-window write, scoped to this one host: arming an intent must feel
	// instant (the wizard's reboot button), not wait out an org-wide eval.
	msg := fmt.Sprintf("intent: %s %s", intent, tag)
	if err := s.applyGated(r, v, fleet.SetDeviceIntent(tag, intent, force), msg, tag); err != nil {
		return err
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}

// postDeviceIntentClear cancels a pending action.
func (s *Server) postDeviceIntentClear(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if err := s.applyGated(r, v, fleet.ClearDeviceIntent(tag), "intent: clear "+tag, tag); err != nil {
		return err
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}
