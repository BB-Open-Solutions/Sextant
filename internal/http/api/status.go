package api

import (
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
)

// --- observed plane reads (operator-side) ---
// Read-confidentiality mirrors the config plane: status and facts are only
// served for devices the caller may view, and an invisible device answers
// exactly like a missing one.

func (a *API) getStatusAll(w http.ResponseWriter, r *http.Request) error {
	sts, err := a.inv.StatusAll(r.Context())
	if err != nil {
		return err
	}
	canView := a.canView(r)
	out := make([]app.StatusView, 0, len(sts))
	for _, st := range sts {
		if canView("device:" + st.Tag) {
			out = append(out, st)
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (a *API) getStatus(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	st, ok, err := a.inv.Status(r.Context(), tag)
	if err != nil {
		return err
	}
	if !ok || !a.canView(r)("device:"+tag) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no check-in for device"})
		return nil
	}
	writeJSON(w, http.StatusOK, st)
	return nil
}

func (a *API) getFacts(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	facts, at, ok, err := a.inv.Facts(r.Context(), tag)
	if err != nil {
		return err
	}
	if !ok || !a.canView(r)("device:"+tag) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no facts for device"})
		return nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"updatedAt": at, "facts": facts})
	return nil
}
