package api

import (
	"net/http"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// --- change requests ---
// Change requests operate on the whole document (their diffs expose every
// scope), so reading them requires org-wide Viewer.

func (a *API) getChanges(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Viewer); err != nil {
		return err
	}
	crs, err := a.changes.List(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, crs)
	return nil
}

func (a *API) getChange(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Viewer); err != nil {
		return err
	}
	cr, ok, err := a.changes.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return reject(err)
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown change"})
		return nil
	}
	writeJSON(w, http.StatusOK, cr)
	return nil
}

// getChangeDiff returns the unified diff an approver reviews before merge.
func (a *API) getChangeDiff(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Viewer); err != nil {
		return err
	}
	diff, err := a.changes.Diff(r.Context(), r.PathValue("id"))
	if err != nil {
		return wrapChangeErr(err)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(diff))
	return nil
}

func (a *API) postChange(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, "org", identity.Editor); err != nil {
		return err
	}
	cr, err := a.changes.Open(r.Context(), in.ID, in.Title, author(r))
	if err != nil {
		return reject(err)
	}
	writeJSON(w, http.StatusCreated, cr)
	return nil
}

// postChangeEdit applies one gated setting edit on the change's branch.
func (a *API) postChangeEdit(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	var in struct {
		Scope   string `json:"scope"`
		Key     string `json:"key"`
		Value   any    `json:"value"`
		Enforce *bool  `json:"enforce,omitempty"`
		// Clear removes the key instead of setting it.
		Clear bool `json:"clear,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, in.Scope, identity.Editor); err != nil {
		return err
	}
	mut := func(f *fleet.Fleet) error {
		if in.Clear {
			return rejectingMut(fleet.ClearScopeSetting(in.Scope, in.Key))(f)
		}
		if err := rejectingMut(fleet.SetScopeSetting(in.Scope, in.Key, in.Value))(f); err != nil {
			return err
		}
		if in.Enforce != nil {
			return rejectingMut(fleet.SetScopeEnforce(in.Scope, in.Key, *in.Enforce))(f)
		}
		return nil
	}
	verb := "set"
	if in.Clear {
		verb = "clear"
	}
	msg := "change " + id + ": " + verb + " " + in.Key + " at " + in.Scope
	if err := a.changes.Edit(r.Context(), id, mut, msg, author(r)); err != nil {
		return wrapChangeErr(err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

func (a *API) postChangeSubmit(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Editor); err != nil {
		return err
	}
	cr, err := a.changes.Submit(r.Context(), r.PathValue("id"))
	if err != nil {
		return wrapChangeErr(err)
	}
	writeJSON(w, http.StatusOK, cr)
	return nil
}

func (a *API) postChangeMerge(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	cr, err := a.changes.Merge(r.Context(), r.PathValue("id"), author(r))
	if err != nil {
		return wrapChangeErr(err)
	}
	writeJSON(w, http.StatusOK, cr)
	return nil
}

func (a *API) postChangeAbandon(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Editor); err != nil {
		return err
	}
	cr, err := a.changes.Abandon(r.Context(), r.PathValue("id"))
	if err != nil {
		return wrapChangeErr(err)
	}
	writeJSON(w, http.StatusOK, cr)
	return nil
}

// wrapChangeErr maps change-flow errors: state machine violations and
// unknown ids are caller errors; gate and conflict pass through for the
// standard 422/409 mapping.
func wrapChangeErr(err error) error {
	if err == nil {
		return nil
	}
	m := err.Error()
	if strings.Contains(m, "unknown change") || strings.Contains(m, "cannot move change") ||
		strings.Contains(m, "four-eyes required") ||
		strings.Contains(m, "only draft") || strings.Contains(m, "only ready") ||
		strings.Contains(m, "no pending diff") ||
		strings.Contains(m, "already exists") || strings.Contains(m, "invalid change-request id") {
		return reject(err)
	}
	return err
}

// --- rollout ---

func (a *API) getRollout(w http.ResponseWriter, r *http.Request) error {
	// The plan enumerates rings and groups: org-wide read.
	if err := a.require(r, "org", identity.Viewer); err != nil {
		return err
	}
	st, rings, err := a.rollouts.Status(r.Context())
	if err != nil {
		return err
	}
	if st == nil {
		writeJSON(w, http.StatusOK, map[string]any{"active": false})
		return nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": st.Status == "active", "state": st, "rings": rings})
	return nil
}

func (a *API) postRollout(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Target string `json:"target"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	st, err := a.rollouts.Start(r.Context(), in.Target, author(r))
	if err != nil {
		return reject(err)
	}
	writeJSON(w, http.StatusCreated, st)
	return nil
}

func (a *API) postRolloutTick(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	act, st, err := a.rollouts.Tick(r.Context())
	if err != nil {
		return err
	}
	out := map[string]any{"state": st}
	if act != nil {
		out["action"] = map[string]string{"kind": string(act.Kind), "reason": act.Reason}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (a *API) deleteRollout(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	st, err := a.rollouts.Cancel(r.Context())
	if err != nil {
		return reject(err)
	}
	writeJSON(w, http.StatusOK, st)
	return nil
}
