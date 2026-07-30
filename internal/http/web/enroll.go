package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
)

// enroll.go: the guided enrollment flow reached from the overview's primary
// action. It walks an operator from "pick an imaging station" to "these
// devices are on the PXE network" to imaging them - one at a time with
// brand-specific guidance, or in bulk for a rack of 1..100+ identical
// machines. It reads the discovery plane and captures each device's record
// via discoveredDevice (enroll_core.go).

// enrollRow is one discovered device plus the profile suggested for it and
// that profile's brand-specific imaging steps (the guidance).
type enrollRow struct {
	Dev     discovery.Discovered
	Suggest string
	Steps   []fleet.ImagingStep
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// enrollPage renders the guided flow. Editor (somewhere) may image; the
// station picker and discovered list need Editor at org to be useful.
func (s *Server) enrollPage(w http.ResponseWriter, r *http.Request, v view) {
	if s.svc.Discovery == nil {
		http.Error(w, "imaging stations need the observed store", http.StatusServiceUnavailable)
		return
	}
	full := s.svc.Config.Fleet()
	stations := make([]string, 0, len(full.Stations))
	for tag := range full.Stations {
		stations = append(stations, tag)
	}
	sort.Strings(stations)

	data := map[string]any{
		"Title": "Enrollment", "Nav": "enroll",
		"Stations":  stations,
		"CanEnroll": v.roleAt("org").Meets(identity.Editor),
		// CanOwn gates the empty-state link to /station: registering a station
		// needs Owner (station.go), so a non-owner should not be pointed at a
		// page they cannot act on.
		"CanOwn": v.roleAt("org").Meets(identity.Owner),
	}

	station := strings.TrimSpace(r.URL.Query().Get("station"))
	if station != "" {
		if _, ok := full.Stations[station]; !ok {
			data["Error"] = fmt.Sprintf("unknown station %q", station)
			s.render(w, "enroll", data, v)
			return
		}
		data["Station"] = station

		profiles := s.svc.Config.HardwareProfiles()
		data["Profiles"] = profiles.All()

		discovered, err := s.svc.Discovery.List(r.Context(), station)
		if err != nil {
			data["Error"] = err.Error()
		}
		rows := make([]enrollRow, 0, len(discovered))
		for _, d := range discovered {
			row := enrollRow{Dev: d, Suggest: profiles.Suggest(d.Vendor, d.Model)}
			if row.Suggest != "" {
				if p, ok := profiles.Get(row.Suggest); ok {
					row.Steps = p.Steps
				}
			}
			rows = append(rows, row)
		}
		data["Rows"] = rows

		visible := full.VisibleTo(v.canView)
		groups := make([]string, 0, len(visible.Groups))
		for g := range visible.Groups {
			groups = append(groups, g)
		}
		sort.Strings(groups)
		data["Groups"] = groups
		data["Classes"] = fleet.Classes

		// In-flight and finished image jobs for this station.
		if s.svc.Imaging != nil {
			if jobs, err := s.svc.Imaging.List(r.Context(), station); err != nil {
				s.log.Warn("list image jobs failed", "station", station, "err", err)
			} else {
				data["Jobs"] = jobs
			}
		}
	}
	s.render(w, "enroll", data, v)
}

// postEnrollBatch images the selected discovered devices onto one shared
// hardware profile, group and class - each with the CMDB name the operator
// typed for it (no auto-generated tags). This is the only imaging path: even a
// single device goes through it, named. A device is named via the form field
// name-<macKey(mac)>; a selected device with a blank or duplicate name aborts
// the whole batch before any dispatch, so a rack is enrolled in one audited
// pass or not at all. Credentials are issued at the image step, not here.
func (s *Server) postEnrollBatch(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Discovery == nil {
		return fmt.Errorf("imaging stations need the observed store")
	}
	station := r.PathValue("station")
	if err := r.ParseForm(); err != nil {
		return err
	}
	macs := r.Form["mac"]
	if len(macs) == 0 {
		return fmt.Errorf("select at least one device to enroll")
	}
	hardware := strings.TrimSpace(r.FormValue("hardware"))
	class := strings.TrimSpace(r.FormValue("class"))
	group := strings.TrimSpace(r.FormValue("group"))

	// The hardware profile is shared across the batch, so validate it once,
	// up front: an unpublished profile fails the whole batch (all-or-nothing)
	// rather than letting every per-device enroll fail best-effort.
	profiles := s.svc.Config.HardwareProfiles()
	if profiles.Len() > 0 && !profiles.Has(hardware) {
		return fmt.Errorf("hardware profile %q is not one of the published profiles", hardware)
	}

	scope := "org"
	var groups []string
	if group != "" {
		scope = "group:" + group
		groups = []string{group}
	}
	if err := s.requireWeb(v, scope, identity.Editor); err != nil {
		return err
	}

	// Resolve + validate every device's CMDB name up front: blank or duplicate
	// names abort before any dispatch, so the batch is all-or-nothing.
	names := make(map[string]string, len(macs))
	seen := make(map[string]string, len(macs))
	for _, mac := range macs {
		name := strings.TrimSpace(r.FormValue("name-" + macKey(mac)))
		if name == "" {
			return fmt.Errorf("device %s needs a name", mac)
		}
		if !slugRE.MatchString(name) {
			return fmt.Errorf("name %q for %s must be a lowercase slug (a-z, 0-9, -)", name, mac)
		}
		if other, dup := seen[name]; dup {
			return fmt.Errorf("name %q is used for both %s and %s - names must be unique", name, other, mac)
		}
		seen[name] = mac
		names[mac] = name
	}

	// ONE gated apply for the whole batch: the gate evaluates all new hosts
	// in a single (chunked) run instead of once per device, so dispatching a
	// rack costs one evaluation, not N. All-or-nothing stays intact - one
	// mutation, one commit, one audit entry. Job dispatch and facts capture
	// are post-success side effects.
	type pending struct {
		mac, tag string
		dev      fleet.Device
		facter   []byte
	}
	items := make([]pending, 0, len(macs))
	tags := make([]string, 0, len(macs))
	for _, mac := range macs {
		dev, facter := s.discoveredDevice(r.Context(), station, mac, hardware, class, groups)
		items = append(items, pending{mac: mac, tag: names[mac], dev: dev, facter: facter})
		tags = append(tags, names[mac])
	}
	mut := func(f *fleet.Fleet) error {
		for _, p := range items {
			if err := fleet.AddDevice(p.tag, p.dev)(f); err != nil {
				return err
			}
		}
		return nil
	}
	msg := fmt.Sprintf("devices: enroll %d device(s) from station %s (%s)", len(items), station, hardware)
	author := webAuthor(v)
	if err := s.runGated(r, v, msg, func(ctx context.Context) error {
		if err := s.svc.Config.Apply(ctx, mut, msg, author, tags...); err != nil {
			return err
		}
		for _, p := range items {
			if p.facter != nil && s.svc.Inventory != nil {
				if err := s.svc.Inventory.RecordFacts(ctx, p.tag, p.facter); err != nil {
					s.log.Warn("device enrolled but captured facter not stored", "tag", p.tag, "err", err)
				}
			}
			if s.svc.Imaging == nil {
				// No imaging plane: direct enrollment - issue the credential
				// and clear the discovery row, as the single path does.
				if s.svc.DevCreds != nil {
					if _, err := s.svc.DevCreds.Issue(ctx, p.tag); err != nil {
						s.log.Error("device enrolled but credential not issued", "tag", p.tag, "err", err)
					}
				}
				if err := s.svc.Discovery.Remove(ctx, station, p.mac); err != nil {
					s.log.Warn("device enrolled but not removed from station set", "station", station, "mac", p.mac, "err", err)
				}
				continue
			}
			// Imaging path: the MAC stays visible with its job and the station
			// receives a fresh credential when it claims the job.
			// Install the revision this device's ring is pinned to, so it is
			// converged at first boot rather than born ahead of its own ring.
			// Empty when the device is in no ring: the station then falls back
			// to main, which is the old behaviour and correct for that case.
			rev := ""
			if cur := s.svc.Config.Fleet(); cur != nil {
				if d, ok := cur.Devices[p.tag]; ok {
					rev = app.TargetRevision(cur, d)
				}
			}
			if err := s.svc.Imaging.Dispatch(ctx, imaging.Job{
				Station: station, MAC: imaging.NormalizeMAC(p.mac), Tag: p.tag, Hardware: hardware,
				Rev: rev,
			}); err != nil {
				s.log.Warn("batch image: dispatch failed", "station", station, "mac", p.mac, "tag", p.tag, "err", err)
			}
		}
		s.log.Info("batch image dispatched", "station", station, "devices", len(items))
		return nil
	}); err != nil {
		return err
	}
	// Land where the effect is: the wizard lists the jobs just dispatched
	// (Pending onwards) and drives the ceremony from there. Without an
	// imaging plane no jobs exist and the wizard has nothing to show, so
	// that path stays on the station page.
	if s.svc.Imaging != nil {
		http.Redirect(w, r, "/enroll/"+url.PathEscape(station)+"/wizard", http.StatusSeeOther)
		return nil
	}
	http.Redirect(w, r, "/enroll?station="+url.QueryEscape(station), http.StatusSeeOther)
	return nil
}

// postDiscoveredRemove drops a MAC from a station's discovered set: a device
// that never enrolled, or a stale lease lingering after a machine powered off,
// so the operator's discovered view stays clean without imaging it.
func (s *Server) postDiscoveredRemove(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Discovery == nil {
		return fmt.Errorf("imaging stations need the observed store")
	}
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	station := r.PathValue("station")
	if err := s.svc.Discovery.Remove(r.Context(), station, r.PathValue("mac")); err != nil {
		return err
	}
	http.Redirect(w, r, "/enroll?station="+url.QueryEscape(station), http.StatusSeeOther)
	return nil
}

// postEnrollJobCancel withdraws a pending/failed image job.
func (s *Server) postEnrollJobCancel(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Imaging == nil {
		return fmt.Errorf("imaging execution needs the database (postgres not configured)")
	}
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	station := r.PathValue("station")
	if err := s.svc.Imaging.Cancel(r.Context(), station, r.PathValue("mac")); err != nil {
		return err
	}
	http.Redirect(w, r, "/enroll?station="+url.QueryEscape(station), http.StatusSeeOther)
	return nil
}

// macKey turns a MAC into a stable, form-field-safe key (no colons), so each
// selected device's CMDB-name input can be addressed as name-<macKey> and
// paired back to its MAC on submit (e.g. "aa:bb:cc:dd:ee:ff" -> "aabbccddeeff").
func macKey(mac string) string {
	return strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.ToLower(strings.TrimSpace(mac)))
}
