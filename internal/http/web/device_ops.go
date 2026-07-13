package web

import (
	"fmt"
	"net/http"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// device_ops.go: device lifecycle and updates from the console. Fresh
// device credentials travel like minted tokens: a one-shot HttpOnly
// cookie over the redirect, rendered exactly once, never in a URL.

const devCredCookie = "sextant_devcred"

// setDevCredCookie stages a one-shot credential for the device page.
// Secure is deliberately hardcoded: on a plain-HTTP host the browser drops
// the cookie and the secret is simply never shown (fail-closed), rather
// than travelling in clear text. localhost counts as a secure context.
func setDevCredCookie(w http.ResponseWriter, tag, secret string) {
	http.SetCookie(w, &http.Cookie{Name: devCredCookie, Value: secret,
		Path: "/devices/" + tag, MaxAge: 60, HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode})
}

// requireDeviceEditor guards a lifecycle action on one device.
func (s *Server) requireDeviceEditor(v view, tag string) error {
	return s.requireWeb(v, "device:"+tag, identity.Editor)
}

// postDeviceRetire parks a device and revokes its credential.
func (s *Server) postDeviceRetire(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	if err := s.requireDeviceEditor(v, tag); err != nil {
		return err
	}
	if err := s.svc.Config.Apply(r.Context(), fleet.RetireDevice(tag),
		"devices: retire "+tag, webAuthor(v)); err != nil {
		return err
	}
	if s.svc.DevCreds != nil {
		if err := s.svc.DevCreds.Revoke(r.Context(), tag); err != nil {
			s.log.Warn("device retired but credential revoke failed", "tag", tag, "err", err)
		}
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}

// postDeviceReactivate returns a device to service with a fresh credential.
func (s *Server) postDeviceReactivate(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	if err := s.requireDeviceEditor(v, tag); err != nil {
		return err
	}
	if err := s.svc.Config.Apply(r.Context(), fleet.ReactivateDevice(tag),
		"devices: reactivate "+tag, webAuthor(v), tag); err != nil {
		return err
	}
	if s.svc.DevCreds != nil {
		if secret, err := s.svc.DevCreds.Issue(r.Context(), tag); err != nil {
			s.log.Error("device reactivated but credential not issued", "tag", tag, "err", err)
		} else {
			setDevCredCookie(w, tag, secret)
		}
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}

// postDeviceRemove unenrolls a device entirely.
func (s *Server) postDeviceRemove(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	if err := s.requireDeviceEditor(v, tag); err != nil {
		return err
	}
	if err := s.svc.Config.Apply(r.Context(), fleet.RemoveDevice(tag),
		"devices: remove "+tag, webAuthor(v)); err != nil {
		return err
	}
	if s.svc.DevCreds != nil {
		if err := s.svc.DevCreds.Revoke(r.Context(), tag); err != nil {
			s.log.Warn("device removed but credential revoke failed", "tag", tag, "err", err)
		}
	}
	http.Redirect(w, r, "/devices", http.StatusSeeOther)
	return nil
}

// postDeviceCredential re-issues the device credential (lost secret,
// re-image); refused for retired devices by policy.
func (s *Server) postDeviceCredential(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	if err := s.requireDeviceEditor(v, tag); err != nil {
		return err
	}
	if s.svc.DevCreds == nil {
		return fmt.Errorf("device credentials need the database (postgres not configured)")
	}
	d, ok := s.svc.Config.Fleet().Devices[tag]
	if !ok {
		return fmt.Errorf("unknown device %q", tag)
	}
	if d.Retired() {
		return fmt.Errorf("device is retired; reactivate it first")
	}
	secret, err := s.svc.DevCreds.Issue(r.Context(), tag)
	if err != nil {
		return err
	}
	setDevCredCookie(w, tag, secret)
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}

// postDeviceUpdate patches device fields from the console form. Moving the
// device into new groups needs editor rights there too.
func (s *Server) postDeviceUpdate(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	if err := s.requireDeviceEditor(v, tag); err != nil {
		return err
	}
	var p fleet.DevicePatch
	if val := strings.TrimSpace(r.FormValue("class")); r.FormValue("setclass") == "1" {
		p.Class = &val
	}
	if val := strings.TrimSpace(r.FormValue("assignedUser")); r.FormValue("setuser") == "1" {
		p.AssignedUser = &val
	}
	if r.FormValue("setgroups") == "1" {
		groups := r.Form["groups"]
		// Editor on groups joined AND left: a device move must not silently
		// pull the device out of a group the editor does not control (that
		// would evade that group's policy enforcement).
		cur := s.svc.Config.Fleet().Devices[tag].Groups
		for _, g := range fleet.GroupMembershipDelta(cur, groups) {
			if err := s.requireWeb(v, "group:"+g, identity.Editor); err != nil {
				return err
			}
		}
		p.Groups = &groups
	}
	if p.Class == nil && p.AssignedUser == nil && p.Groups == nil {
		return fmt.Errorf("nothing to update")
	}
	if err := s.svc.Config.Apply(r.Context(), fleet.UpdateDevice(tag, p),
		"devices: update "+tag, webAuthor(v), tag); err != nil {
		return err
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}
