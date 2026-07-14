package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// DefaultTenant names the single-tenant namespace until multi-tenant
// routing lands (phase 5+); the storage schema is tenant-ready today.
const DefaultTenant = "default"

// maxFactsBytes bounds a facter report (raw nixos-facter JSON).
const maxFactsBytes = 256 << 10

// InventoryService is the observed plane: device check-ins, status reads
// and hardware facts.
type InventoryService struct {
	status ports.StatusStore
	facts  ports.InventoryStore
	clock  ports.Clock
	tenant string

	// notifier and wipeAudience are optional: when set, the FIRST check-in
	// that confirms a crypto-wipe outcome raises a notification to the
	// audience (the groups that own the fleet).
	notifier     Notifier
	wipeAudience []string
}

// NewInventoryService wires the observed plane for one tenant namespace.
func NewInventoryService(status ports.StatusStore, facts ports.InventoryStore, clock ports.Clock, tenant string) *InventoryService {
	if tenant == "" {
		tenant = DefaultTenant
	}
	return &InventoryService{status: status, facts: facts, clock: clock, tenant: tenant}
}

// WithNotifier makes check-in raise a notification the first time a device
// confirms a wipe outcome (executed, refused or failed), to the given groups.
func (s *InventoryService) WithNotifier(n Notifier, audience []string) *InventoryService {
	s.notifier = n
	s.wipeAudience = audience
	return s
}

// CheckIn records one device report; facts, when present, must be valid
// JSON and within bounds.
func (s *InventoryService) CheckIn(ctx context.Context, c observed.CheckIn, facts []byte) error {
	if err := c.Validate(); err != nil {
		return err
	}
	// Validate the facts payload BEFORE any side effect: a malformed or
	// oversized facts blob must reject the whole check-in with nothing
	// recorded and no notification fired, not a partially-applied call driven
	// by agent-controlled bytes.
	if len(facts) > 0 {
		if len(facts) > maxFactsBytes {
			return fmt.Errorf("facts report exceeds %d bytes", maxFactsBytes)
		}
		if !json.Valid(facts) {
			return fmt.Errorf("facts report is not valid JSON")
		}
	}
	now := s.clock.Now()
	// A device echoes its last ack on every beat, so only the transition INTO
	// a wipe outcome is news. Whether the ack actually changed is derived
	// from the store's own atomic write (ackChanged), not a separate read
	// beforehand - a read-then-write here would let two concurrent check-ins
	// for the same tag both observe the same prior ack and both raise the
	// notification, duplicating an alert for a security-relevant event.
	ackChanged, err := s.status.Upsert(ctx, s.tenant, c, now)
	if err != nil {
		return err
	}
	if s.notifier != nil && ackChanged && isWipeAck(c.Ack) {
		s.emitWipe(ctx, c.Tag, c.Ack)
	}
	if len(facts) > 0 {
		return s.facts.PutFacts(ctx, s.tenant, c.Tag, facts, now)
	}
	return nil
}

// RecordFacts stores a hardware facts document for a device outside a
// check-in - used when an imaging station captured the native nixos-facter
// spec at enrollment, so the console shows the device's hardware before its
// agent ever reports. Same validation and bounds as the check-in path.
func (s *InventoryService) RecordFacts(ctx context.Context, tag string, facts []byte) error {
	if tag == "" {
		return fmt.Errorf("recording facts needs a device tag")
	}
	if len(facts) == 0 {
		return nil
	}
	if len(facts) > maxFactsBytes {
		return fmt.Errorf("facts report exceeds %d bytes", maxFactsBytes)
	}
	if !json.Valid(facts) {
		return fmt.Errorf("facts report is not valid JSON")
	}
	return s.facts.PutFacts(ctx, s.tenant, tag, facts, s.clock.Now())
}

// isWipeAck reports whether an ack is one of the crypto-wipe outcomes.
func isWipeAck(ack string) bool {
	switch ack {
	case observed.AckWipe, observed.AckWipeRefused, observed.AckWipeFailed:
		return true
	}
	return false
}

// emitWipe raises a best-effort notification to each owner group describing a
// device's wipe outcome. The tone is carried by the title so it stands out in
// the inbox: a completed wipe is final, a refusal or failure needs attention.
func (s *InventoryService) emitWipe(ctx context.Context, tag, ack string) {
	title, body := "Device wiped: "+tag, "The device confirmed a crypto-wipe. Its disk key is destroyed."
	switch ack {
	case observed.AckWipeRefused:
		title = "Wipe refused: " + tag
		body = "The device declined the wipe intent (unarmed or an interlock blocked it)."
	case observed.AckWipeFailed:
		title = "Wipe failed: " + tag
		body = "The device attempted a crypto-wipe but did not complete it. Investigate the device."
	}
	for _, g := range s.wipeAudience {
		_ = s.notifier.Emit(ctx, notify.Notification{
			Audience: g, Kind: notify.WipeExecuted,
			Title: title, Body: body, Link: "/devices/" + tag,
		})
	}
}

// StatusView is a device's observed state plus the derived online flag.
type StatusView struct {
	observed.DeviceStatus
	Online bool `json:"online"`
}

// Status returns one device's observed state.
func (s *InventoryService) Status(ctx context.Context, tag string) (StatusView, bool, error) {
	st, ok, err := s.status.Get(ctx, s.tenant, tag)
	if err != nil || !ok {
		return StatusView{}, ok, err
	}
	return StatusView{DeviceStatus: st, Online: st.Online(s.clock.Now())}, true, nil
}

// StatusAll returns every device's observed state, tag-sorted.
func (s *InventoryService) StatusAll(ctx context.Context) ([]StatusView, error) {
	sts, err := s.status.List(ctx, s.tenant)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	out := make([]StatusView, 0, len(sts))
	for _, st := range sts {
		out = append(out, StatusView{DeviceStatus: st, Online: st.Online(now)})
	}
	return out, nil
}

// Facts returns a device's stored hardware report.
func (s *InventoryService) Facts(ctx context.Context, tag string) (json.RawMessage, time.Time, bool, error) {
	b, at, ok, err := s.facts.GetFacts(ctx, s.tenant, tag)
	return b, at, ok, err
}
