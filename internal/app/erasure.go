package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// erasure.go: removing one person's data on request (GDPR art. 17).
//
// THE HARD PART IS NOT THE DELETE. It is being honest about what cannot be
// deleted, because an erasure that reports success while the audit trail
// still names the person is worse than no feature at all - somebody tells a
// data subject their data is gone, on the strength of this.
//
// So this service never says "erased". It returns a report with two halves:
// what was removed, and what remains and why. The caller renders both.
//
// TWO IDENTIFIERS. A person is an OIDC subject in the console and an OS
// username on their device, and the two do not have to match (measured
// 2026-08-07: a numeric IdP id versus a login name). Both travel together
// and the report says which one matched what, so an operator can see that
// one of them found nothing and go and ask why rather than assume.

// ErasureReport is what an erasure did and did not do.
type ErasureReport struct {
	Subject  string
	Username string
	// Removed is what went from the observed plane.
	Removed ports.PersonalDataCounts
	// DevicesCleared lists device tags whose AssignedUser named this person
	// and was cleared.
	DevicesCleared []string
	// Remaining is what still names them, in plain language. Never empty:
	// the git history always does.
	Remaining []string
	// DryRun reports whether anything was actually removed.
	DryRun bool
}

// ErasureService finds and removes a person's data.
type ErasureService struct {
	store  ports.ErasureStore
	cfg    *ConfigService
	tenant string
	log    *slog.Logger
}

// NewErasureService wires the service. cfg may be nil, which leaves the
// fleet document out of scope (and says so in the report).
func NewErasureService(store ports.ErasureStore, cfg *ConfigService, tenant string, log *slog.Logger) *ErasureService {
	return &ErasureService{store: store, cfg: cfg, tenant: tenant, log: log}
}

// Preview reports what an erasure would remove, without removing it.
//
// This exists because the operation is irreversible and matches on
// identifiers a human typed. The same shape as arming a wipe: the dangerous
// thing is reachable, but reaching it is two acts.
func (s *ErasureService) Preview(ctx context.Context, subject, username string) (ErasureReport, error) {
	return s.run(ctx, subject, username, true)
}

// Erase removes the person's data and reports what remains.
func (s *ErasureService) Erase(ctx context.Context, subject, username string, a ports.Author) (ErasureReport, error) {
	rep, err := s.run(ctx, subject, username, false)
	if err != nil {
		return rep, err
	}
	// Logged deliberately, and this is the one place where logging the
	// identifiers is right: an erasure IS an accountability event, and a
	// controller has to be able to show it happened. What is logged is who
	// asked and what was removed - never the content that was removed.
	s.logger().Info("personal data erased on request",
		"subject", subject, "username", username, "by", a.Name,
		"removed", rep.Removed.Total(), "devicesCleared", len(rep.DevicesCleared))
	return rep, nil
}

func (s *ErasureService) run(ctx context.Context, subject, username string, dry bool) (ErasureReport, error) {
	subject, username = strings.TrimSpace(subject), strings.TrimSpace(username)
	if subject == "" && username == "" {
		return ErasureReport{}, fmt.Errorf("erasure needs at least one identifier: the console subject, the device username, or both")
	}
	rep := ErasureReport{Subject: subject, Username: username, DryRun: dry}

	var err error
	if dry {
		rep.Removed, err = s.store.CountPersonalData(ctx, s.tenant, subject, username)
	} else {
		rep.Removed, err = s.store.ErasePersonalData(ctx, s.tenant, subject, username)
	}
	if err != nil {
		return rep, fmt.Errorf("erase personal data: %w", err)
	}

	// The fleet document: AssignedUser is a person's name on a device.
	// Matched against BOTH identifiers, because whoever filled it in used
	// whichever name they had to hand.
	if s.cfg != nil {
		for tag, d := range s.cfg.Fleet().Devices {
			u := strings.TrimSpace(d.AssignedUser)
			if u == "" {
				continue
			}
			if !strings.EqualFold(u, subject) && !strings.EqualFold(u, username) {
				continue
			}
			rep.DevicesCleared = append(rep.DevicesCleared, tag)
		}
		if !dry && len(rep.DevicesCleared) > 0 {
			for _, tag := range rep.DevicesCleared {
				msg := "devices: clear assigned user on " + tag + " (erasure request)"
				empty := ""
				if err := s.cfg.Apply(ctx, fleet.UpdateDevice(tag, fleet.DevicePatch{AssignedUser: &empty}),
					msg, ports.Author{Name: "erasure", Email: "erasure@sextant"}, tag); err != nil {
					return rep, fmt.Errorf("clear assigned user on %s: %w", tag, err)
				}
			}
		}
	}

	rep.Remaining = s.remaining(subject, username, rep)
	return rep, nil
}

// remaining is the half of this feature that matters. It is never empty.
func (s *ErasureService) remaining(subject, username string, rep ErasureReport) []string {
	var out []string

	// Always. Clearing AssignedUser writes a NEW commit; the old value stays
	// in the history, which is what an append-only audit trail means.
	out = append(out, "The git history still contains this person's name in "+
		"past commits and past versions of the fleet document. That history is "+
		"the audit trail, it is append-only by design and required by BIO, and "+
		"it cannot be edited without destroying the evidence value of every "+
		"other entry. Rewriting it is a decision for the controller, not an "+
		"operation this console offers.")

	if rep.Removed.ElevationDecided > 0 {
		out = append(out, fmt.Sprintf(
			"%d elevation request(s) that this person DECIDED for somebody else "+
				"were left in place. They are the other person's record of who "+
				"approved their access; erasing them on this request would destroy "+
				"a different data subject's evidence.", rep.Removed.ElevationDecided))
	}
	if subject == "" {
		out = append(out, "No console subject was given, so nothing in the console "+
			"(cached identity, preferences, notifications) was searched. If this "+
			"person ever logged in, their data is still there.")
	}
	if username == "" {
		out = append(out, "No device username was given, so elevation requests were "+
			"not searched. Those carry free text the person wrote themselves.")
	}
	if s.cfg == nil {
		out = append(out, "The fleet document was not searched (no config plane wired), "+
			"so an assigned-user field may still name this person.")
	}
	// Diagnostics bundles are not addressed by name, so they cannot be found
	// by identifier - but they can contain the person's data, and the reader
	// has to know that.
	out = append(out, "Diagnostics bundles are stored per DEVICE, not per person, "+
		"so they cannot be found by name. A bundle taken from this person's "+
		"machine may contain their data and will expire on its own 14-day "+
		"window; clear it sooner from the device page if that matters.")
	return out
}

func (s *ErasureService) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}
