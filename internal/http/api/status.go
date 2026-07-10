package api

import (
	"net/http"
)

// --- observed plane reads (operator-side) ---

func (a *API) getStatusAll(w http.ResponseWriter, r *http.Request) error {
	sts, err := a.inv.StatusAll(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, sts)
	return nil
}

func (a *API) getStatus(w http.ResponseWriter, r *http.Request) error {
	st, ok, err := a.inv.Status(r.Context(), r.PathValue("tag"))
	if err != nil {
		return err
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no check-in for device"})
		return nil
	}
	writeJSON(w, http.StatusOK, st)
	return nil
}

func (a *API) getFacts(w http.ResponseWriter, r *http.Request) error {
	facts, at, ok, err := a.inv.Facts(r.Context(), r.PathValue("tag"))
	if err != nil {
		return err
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no facts for device"})
		return nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"updatedAt": at, "facts": facts})
	return nil
}
