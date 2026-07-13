package web

import (
	"net/http"
	"strconv"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/mail"
)

// mail.go: the per-organisation SMTP surface. Owners configure how this tenant
// sends notification e-mail (its own domain), choosing between a mounted
// secret reference for the password (the default) or, where an encryption key
// is present, a value typed into the console and sealed at rest.

// mailPage shows the SMTP configuration form and a test-send. Owner-only.
func (s *Server) mailPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	data := map[string]any{"Title": "E-mail (SMTP)", "Nav": "mail"}
	if s.svc.Mail == nil {
		data["Unavailable"] = true
		s.render(w, "mail", data, v)
		return
	}
	cfg, ok, err := s.svc.Mail.Config(r.Context())
	if err != nil {
		s.log.Warn("mail config load failed", "err", err)
		data["Error"] = "Could not load the SMTP configuration."
	}
	data["Configured"] = ok
	data["Cfg"] = cfg
	data["CanEnterSecret"] = s.svc.Mail.CanStoreEnteredSecret()
	data["TestTo"] = v.User.Email
	data["Sent"] = r.URL.Query().Get("sent") == "1"
	s.render(w, "mail", data, v)
}

// postMailSave stores the SMTP configuration. Owner-only.
func (s *Server) postMailSave(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	port, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
	cfg := mail.Config{
		Host:        strings.TrimSpace(r.FormValue("host")),
		Port:        port,
		From:        strings.TrimSpace(r.FormValue("from")),
		Username:    strings.TrimSpace(r.FormValue("username")),
		PasswordRef: strings.TrimSpace(r.FormValue("passwordRef")),
		Security:    mail.Security(strings.TrimSpace(r.FormValue("security"))),
	}
	if err := s.svc.Mail.Save(r.Context(), cfg, r.FormValue("password")); err != nil {
		return err
	}
	http.Redirect(w, r, "/mail", http.StatusSeeOther)
	return nil
}

// postMailTest sends a test message to prove the configuration. Owner-only.
func (s *Server) postMailTest(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	to := strings.TrimSpace(r.FormValue("to"))
	if to == "" {
		to = v.User.Email
	}
	if err := s.svc.Mail.SendTest(r.Context(), to); err != nil {
		return err
	}
	http.Redirect(w, r, "/mail?sent=1", http.StatusSeeOther)
	return nil
}

// postMailDelete removes the SMTP configuration. Owner-only.
func (s *Server) postMailDelete(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if err := s.svc.Mail.Delete(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/mail", http.StatusSeeOther)
	return nil
}
