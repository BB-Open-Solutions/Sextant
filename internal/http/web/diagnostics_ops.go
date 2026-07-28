package web

import (
	"net/http"
	"strconv"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// deviceDiagnostics streams a device's diagnostics bundle (design 0010) as a
// gzip download. Editor reach at the device scope, like requesting the
// collection; the download is logged with the actor (journals can contain
// personal data). Retention is enforced by the service - an expired bundle
// reads as absent.
func (s *Server) deviceDiagnostics(w http.ResponseWriter, r *http.Request, v view) {
	tag := r.PathValue("tag")
	if err := s.requireWeb(v, "device:"+tag, identity.Editor); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if s.svc.Diagnostics == nil {
		http.Error(w, "diagnostics is disabled in this deployment", http.StatusNotFound)
		return
	}
	bundle, meta, ok, err := s.svc.Diagnostics.Get(r.Context(), tag)
	if err != nil {
		s.log.Error("diagnostics bundle read failed", "tag", tag, "err", err)
		http.Error(w, "could not read the bundle", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.log.Info("diagnostics bundle downloaded", "tag", tag, "by", v.User.Subject, "bytes", len(bundle))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+tag+`-diagnostics-`+meta.Created.UTC().Format("2006-01-02")+`.gz"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(bundle)))
	_, _ = w.Write(bundle)
}
