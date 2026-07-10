package api

import (
	"fmt"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// --- reads ---

func (a *API) getFleet(w http.ResponseWriter, _ *http.Request) error {
	writeJSON(w, http.StatusOK, a.cfg.Fleet())
	return nil
}

type deviceSummary struct {
	Tag          string   `json:"tag"`
	Groups       []string `json:"groups,omitempty"`
	Class        string   `json:"class,omitempty"`
	Hardware     string   `json:"hardware"`
	AssignedUser string   `json:"assignedUser,omitempty"`
}

func (a *API) getDevices(w http.ResponseWriter, _ *http.Request) error {
	f := a.cfg.Fleet()
	out := make([]deviceSummary, 0, len(f.Devices))
	for _, tag := range f.DeviceTags() {
		d := f.Devices[tag]
		out = append(out, deviceSummary{
			Tag: tag, Groups: d.Groups, Class: d.Class,
			Hardware: d.Hardware, AssignedUser: d.AssignedUser,
		})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (a *API) getDevice(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	f := a.cfg.Fleet()
	d, ok := f.Devices[tag]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown device " + tag})
		return nil
	}
	pkgs, flats, ovs := f.ResolveApps(tag)
	writeJSON(w, http.StatusOK, map[string]any{
		"tag":      tag,
		"device":   d,
		"resolved": f.ResolveSorted(tag),
		"apps":     map[string][]string{"packages": pkgs, "flatpaks": flats, "overlays": ovs},
	})
	return nil
}

// --- writes: every mutation rides the safe transaction ---

func (a *API) postSetting(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Scope   string `json:"scope"`
		Key     string `json:"key"`
		Value   any    `json:"value"`
		Enforce *bool  `json:"enforce,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, in.Scope, identity.Editor); err != nil {
		return err
	}
	mut := func(f *fleet.Fleet) error {
		if err := fleet.SetScopeSetting(in.Scope, in.Key, in.Value)(f); err != nil {
			return reject(err)
		}
		if in.Enforce != nil {
			if err := fleet.SetScopeEnforce(in.Scope, in.Key, *in.Enforce)(f); err != nil {
				return reject(err)
			}
		}
		return nil
	}
	msg := fmt.Sprintf("settings: set %s at %s", in.Key, in.Scope)
	if err := a.cfg.Apply(r.Context(), mut, msg, author(r),
		app.AffectedHosts(a.cfg.Fleet(), in.Scope)...); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

func (a *API) deleteSetting(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Scope string `json:"scope"`
		Key   string `json:"key"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, in.Scope, identity.Editor); err != nil {
		return err
	}
	msg := fmt.Sprintf("settings: clear %s at %s", in.Key, in.Scope)
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.ClearScopeSetting(in.Scope, in.Key)), msg, author(r),
		app.AffectedHosts(a.cfg.Fleet(), in.Scope)...); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

func (a *API) putPolicy(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	var p fleet.Policy
	if err := decode(r, &p); err != nil {
		return err
	}
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.PutPolicy(id, p)),
		"policies: put "+id, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

func (a *API) deletePolicy(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.DeletePolicy(id)),
		"policies: delete "+id, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

func (a *API) postAssignment(w http.ResponseWriter, r *http.Request) error {
	var in fleet.Assignment
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, in.Target, identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("policies: assign %s to %s", in.Policy, in.Target)
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.Assign(in)), msg, author(r),
		app.AffectedHosts(a.cfg.Fleet(), in.Target)...); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

func (a *API) deleteAssignment(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Policy string `json:"policy"`
		Target string `json:"target"`
		Filter string `json:"filter,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, in.Target, identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("policies: unassign %s from %s", in.Policy, in.Target)
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.Unassign(in.Policy, in.Target, in.Filter)), msg, author(r),
		app.AffectedHosts(a.cfg.Fleet(), in.Target)...); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

func (a *API) putFilter(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	var fl fleet.Filter
	if err := decode(r, &fl); err != nil {
		return err
	}
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.PutFilter(id, fl)),
		"filters: put "+id, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

func (a *API) deleteFilter(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.DeleteFilter(id)),
		"filters: delete "+id, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

// rejectingMut wraps a domain mutation so its errors surface as 400 (caller
// data) rather than 500: domain mutations only fail on bad references.
func rejectingMut(m fleet.Mutation) fleet.Mutation {
	return func(f *fleet.Fleet) error {
		if err := m(f); err != nil {
			return reject(err)
		}
		return nil
	}
}
