package web

// rollout_ops.go: the console's rollout surface - the plan/status page and the
// start/tick/approve/cancel actions. Split out of pages.go to keep each file
// cohesive.

import (
	"fmt"
	"net/http"
	"sort"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
)

func (s *Server) rolloutPage(w http.ResponseWriter, r *http.Request, v view) {
	// The plan enumerates rings and groups: org-wide read required.
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f := s.svc.Config.Fleet()
	data := map[string]any{"Title": "Rollout", "Nav": "rollout",
		"CanOwn":   v.roleAt("org").Meets(identity.Owner),
		"HasRings": f.Rollout != nil && len(f.Rollout.Rings) > 0,
		// A required-but-missing test wave: the start form then offers the
		// owner an explicit skip (governance: instelbare test-flow).
		"NeedsTestWaveSkip": f.Assurance != nil && f.Assurance.RequireTestWave && !f.Rollout.HasTestGate(),
	}
	for k, val := range rolloutPlanData(f) {
		data[k] = val
	}

	// Plan ladder: the ordered waves with each wave's device count, so an
	// operator sees the progression at a glance (e.g. Canary 1 -> Pilot 10 ->
	// Phase 1 100 -> ...). The wave size is its group's active device count;
	// tune it by sizing groups and ordering waves, and refine per wave with
	// soak, health and an approval gate.
	if f.Rollout != nil && len(f.Rollout.Rings) > 0 {
		type ladderStep struct {
			N             int
			Name, Group   string
			Devices       int
			Soak, Healthy int
			Approval      bool
			Max           int // count-cap per cohort; 0 = whole group at once
		}
		ladder := make([]ladderStep, 0, len(f.Rollout.Rings))
		for i, rr := range f.Rollout.Rings {
			ladder = append(ladder, ladderStep{
				N: i + 1, Name: rr.Label(), Group: rr.Group,
				Devices: len(f.ActiveGroupDevices(rr.Group)),
				Soak:    rr.SoakMinutes, Healthy: rr.MinHealthyPercent, Approval: rr.RequireApproval,
				Max: rr.MaxDevices,
			})
		}
		data["PlanLadder"] = ladder
	}
	st, ringStatus, err := s.svc.Rollouts.Status(r.Context())
	if err != nil {
		data["Error"] = err.Error()
	}
	if st != nil {
		data["State"] = st
		type ringRow struct {
			Ring   fleet.RolloutRing
			Status rollout.RingStatus
			Label  string // observed phase: Complete | Deploying | Soaking | Awaiting approval | Queued
			Active bool
			Await  bool // active manual gate: soaked, needs operator sign-off
		}
		var rows []ringRow
		total, onTarget := 0, 0
		if f.Rollout != nil {
			for i, rr := range f.Rollout.Rings {
				row := ringRow{Ring: rr}
				if i < len(ringStatus) {
					row.Status = ringStatus[i]
				}
				total += row.Status.Total
				onTarget += row.Status.OnTarget
				switch {
				case st.Status != rollout.Active:
					row.Label = "Queued"
				case i < st.Ring:
					row.Label = "Complete"
				case i == st.Ring:
					row.Active = true
					converged := row.Status.Total > 0 && row.Status.OnTarget >= row.Status.Total
					_, approved := st.ApprovedAt[i]
					_, building := st.BuildRequestedAt[i]
					_, promoted := st.PromotedAt[i]
					switch {
					// Build-before-promote: the wave's release is being
					// realised into the binary cache; promotion follows.
					case building && !promoted:
						row.Label = "Building"
					case converged && rr.RequireApproval && !approved:
						row.Label = "Awaiting approval"
						row.Await = true
					case converged:
						row.Label = "Soaking"
					default:
						row.Label = "Deploying"
					}
				default:
					row.Label = "Queued"
				}
				rows = append(rows, row)
			}
		}
		data["Rings"] = rows
		// Overall convergence bar: devices on the target across all rings,
		// bucketed to the nearest 5% so the width is a static CSP-safe class
		// (bar-w-0 .. bar-w-100) rather than an inline style.
		data["TotalDevices"], data["TotalOnTarget"] = total, onTarget
		bucket := 0
		if total > 0 {
			pct := (onTarget*100 + total/2) / total
			bucket = ((pct + 2) / 5) * 5
			if bucket > 100 {
				bucket = 100
			}
		}
		data["BarClass"] = fmt.Sprintf("bar-w-%d", bucket)
	}
	s.render(w, "rollout", data, v)
}

