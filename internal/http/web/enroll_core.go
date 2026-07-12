package web

import (
	"context"
	"fmt"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// enroll_core.go: the shared enrollment transaction, used by both the guided
// single-device flow and batch imaging. The CALLER authorizes (the scope
// differs: a single enroll checks the target group, a batch checks org once);
// this only performs the work, so the two entry points cannot drift.

// enrollOne enrolls one discovered device: it validates the hardware profile,
// captures the discovered specs and native nixos-facter document, commits the
// device (a gated, audited fleet.json change), optionally issues the device
// credential, seeds the captured facts, and drops the MAC from the station
// set. It returns the one-time credential secret when issueCred is set (the
// single-device flow shows it once); batch imaging defers credentials to the
// image step and passes issueCred=false.
func (s *Server) enrollOne(ctx context.Context, station, mac, tag, hardware, class string, groups []string, issueCred, removeMAC bool, author ports.Author) (string, error) {
	if s.svc.Discovery == nil {
		return "", fmt.Errorf("imaging stations need the observed store")
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", fmt.Errorf("enrolling a discovered device needs a tag")
	}
	hardware = strings.TrimSpace(hardware)
	// When the overlay published hardware profiles, the chosen profile must be
	// one of them - so a device only lands on a profile the generator builds.
	profiles := s.svc.Config.HardwareProfiles()
	if profiles.Len() > 0 && !profiles.Has(hardware) {
		return "", fmt.Errorf("hardware profile %q is not one of the published profiles", hardware)
	}

	dev := fleet.Device{Hardware: hardware, Class: strings.TrimSpace(class), Groups: groups}

	// Capture the hardware the station discovered: the flat summary onto the
	// device record, the native facter document into the facts store (below).
	var capturedFacter []byte
	if mac != "" {
		if d, ok, err := s.svc.Discovery.Get(ctx, station, mac); err != nil {
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
	if err := s.svc.Config.Apply(ctx, fleet.AddDevice(tag, dev), msg, author, tag); err != nil {
		return "", err
	}

	var secret string
	if issueCred && s.svc.DevCreds != nil {
		sec, err := s.svc.DevCreds.Issue(ctx, tag)
		if err != nil {
			s.log.Error("device enrolled but credential not issued", "tag", tag, "err", err)
		} else {
			secret = sec
		}
	}
	if capturedFacter != nil && s.svc.Inventory != nil {
		if err := s.svc.Inventory.RecordFacts(ctx, tag, capturedFacter); err != nil {
			s.log.Warn("device enrolled but captured facter not stored", "tag", tag, "err", err)
		}
	}
	if removeMAC && mac != "" {
		if err := s.svc.Discovery.Remove(ctx, station, mac); err != nil {
			s.log.Warn("device enrolled but not removed from station set", "station", station, "mac", mac, "err", err)
		}
	}
	return secret, nil
}
