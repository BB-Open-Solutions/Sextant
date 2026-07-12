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
		"Stations":  stations,
		"CanEnroll": v.roleAt("org").Meets(identity.Editor),
		"CanOwn":    v.roleAt("org").Meets(identity.Owner),
	}

	if station != "" {
		st, registered := full.Stations[station]
		data["Station"] = station
		data["Registered"] = registered
		data["StationInfo"] = st
		data["ReportURL"] = reportURL(r, station)

		visible := full.VisibleTo(v.canView)
		groups := make([]string, 0, len(visible.Groups))
		for g := range visible.Groups {
			groups = append(groups, g)
		}
		sort.Strings(groups)
		data["Groups"] = groups
		data["Classes"] = deviceClasses

		discovered, err := s.svc.Discovery.List(r.Context(), station)
		data["Discovered"] = discovered
		if err != nil {
			data["Error"] = err.Error()
		}
		// Hardware-profile dropdown (never free text) + a per-device suggestion
		// derived from the discovered make/model, so the operator confirms the
		// profile instead of typing one. Empty profiles => the template falls
		// back to a text field (an overlay that predates the imaging surface).
		profiles := s.svc.Config.HardwareProfiles()
		data["Profiles"] = profiles.All()
		suggest := make(map[string]string, len(discovered))
		for _, d := range discovered {
			if name := profiles.Suggest(d.Vendor, d.Model); name != "" {
				suggest[d.MAC] = name
			}
		}
		data["Suggest"] = suggest
		// One-shot station credential (just minted): show once, then clear.
		if c, err := r.Cookie(stationCredCookie); err == nil && c.Value != "" {
			data["MintedSecret"] = c.Value
			http.SetCookie(w, &http.Cookie{Name: stationCredCookie, Value: "", Path: "/station", MaxAge: -1})
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
	if err := s.svc.Config.Apply(r.Context(), fleet.AddStation(tag, st),
		"stations: register "+tag, webAuthor(v)); err != nil {
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
	if err := s.svc.Config.Apply(r.Context(), fleet.RemoveStation(station),
		"stations: remove "+station, webAuthor(v)); err != nil {
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

// postStationEnroll enrolls a discovered device into the fleet, then drops it
// from the station's set. The operator confirms tag/hardware/group; the
// discovered facts pre-fill the form. Reuses the standard enroll path so a
// station-enrolled device is indistinguishable from a hand-enrolled one.
func (s *Server) postStationEnroll(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Discovery == nil {
		return fmt.Errorf("imaging stations need the observed store")
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

	hardware := strings.TrimSpace(r.FormValue("hardware"))
	// When the overlay published hardware profiles, the chosen profile must be
	// one of them (the form offers a dropdown, never free text) - so a device
	// can only be enrolled onto a profile the generator can actually build.
	profiles := s.svc.Config.HardwareProfiles()
	if profiles.Len() > 0 && !profiles.Has(hardware) {
		return fmt.Errorf("hardware profile %q is not one of the published profiles", hardware)
	}

	dev := fleet.Device{
		Hardware: hardware,
		Class:    strings.TrimSpace(r.FormValue("class")),
		Groups:   groups,
	}
	// Capture the hardware the station discovered. The flat summary (make/
	// model/cpu/mem/disk) goes onto the device record for the inventory card;
	// the authoritative NATIVE nixos-facter document is observed data, so it
	// is seeded into the facts store after enrollment (below), not into
	// fleet.json - the same place the agent's runtime facts land.
	var capturedFacter []byte
	if mac != "" {
		if d, ok, err := s.svc.Discovery.Get(r.Context(), station, mac); err != nil {
			s.log.Warn("could not read discovered specs at enroll", "station", station, "mac", mac, "err", err)
		} else if ok {
			spec := fleet.HardwareSpec{
				Vendor: d.Vendor, Model: d.Model, Serial: d.Serial,
				CPU: d.CPU, Cores: d.Cores, MemGB: d.MemGB, DiskGB: d.DiskGB,
				Firmware: d.Firmware,
			}
			if !spec.Empty() {
				dev.Spec = &spec
				dev.ITAM.Serial, dev.ITAM.Model = d.Serial, d.Model
			}
			if d.Facter != "" {
				capturedFacter = []byte(d.Facter)
			}
		}
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
	// Seed the native hardware facts captured at imaging, so the device page
	// shows its nixos-facter spec immediately (before the agent checks in).
	if capturedFacter != nil && s.svc.Inventory != nil {
		if err := s.svc.Inventory.RecordFacts(r.Context(), tag, capturedFacter); err != nil {
			s.log.Warn("device enrolled but captured facter not stored", "tag", tag, "err", err)
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