func (s *Server) postRolloutStart(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	// Governance: when a test wave is required, refuse a plan without a gated
	// test wave unless this owner explicitly skips it ("hoeft niet").
	f := s.svc.Config.Fleet()
	if f.Assurance != nil && f.Assurance.RequireTestWave && !f.Rollout.HasTestGate() {
		if r.FormValue("skipTestWave") == "" {
			return fmt.Errorf("a gated test wave is required: add a wave with manual approval on the plan, or check 'skip test wave' to proceed without one")
		}
		s.log.Warn("rollout started without the required test wave (owner skip)",
			"by", v.User.Subject, "target", r.FormValue("target"))
	}
	if _, err := s.svc.Rollouts.Start(r.Context(), r.FormValue("target"), webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}

func (s *Server) postRolloutTick(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, _, err := s.svc.Rollouts.Tick(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}

// postRolloutApprove signs off the current manual-gate wave, then ticks so the
// pipeline promotes the next wave immediately.
func (s *Server) postRolloutApprove(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Rollouts.Approve(r.Context()); err != nil {
		return err
	}
	if _, _, err := s.svc.Rollouts.Tick(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}

// postRolloutPause freezes the active run; postRolloutResume lifts it. The
// operator's stop-the-world control (delivery-process §7.6) - unlike cancel
// it keeps the run and its progress.
func (s *Server) postRolloutPause(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Rollouts.Pause(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}

func (s *Server) postRolloutResume(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Rollouts.Resume(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}

func (s *Server) postRolloutCancel(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Rollouts.Cancel(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}

// rolloutPlanData builds the ring-plan editor state (every existing ring plus
// two blank rows) and the group list. Shared by the rollout page and the
// pipeline board so both edit the plan identically. Sizing to the plan (not a
// fixed cap) means a large plan can never render truncated and then lose rings
// on an unrelated save.
func rolloutPlanData(f *fleet.Fleet) map[string]any {
	// One blank row after the configured waves: the editor grows a wave at a
	// time instead of presenting a wall of "(unused wave)" blocks.
	ringRows := 1
	if f.Rollout != nil {
		ringRows += len(f.Rollout.Rings)
	}
	planGroups := make([]string, ringRows)
	planNames := make([]string, ringRows)
	planSoaks := make([]string, ringRows)
	planHealthy := make([]string, ringRows)
	planApproval := make([]bool, ringRows)
	planMax := make([]string, ringRows)
	if f.Rollout != nil {
		for i, ring := range f.Rollout.Rings {
			planGroups[i] = ring.Group
			planNames[i] = ring.Name
			planApproval[i] = ring.RequireApproval
			if ring.SoakMinutes > 0 {
				planSoaks[i] = fmt.Sprint(ring.SoakMinutes)
			}
			if ring.MinHealthyPercent > 0 {
				planHealthy[i] = fmt.Sprint(ring.MinHealthyPercent)
			}
			if ring.MaxDevices > 0 {
				planMax[i] = fmt.Sprint(ring.MaxDevices)
			}
		}
	}
	rows := make([]int, ringRows)
	for i := range rows {
		rows[i] = i
	}
	allGroups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		allGroups = append(allGroups, g)
	}
	sort.Strings(allGroups)
	return map[string]any{
		"RingRows": rows, "AllGroups": allGroups,
		"PlanGroups": planGroups, "PlanSoaks": planSoaks, "PlanHealthy": planHealthy,
		"PlanNames": planNames, "PlanApproval": planApproval, "PlanMax": planMax,
	}
}
