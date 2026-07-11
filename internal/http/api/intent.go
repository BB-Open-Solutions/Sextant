package api

import (
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// intent.go: arming and clearing remote-action intents (design 0004).
// Destructive reach - org Owner, not device editor. Every intent is an
// audited commit; the device pulls it on check-in and acts locally.

// postDeviceIntent arms lock or wipe on a device.
func (a *API) postDeviceIntent(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	var in struct {
		Intent string `json:"intent"`
		Force  bool   `json:"force,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	// Destructive reach across a whole device: org Owner.
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	if !a.canView(r)("device:" + tag) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown device " + tag})
		return nil
	}
	msg := "intent: " + in.Intent + " " + tag
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.SetDeviceIntent(tag, in.Intent, in.Force)),
		msg, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "armed", "intent": in.Intent})
	return nil
}

// deleteDeviceIntent cancels a pending action (a found laptop).
func (a *API) deleteDeviceIntent(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	msg := "intent: clear " + tag
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.ClearDeviceIntent(tag)), msg, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
	return nil
}
