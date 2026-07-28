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

// nosec G101 - this is a cookie NAME, not a credential value.
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

// redirectToDevice sends the operator back to a device's page after an
// action. The target is always the fixed "/devices/" prefix plus the tag, so
// it is a same-site relative path and never an open redirect.
func redirectToDevice(w http.ResponseWriter, r *http.Request, tag string) {
	// #nosec G710 - constant "/devices/" prefix keeps this same-host and relative; tag cannot introduce a scheme or host.
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
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
	// Retiring is subtract-only (approved by Bram, 2026-07-16): the host
	// leaves the build set and no remaining host's configuration changes, so
	// there is nothing for the nix gate to prove - the structural checks
	// (decode, mutate, encode) still run. Reactivate DOES re-add a host and
	// stays gated.
	if err := s.svc.Config.ApplyStructural(r.Context(), fleet.RetireDevice(tag),
		"devices: retire "+tag, webAuthor(v)); err != nil {
		return err
	}
	if s.svc.DevCreds != nil {
		if err := s.svc.DevCreds.Revoke(r.Context(), tag); err != nil {
			s.log.Warn("device retired but credential revoke failed", "tag", tag, "err", err)
		}
	}
	// A retired device's diagnostics bundle is support material for a live
	// machine - delete it with the retirement (design 0010).
	if err := s.svc.Diagnostics.Delete(r.Context(), tag); err != nil {
		s.log.Warn("device retired but diagnostics delete failed", "tag", tag, "err", err)
	}
	redirectToDevice(w, r, tag)
	return nil
}

// postDeviceReactivate returns a device to service with a fresh credential.
func (s *Server) postDeviceReactivate(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	if err := s.requireDeviceEditor(v, tag); err != nil {
		return err
	}
	if err := s.applyGated(r, v, fleet.ReactivateDevice(tag),
		"devices: reactivate "+tag, tag); err != nil {
		return err
	}
	if s.svc.DevCreds != nil {
		if secret, err := s.svc.DevCreds.Issue(r.Context(), tag); err != nil {
			s.log.Error("device reactivated but credential not issued", "tag", tag, "err", err)
		} else {
			setDevCredCookie(w, tag, secret)
		}
	}
	redirectToDevice(w, r, tag)
	return nil
}

// postDeviceRemove unenrolls a device entirely.
func (s *Server) postDeviceRemove(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	if err := s.requireDeviceEditor(v, tag); err != nil {
		return err
	}
	// Subtract-only, like retire: see postDeviceRetire.
	if err := s.svc.Config.ApplyStructural(r.Context(), fleet.RemoveDevice(tag),
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

// postDevicesGroupCreate makes a group from a multi-selection on the device
// list and moves the selected devices into it in one commit. Creating the group
// needs Owner on the target scope (as postGroupAdd does); moving each selected
// device needs editor on it, so a selection cannot pull a device out of a scope
// the operator does not control.
func (s *Server) postDevicesGroupCreate(w http.ResponseWriter, r *http.Request, v view) error {
	name := strings.TrimSpace(r.FormValue("name"))
	parent := strings.TrimSpace(r.FormValue("parent"))
	tags := r.Form["tags"]
	if name == "" {
		return fmt.Errorf("group name required")
	}
	if len(tags) == 0 {
		return fmt.Errorf("select at least one device")
	}
	scope := "org"
	if parent != "" {
		scope = "group:" + parent
	}
	if err := s.requireWeb(v, scope, identity.Owner); err != nil {
		return err
	}
	for _, tag := range tags {
		if err := s.requireDeviceEditor(v, tag); err != nil {
			return err
		}
	}
	g := fleet.Group{Parent: parent}
	msg := fmt.Sprintf("groups: create %s from %d device(s)", name, len(tags))
	if err := s.applyGated(r, v, fleet.CreateGroupWithDevices(name, g, tags),
		msg, tags...); err != nil {
		return err
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
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
	redirectToDevice(w, r, tag)
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
	if r.FormValue("setclass") == "1" {
		val := strings.TrimSpace(r.FormValue("class"))
		// The class comes from a controlled dropdown; validate it here so a
		// tampered or legacy value gets a neat error instead of persisting.
		if !fleet.ValidClass(val) {
			return fmt.Errorf("unknown device class %q (choose one of %s)", val, strings.Join(fleet.Classes, ", "))
		}
		p.Class = &val
	}
	if val := strings.TrimSpace(r.FormValue("assignedUser")); r.FormValue("setuser") == "1" {
		p.AssignedUser = &val
	}
	if r.FormValue("setgroups") == "1" {
		// The group picker is a single-select now: one chosen group (or none)
		// becomes the device's whole membership.
		var groups []string
		if g := strings.TrimSpace(r.FormValue("groups")); g != "" {
			groups = []string{g}
		}
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
	if err := s.applyGated(r, v, fleet.UpdateDevice(tag, p),
		"devices: update "+tag, tag); err != nil {
		return err
	}
	redirectToDevice(w, r, tag)
	return nil
}
