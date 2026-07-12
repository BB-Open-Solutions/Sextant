package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// stationCredCookie stages a just-minted station credential for the station
// page to show exactly once (same one-shot pattern as device credentials).
const stationCredCookie = "sextant_stationcred"

// setStationCredCookie stages the station's credential over the redirect.
// Secure is hardcoded: on plain HTTP the browser drops it and the secret is
// never shown, rather than travelling in clear text.
func setStationCredCookie(w http.ResponseWriter, station, secret string) {
	http.SetCookie(w, &http.Cookie{Name: stationCredCookie, Value: secret,
		Path: "/station/" + station, MaxAge: 60, HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode})
}

// station.go: the inspoelstraat surface. An imaging station reports devices it
// has discovered over PXE; an operator enrolls one into the fleet, and an org
// owner mints the credential the station uses to report (ADR 0008).

// stationPage lists a station's discovered devices and the owner controls
// (mint a station credential). Org Viewer to look; enroll/mint are gated on
// their own POST handlers.
func (s *Server) stationPage(w http.ResponseWriter, r *http.Request, v view) {
	if s.svc.Discovery == nil {
		http.Error(w, "the inspoelstraat plane needs the observed store (Postgres)", http.StatusNotFound)
		return
	}
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	// The page is reached by station tag from the query so a plain GET form
	// can navigate to it; the enroll/mint actions carry the tag in the path.
	station := strings.TrimSpace(r.URL.Query().Get("tag"))
	if station == "" {
		s.render(w, "station", map[string]any{"Title": "Inspoelstraat", "Nav": "station"}, v)
		return
	}
	discovered, err := s.svc.Discovery.List(r.Context(), station)
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	data := map[string]any{
		"Title": "Inspoelstraat " + station, "Nav": "station",
		"Station":    station,
		"Discovered": discovered,
		"Groups":     groups,
		"CanEnroll":  v.roleAt("org").Meets(identity.Editor),
		"CanOwn":     v.roleAt("org").Meets(identity.Owner),
	}
	// One-shot station credential (just minted): show once, then clear.
	if c, err := r.Cookie(stationCredCookie); err == nil && c.Value != "" {
		data["MintedSecret"] = c.Value
		http.SetCookie(w, &http.Cookie{Name: stationCredCookie, Value: "", Path: "/station/" + station, MaxAge: -1})
	}
	if err != nil {
		data["Error"] = err.Error()
	}
	s.render(w, "station", data, v)
}

// postStationCredential mints (or rotates) the station's report credential and
// shows it once. Org Owner: a station credential lets a host push discoveries.
func (s *Server) postStationCredential(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.StationCreds == nil {
		return fmt.Errorf("station credentials need the token store (Postgres)")
	}
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	station := r.PathValue("tag")
	secret, err := s.svc.StationCreds.Issue(r.Context(), station)
	if err != nil {
		return err
	}
	// Reuse the one-shot device-credential cookie machinery: the station page
	// reads it and shows the secret exactly once.
	setStationCredCookie(w, station, secret)
	http.Redirect(w, r, "/station/"+station, http.StatusSeeOther)
	return nil
}

// postStationEnroll enrolls a discovered device into the fleet, then drops it
// from the station's set. The operator confirms tag/hardware/group; the
// discovered facts pre-fill the form. Reuses the standard enroll path so a
// station-enrolled device is indistinguishable from a hand-enrolled one.
func (s *Server) postStationEnroll(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Discovery == nil {
		return fmt.Errorf("the inspoelstraat plane needs the observed store")
	}
	station := r.PathValue("tag")
	mac := r.FormValue("mac")
	tag := strings.TrimSpace(r.FormValue("tag"))
	group := r.FormValue("group")

	scope := "org"
	var groups []string
	if group != "" {
		scope = "group:" + group
		groups = []string{group}
	}
	if err := s.requireWeb(v, scope, identity.Editor); err != nil {
		return err
	}
	if tag == "" {
		return fmt.Errorf("enrolling a discovered device needs a tag")
	}

	dev := fleet.Device{
		Hardware: strings.TrimSpace(r.FormValue("hardware")),
		Class:    strings.TrimSpace(r.FormValue("class")),
		Groups:   groups,
	}
	msg := fmt.Sprintf("devices: enroll %s from station %s (%s)", tag, station, dev.Hardware)
	if err := s.svc.Config.Apply(r.Context(), fleet.AddDevice(tag, dev), msg, webAuthor(v), tag); err != nil {
		return err
	}
	if s.svc.DevCreds != nil {
		if secret, err := s.svc.DevCreds.Issue(r.Context(), tag); err != nil {
			s.log.Error("device enrolled but credential not issued", "tag", tag, "err", err)
		} else {
			setDevCredCookie(w, tag, secret)
		}
	}
	// Drop the MAC from the station set: it is enrolled now, not discovered.
	if mac != "" {
		if err := s.svc.Discovery.Remove(r.Context(), station, mac); err != nil {
			s.log.Warn("device enrolled but not removed from station set", "station", station, "mac", mac, "err", err)
		}
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}
