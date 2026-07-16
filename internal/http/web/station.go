package web

import (
	"fmt"
	"net/http"
	"net/url"
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
func setStationCredCookie(w http.ResponseWriter, secret string) {
	// Path is /station (the page is served there with ?tag=), so the cookie
	// is actually sent back to the page that shows it once.
	http.SetCookie(w, &http.Cookie{Name: stationCredCookie, Value: secret,
		Path: "/station", MaxAge: 60, HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode})
}

// station.go: the imaging-station (inspoelstraat) surface. A station reports
// devices it has discovered over PXE; an operator enrolls one into the fleet,
// and an org owner registers stations and mints their report credentials.

// deviceClasses is the fixed set of device classes offered as a choice, so an
// operator never has to remember or free-type one.
var deviceClasses = []string{"laptop", "workstation", "kiosk", "server", "station"}

// reportURL is the endpoint a station posts discoveries to, built from the
// request so the console shows the operator exactly what to configure.
func reportURL(r *http.Request, station string) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/api/station/" + station + "/report"
}

// stationPage lists a station's discovered devices and the owner controls
// (mint a station credential). Org Viewer to look; enroll/mint are gated on
// their own POST handlers.
func (s *Server) stationPage(w http.ResponseWriter, r *http.Request, v view) {
	if s.svc.Discovery == nil {
		http.Error(w, "imaging stations need the observed store (Postgres)", http.StatusNotFound)
		return
	}
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	full := s.svc.Config.Fleet()
	stations := make([]string, 0, len(full.Stations))
	for tag := range full.Stations {
		stations = append(stations, tag)
	}
	sort.Strings(stations)

	// The selected station comes from the query, so a plain dropdown+button
	// GET form navigates here; the enroll/mint actions carry it in the path.
	station := strings.TrimSpace(r.URL.Query().Get("tag"))
	data := map[string]any{
		"Title": "Imaging stations", "Nav": "station",
		"Stations": stations,
		"CanOwn":   v.roleAt("org").Meets(identity.Owner),
	}

	// The Stations page is station ADMIN only: register/remove a station, mint
	// its report credential, show the setup instructions. The device-facing
	// discovery + imaging flow lives on /enroll, so the two do not overlap.
	if station != "" {
		st, registered := full.Stations[station]
		data["Station"] = station
		data["Registered"] = registered
		data["StationInfo"] = st
		data["ReportURL"] = reportURL(r, station)

		if discovered, err := s.svc.Discovery.List(r.Context(), station); err == nil {
			data["SeenCount"] = len(discovered)
		}
		// One-shot station credential (just minted): show once, then clear.
		if c, err := r.Cookie(stationCredCookie); err == nil && c.Value != "" {
			data["MintedSecret"] = c.Value
			// Clear with the same attributes it was set with (HttpOnly/Secure/
			// Path) so the deletion reliably overwrites the one-shot cookie.
			http.SetCookie(w, &http.Cookie{Name: stationCredCookie, Value: "",
				Path: "/station", MaxAge: -1, HttpOnly: true, Secure: true})
		}
	}
	s.render(w, "station", data, v)
}

// postStationRegister registers a new imaging station (config-as-data, an
// audited commit). Org Owner: registering a station is fleet infrastructure.
func (s *Server) postStationRegister(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	tag := strings.TrimSpace(r.FormValue("tag"))
	st := fleet.Station{
		Description: strings.TrimSpace(r.FormValue("description")),
		Site:        strings.TrimSpace(r.FormValue("site")),
	}
	if err := s.applyGated(r, v, fleet.AddStation(tag, st),
		"stations: register "+tag); err != nil {
		return err
	}
	http.Redirect(w, r, "/station?tag="+url.QueryEscape(tag), http.StatusSeeOther)
	return nil
}

// postStationRemove unregisters a station and revokes its report credential.
func (s *Server) postStationRemove(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	station := r.PathValue("tag")
	if err := s.applyGated(r, v, fleet.RemoveStation(station),
		"stations: remove "+station); err != nil {
		return err
	}
	if s.svc.StationCreds != nil {
		if err := s.svc.StationCreds.Revoke(r.Context(), station); err != nil {
			s.log.Warn("station removed but credential revoke failed", "station", station, "err", err)
		}
	}
	http.Redirect(w, r, "/station", http.StatusSeeOther)
	return nil
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
	// One-shot: the station page reads the cookie and shows the secret once.
	setStationCredCookie(w, secret)
	http.Redirect(w, r, "/station?tag="+url.QueryEscape(station), http.StatusSeeOther)
	return nil
}
