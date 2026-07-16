package web

import (
	"context"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// enroll_core.go: the discovered-device record builder shared by the batch
// enroll path. The one-batch gated apply (enroll.go postEnrollBatch) replaced
// the old per-device enrollOne transaction - the record capture below is the
// piece both worlds share.

// discoveredDevice builds the device record for a discovered MAC: the flat
// hardware summary onto the record, the native facter document returned for
// the facts store. Missing discovery data degrades to a bare record - specs
// are enrichment, never a precondition.
func (s *Server) discoveredDevice(ctx context.Context, station, mac, hardware, class string, groups []string) (fleet.Device, []byte) {
	dev := fleet.Device{Hardware: strings.TrimSpace(hardware), Class: strings.TrimSpace(class), Groups: groups}
	if mac == "" {
		return dev, nil
	}
	d, ok, err := s.svc.Discovery.Get(ctx, station, mac)
	if err != nil {
		s.log.Warn("could not read discovered specs at enroll", "station", station, "mac", mac, "err", err)
		return dev, nil
	}
	if !ok {
		return dev, nil
	}
	spec := fleet.HardwareSpec{
		Vendor: d.Vendor, Model: d.Model, Serial: d.Serial,
		CPU: d.CPU, Cores: d.Cores, MemGB: d.MemGB, DiskGB: d.DiskGB,
		Firmware: d.Firmware,
	}
	if !spec.Empty() {
		dev.Spec = &spec
		dev.ITAM.Serial, dev.ITAM.Model = d.Serial, d.Model
	}
	var facter []byte
	if d.Facter != "" {
		facter = []byte(d.Facter)
	}
	return dev, facter
}
