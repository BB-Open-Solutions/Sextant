package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

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
	// Disko is the profile's note of the disk layout. It is the one line an
	// operator checks before letting a machine be wiped, so it belongs next
	// to the machine and not only in the overlay's json.
	Disko string
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// enrollPage renders the guided flow. Editor (somewhere) may image; the
// station picker and discovered list need Editor at org to be useful.
// hardwareInUse lists hardware names devices already carry that the overlay's
// profile catalog does not describe, sorted. They are offered in the picker
// beside the catalogued ones: an operator enrolling the second machine of a
// model should find the name the first one got, not type it again.
func hardwareInUse(f *fleet.Fleet, profiles *fleet.HardwareProfiles) []string {
	known := map[string]bool{}
	for _, p := range profiles.All() {
		known[p.Name] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range f.Devices {
		if d.Hardware == "" || known[d.Hardware] || seen[d.Hardware] {
			continue
		}
		seen[d.Hardware] = true
		out = append(out, d.Hardware)
	}
	sort.Strings(out)
	return out
}

func (s *Server) enrollPage(w http.ResponseWriter, r *http.Request, v view) {
	if s.svc.Discovery == nil {
		s.unavailable(w, r, v, v.L.T("degraded.needs_store"))
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
		// Names already carried by devices, on top of the catalog. A fleet
		// enrolled before its overlay described a model still has that model
		// in use, and leaving it out of the picker is how the same machine
		// ends up spelled two ways.
		data["HardwareInUse"] = hardwareInUse(full, profiles)

		discovered, err := s.svc.Discovery.List(r.Context(), station)
		if err != nil {
			data["Error"] = err.Error()
		}
		rows := make([]enrollRow, 0, len(discovered))
		for _, d := range discovered {
			row := enrollRow{Dev: d, Suggest: profiles.Suggest(d.Vendor, d.Model)}
			if row.Suggest != "" {
				if p, ok := profiles.Get(row.Suggest); ok {
					row.Steps, row.Disko = p.Steps, p.Disko
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
	// Re-imaging the same chassis updates its unconfirmed enrolment instead of
	// minting a second one. Resolved INSIDE the mutation, against the fleet the
	// write actually sees, so two operators enrolling at once cannot both miss
	// the same existing record.
	reused := make([]string, 0, len(items))
	mut := func(f *fleet.Fleet) error {
		reused = reused[:0]
		for _, p := range items {
			if prior, ok := f.ProvisionalBySerial(p.dev.ITAM.Serial); ok {
				// Keep the original tag - it is on the operator's label and in
				// the imaging job - and refresh what the new scan learned.
				d := f.Devices[prior]
				d.Hardware, d.Class, d.Groups = p.dev.Hardware, p.dev.Class, p.dev.Groups
				d.Spec, d.ITAM = p.dev.Spec, p.dev.ITAM
				f.Devices[prior] = d
				reused = append(reused, prior)
				continue
			}
			if err := fleet.AddDevice(p.tag, p.dev, time.Now())(f); err != nil {
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
		// The commit that created these devices is what they must be imaged
		// from, and their rings have to contain it. Refusing here is the
		// point: a ring carrying a real pending change must be promoted
		// deliberately, not swept along by somebody imaging a laptop.
		enrolRev := s.svc.Config.Head(ctx)
		if s.svc.Rollouts != nil && enrolRev != "" {
			moved, err := app.EnsureRingsContain(ctx, s.svc.Config, s.svc.Rollouts.Refs(), tags, enrolRev)
			if err != nil {
				return err
			}
			for _, m := range moved {
				s.log.Info("ring advanced to the enrolment commit",
					"group", m.Group, "from", m.From, "to", m.To)
			}
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
			//
			// Install the enrolment commit itself. It is the earliest revision
			// that CONTAINS the device, and enrolRev has already made every
			// covering ring branch point at it, so the machine boots exactly at
			// its ring's head - not ahead of it (#16, comin refuses a head that
			// is not a descendant) and not missing from it (the failure this
			// replaced: nixos-anywhere could not find the host's attribute at
			// all). Empty only when HEAD is unreadable, and then the station
			// falls back to main as it always did.
			if err := s.svc.Imaging.Dispatch(ctx, imaging.Job{
				Station: station, MAC: imaging.NormalizeMAC(p.mac), Tag: p.tag, Hardware: hardware,
				Rev: enrolRev,
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
