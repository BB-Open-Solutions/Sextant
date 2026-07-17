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

func (s *Server) rolloutMonitorPage(w http.ResponseWriter, r *http.Request, v view) {
	// Pure status/monitoring of the running (or last) rollout - the second
	// screen an operator keeps open during an update. Configuration lives on
	// /updates; this page only watches and steers (approve/pause/resume/stop).
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f := s.svc.Config.Fleet()
	st, ringStatus, _ := s.svc.Rollouts.Status(r.Context())
	active := st != nil && st.Status == rollout.Active
	paused := st != nil && st.Status == rollout.Paused
	waves := waveCols(f, st, ringStatus, active)
	data := map[string]any{
		"Title": "Rollout", "Nav": "updates",
		"Waves":   waves,
		"State":   st,
		"Active":  active,
		"Paused":  paused,
		"HasPlan": f.Rollout != nil && len(f.Rollout.Rings) > 0,
		"CanOwn":  v.roleAt("org").Meets(identity.Owner),
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
