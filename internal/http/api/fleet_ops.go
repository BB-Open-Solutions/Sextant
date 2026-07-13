package api

import (
	"fmt"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// fleet_ops.go: the management operations beyond enroll/settings - device
// lifecycle and updates, group administration, app assignment, rollout plan
// and assurance configuration. Every write rides the safe transaction.

// patchDevice updates device fields. Moving a device into new groups needs
// editor rights there too - membership decides which policies apply.
func (a *API) patchDevice(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	var in struct {
		Hardware     *string            `json:"hardware,omitempty"`
		Class        *string            `json:"class,omitempty"`
		AssignedUser *string            `json:"assignedUser,omitempty"`
		Groups       *[]string          `json:"groups,omitempty"`
		Labels       *map[string]string `json:"labels,omitempty"`
		ITAM         *fleet.ITAM        `json:"itam,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, "device:"+tag, identity.Editor); err != nil {
		return err
	}
	if in.Groups != nil {
		// Require Editor on every group the device joins AND every group it
		// leaves: stripping a device out of a group changes which policies
		// apply to it, so a device-scoped editor must not do it to a group
		// they do not control.
		cur := a.cfg.Fleet().Devices[tag].Groups
		for _, g := range fleet.GroupMembershipDelta(cur, *in.Groups) {
			if err := a.require(r, "group:"+g, identity.Editor); err != nil {
				return err
			}
		}
	}
	p := fleet.DevicePatch{Hardware: in.Hardware, Class: in.Class,
		AssignedUser: in.AssignedUser, Groups: in.Groups, Labels: in.Labels, ITAM: in.ITAM}
	msg := "devices: update " + tag
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.UpdateDevice(tag, p)), msg, author(r), tag); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	return nil
}

// postDeviceRetire parks a device: audit record stays, builds/check-ins/
// rollout counting stop, and its credential is revoked.
func (a *API) postDeviceRetire(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	if err := a.require(r, "device:"+tag, identity.Editor); err != nil {
		return err
	}
	msg := "devices: retire " + tag
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.RetireDevice(tag)), msg, author(r)); err != nil {
		return err
	}
	if a.devCreds != nil {
		if err := a.devCreds.Revoke(r.Context(), tag); err != nil {
			a.log.Warn("device retired but credential revoke failed", "tag", tag, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "retired"})
	return nil
}

// postDeviceReactivate returns a retired device to service and issues a
// fresh credential (the old one was revoked at retirement).
func (a *API) postDeviceReactivate(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	if err := a.require(r, "device:"+tag, identity.Editor); err != nil {
		return err
	}
	msg := "devices: reactivate " + tag
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.ReactivateDevice(tag)), msg, author(r), tag); err != nil {
		return err
	}
	out := map[string]string{"status": "active"}
	if a.devCreds != nil {
		if secret, err := a.devCreds.Issue(r.Context(), tag); err != nil {
			a.log.Error("device reactivated but credential not issued", "tag", tag, "err", err)
			out["credentialError"] = "credential not issued; re-issue before the device checks in"
		} else {
			out["credential"] = secret
			out["notice"] = "store this device credential now; it is not shown again"
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// patchGroup re-parents a group and/or changes its IdP mapping. Moving a
// subtree changes what governs it: org owner only.
func (a *API) patchGroup(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	var in struct {
		Parent   *string `json:"parent,omitempty"`
		IdpGroup *string `json:"idpGroup,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Parent == nil && in.IdpGroup == nil {
		return &ports.ValidationError{Detail: "nothing to update (parent or idpGroup)"}
	}
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	msg := "groups: update " + name
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.UpdateGroup(name, in.Parent, in.IdpGroup)),
		msg, author(r), app.AffectedHosts(a.cfg.Fleet(), "group:"+name)...); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	return nil
}

// deleteGroup removes an empty leaf group.
func (a *API) deleteGroup(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	msg := "groups: remove " + name
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.RemoveGroup(name)), msg, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	return nil
}

// putApps replaces one additive app list (packages|flatpaks|overlays) at a
// scope. Names pass the injection firewall in the domain; the gate proves
// the result still evaluates before anything commits.
func (a *API) putApps(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Scope string   `json:"scope"`
		Kind  string   `json:"kind"`
		Names []string `json:"names"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, in.Scope, identity.Editor); err != nil {
		return err
	}
	msg := fmt.Sprintf("apps: set %s at %s (%d)", in.Kind, in.Scope, len(in.Names))
	if err := a.cfg.Apply(r.Context(),
		rejectingMut(fleet.SetScopeApps(in.Scope, fleet.AppKind(in.Kind), in.Names)),
		msg, author(r), app.AffectedHosts(a.cfg.Fleet(), in.Scope)...); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	return nil
}

// putRolloutPlan replaces the ring plan; null clears it.
func (a *API) putRolloutPlan(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Plan *fleet.RolloutPolicy `json:"plan"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	msg := "rollout: replace plan"
	if in.Plan == nil {
		msg = "rollout: clear plan"
	}
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.SetRolloutPlan(in.Plan)), msg, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	return nil
}

// putAssurance replaces the audit-control configuration.
func (a *API) putAssurance(w http.ResponseWriter, r *http.Request) error {
	var in fleet.Assurance
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("assurance: four-eyes %v", in.RequireFourEyes)
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.SetAssurance(in)), msg, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	return nil
}
