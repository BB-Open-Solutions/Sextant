package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// evidence.go: the audit-evidence bundle (roadmap stream E). An auditor
// asks "what changed in this period, who made it, who approved it, how
// did it roll out" - and gets one self-contained document assembled from
// the systems of record: git history (every change is a commit), the
// change-request store (authorship and approval), and the fleet document
// (the controls in force). No separate evidence database exists to drift.

// Evidence is the exported bundle for one period.
type Evidence struct {
	// From/To bound the period (inclusive/exclusive).
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// GeneratedAt stamps the export itself.
	GeneratedAt time.Time `json:"generatedAt"`
	// Controls records the assurance configuration at export time.
	Controls EvidenceControls `json:"controls"`
	// Commits is every configuration change in the period, newest first.
	Commits []ports.AuditEntry `json:"commits"`
	// Changes is every change request touched in the period.
	Changes []change.CR `json:"changes"`
	// Rollouts is every staged-rollout promotion in the period, derived
	// from the engine's pin commits (rollout: pin ring ...).
	Rollouts []EvidenceRollout `json:"rollouts"`
}

// EvidenceControls snapshots the audit controls in force.
type EvidenceControls struct {
	RequireFourEyes bool `json:"requireFourEyes"`
}

// EvidenceRollout is one ring promotion reconstructed from its commit.
type EvidenceRollout struct {
	When    time.Time `json:"when"`
	Subject string    `json:"subject"` // the pin commit subject
	Hash    string    `json:"hash"`
}

// EvidenceService assembles bundles.
type EvidenceService struct {
	cfg     *ConfigService
	changes *ChangeService
	clock   ports.Clock
}

// NewEvidenceService wires the exporter. changes may be nil (no CR store).
func NewEvidenceService(cfg *ConfigService, changes *ChangeService, clock ports.Clock) *EvidenceService {
	return &EvidenceService{cfg: cfg, changes: changes, clock: clock}
}

// evidenceLogDepth bounds how far back the export reads git history.
const evidenceLogDepth = 1000

// Export assembles the bundle for [from, to).
func (s *EvidenceService) Export(ctx context.Context, from, to time.Time) (*Evidence, error) {
	if !from.Before(to) {
		return nil, fmt.Errorf("evidence period: from must be before to")
	}
	ev := &Evidence{
		From: from, To: to, GeneratedAt: s.clock.Now(),
		Commits:  []ports.AuditEntry{},
		Changes:  []change.CR{},
		Rollouts: []EvidenceRollout{},
	}
	if asr := s.cfg.Fleet().Assurance; asr != nil {
		ev.Controls.RequireFourEyes = asr.RequireFourEyes
	}

	entries, err := s.cfg.AuditLog(ctx, evidenceLogDepth)
	if err != nil {
		return nil, err
	}
	truncated := len(entries) == evidenceLogDepth
	for _, e := range entries {
		if e.When.Before(from) || !e.When.Before(to) {
			continue
		}
		ev.Commits = append(ev.Commits, e)
		if strings.HasPrefix(e.Subject, "rollout: pin ring") {
			ev.Rollouts = append(ev.Rollouts, EvidenceRollout{
				When: e.When, Subject: e.Subject, Hash: e.Hash,
			})
		}
	}
	// An export that may miss commits must say so, never silently pass.
	if truncated && len(entries) > 0 && entries[len(entries)-1].When.After(from) {
		return nil, fmt.Errorf(
			"period reaches beyond the newest %d commits; narrow the window", evidenceLogDepth)
	}

	if s.changes != nil {
		crs, err := s.changes.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, cr := range crs {
			if cr.Updated.Before(from) || !cr.Created.Before(to) {
				continue
			}
			ev.Changes = append(ev.Changes, cr)
		}
	}
	return ev, nil
}
