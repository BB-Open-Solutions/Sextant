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

// postDeviceIntent arms lock, reboot or wipe from the console. Reboot is
// non-destructive (a provisioning convenience) so it is Editor reach; lock and
// wipe stay org-owner.
func (s *Server) postDeviceIntent(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	intent := r.FormValue("intent")
	force := r.FormValue("force") != ""
	role := identity.Owner
	if intent == fleet.IntentReboot {
		role = identity.Editor
	}
	if err := s.requireWeb(v, "org", role); err != nil {
		return err
	}
	// Wipe is irreversible: require the operator to type the tag exactly.
	if intent == fleet.IntentWipe && r.FormValue("confirm") != tag {
		return fmt.Errorf("type the device tag exactly to confirm a wipe")
	}
	msg := fmt.Sprintf("intent: %s %s", intent, tag)
	if err := s.svc.Config.Apply(r.Context(), fleet.SetDeviceIntent(tag, intent, force),
		msg, webAuthor(v)); err != nil {
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
	if err := s.svc.Config.Apply(r.Context(), fleet.ClearDeviceIntent(tag),
		"intent: clear "+tag, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}
