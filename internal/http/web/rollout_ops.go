package web

// rollout_ops.go: the console's rollout surface - the plan/status page and the
// start/tick/approve/cancel actions. Split out of pages.go to keep each file
// cohesive.

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

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
	// Name the stragglers of the active wave: the devices behind the
	// percentages, with a reason each. Active wave only - history is the
	// audit trail's job, not this page's.
	if st != nil {
		for i := range waves {
			if waves[i].Active {
				waves[i].Stragglers = s.svc.Rollouts.Stragglers(r.Context(), waves[i].Groups, st.Target)
			}
		}
	}
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
	http.Redirect(w, r, "/updates/rollout", http.StatusSeeOther)
	return nil
}

func (s *Server) postRolloutTick(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, _, err := s.svc.Rollouts.Tick(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/updates/rollout", http.StatusSeeOther)
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
	http.Redirect(w, r, "/updates/rollout", http.StatusSeeOther)
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
	http.Redirect(w, r, "/updates/rollout", http.StatusSeeOther)
	return nil
}

func (s *Server) postRolloutResume(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Rollouts.Resume(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/updates/rollout", http.StatusSeeOther)
	return nil
}

func (s *Server) postRolloutCancel(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Rollouts.Cancel(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/updates/rollout", http.StatusSeeOther)
	return nil
}

// orgUpdatesPage is the set-and-forget home of update policy (org tile):
// the simple form (pick the test group, the rest derives), governance, and
// the hand-authored ladder tucked under "advanced". The Updates overview
// and the rollout monitor stay purely operational.
func (s *Server) orgUpdatesPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f := s.svc.Config.Fleet()
	data := map[string]any{
		"Title": "Update policy", "Nav": "org",
		"CanOwn": true,
	}
	for k, val := range rolloutPlanData(f) {
		data[k] = val
	}
	if f.Rollout != nil && len(f.Rollout.Rings) > 0 {
		data["TestGroup"] = f.Rollout.Rings[0].Group
		rows := planWaveRows(f)
		data["PlanWaves"] = rows
		shares := make([]string, 0, len(rows))
		for i, row := range rows {
			if i == 0 {
				continue // the test wave is outside the percentage ladder
			}
			shares = append(shares, strconv.Itoa(row.Share))
		}
		data["Percents"] = strings.Join(shares, ", ")
	}
	if f.Assurance != nil {
		data["RequireFourEyes"] = f.Assurance.RequireFourEyes
		data["RequireChangeRequest"] = f.Assurance.RequireChangeRequest
		data["RequireTestWave"] = f.Assurance.RequireTestWave
	}
	s.render(w, "org_updates", data, v)
}

// postUpdatesPolicy derives the whole wave plan from one choice: the test
// group. Ring 0 = that group, manual approval, an hour of soak; every other
// group becomes a wave (smallest first - blast radius grows with confidence)
// with the opinionated defaults (30 min soak, 95% threshold). The ladder
// under "advanced" overwrites this for the rare hand-authored plan.
func (s *Server) postUpdatesPolicy(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	test := strings.TrimSpace(r.FormValue("testgroup"))
	f := s.svc.Config.Fleet()
	if _, ok := f.Groups[test]; !ok {
		return fmt.Errorf("unknown test group %q", test)
	}
	percents, err := parsePercents(r.FormValue("percents"))
	if err != nil {
		return err
	}
	plan := derivePlan(f, test, percents)
	if err := s.applyGated(r, v, fleet.SetRolloutPlan(plan), "rollout: derive plan from test group "+test); err != nil {
		return err
	}
	http.Redirect(w, r, "/org/updates", http.StatusSeeOther)
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
			planGroups[i] = strings.Join(ring.GroupList(), ", ")
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

// parsePercents reads the ladder shape ("10, 30, 60"): the share of the
// fleet each wave after the test wave receives. Empty means one 100% wave.
// Values are proportions - they need not sum to exactly 100.
func parsePercents(raw string) ([]int, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '-' || r == '/'
	})
	if len(fields) == 0 {
		return []int{100}, nil
	}
	if len(fields) > 10 {
		return nil, fmt.Errorf("at most 10 waves")
	}
	out := make([]int, 0, len(fields))
	for _, fld := range fields {
		n, err := strconv.Atoi(fld)
		if err != nil || n < 1 || n > 100 {
			return nil, fmt.Errorf("wave percentages must be whole numbers 1-100 (got %q)", fld)
		}
		out = append(out, n)
	}
	return out, nil
}

// derivePlan turns the org's two choices (test group + ladder shape) into a
// full wave plan: the test wave first (manual sign-off, structural per
// delivery-process 4), then percentage waves. Groups are the engine's release
// unit, so each percentage wave takes whole groups, smallest first, until the
// wave's cumulative share of the fleet is reached - actual shares therefore
// approximate the requested ones at group granularity.
func derivePlan(f *fleet.Fleet, test string, percents []int) *fleet.RolloutPolicy {
	plan := &fleet.RolloutPolicy{Rings: []fleet.RolloutRing{{
		Group: test, Name: "Testgroep", RequireApproval: true, SoakMinutes: 60,
	}}}
	type gs struct {
		name string
		n    int
	}
	var rest []gs
	total := 0
	for g := range f.Groups {
		if g == test {
			continue
		}
		n := len(f.ActiveGroupDevices(g))
		rest = append(rest, gs{g, n})
		total += n
	}
	sort.Slice(rest, func(i, j int) bool {
		if rest[i].n != rest[j].n {
			return rest[i].n < rest[j].n
		}
		return rest[i].name < rest[j].name
	})
	sum := 0
	for _, p := range percents {
		sum += p
	}
	covered, gi := 0, 0
	for wi, p := range percents {
		if gi >= len(rest) {
			break
		}
		// Cumulative device target for this wave, in whole devices.
		cumShare := 0
		for _, q := range percents[:wi+1] {
			cumShare += q
		}
		target := total * cumShare / sum
		ring := fleet.RolloutRing{Name: fmt.Sprintf("Wave %d · %d%%", wi+1, p), SoakMinutes: 30}
		for gi < len(rest) && (covered < target || wi == len(percents)-1) {
			ring.Groups = append(ring.Groups, rest[gi].name)
			covered += rest[gi].n
			gi++
		}
		if len(ring.Groups) == 0 {
			continue // fleet too small for this many waves
		}
		plan.Rings = append(plan.Rings, ring)
	}
	return plan
}

// planWaveRows renders the configured plan for the policy page preview: per
// wave its label, groups and device count plus the wave's real share.
type planWaveRow struct {
	Label   string
	Groups  string
	Devices int
	Share   int // percent of the post-test fleet
	Manual  bool
}

func planWaveRows(f *fleet.Fleet) []planWaveRow {
	if f.Rollout == nil {
		return nil
	}
	total := 0
	counts := make([]int, len(f.Rollout.Rings))
	for i, ring := range f.Rollout.Rings {
		for _, g := range ring.GroupList() {
			counts[i] += len(f.ActiveGroupDevices(g))
		}
		if i > 0 { // the test wave is outside the percentage fleet
			total += counts[i]
		}
	}
	rows := make([]planWaveRow, 0, len(f.Rollout.Rings))
	for i, ring := range f.Rollout.Rings {
		row := planWaveRow{
			Label: ring.Label(), Groups: strings.Join(ring.GroupList(), ", "),
			Devices: counts[i], Manual: ring.RequireApproval,
		}
		if i > 0 && total > 0 {
			row.Share = counts[i] * 100 / total
		}
		rows = append(rows, row)
	}
	return rows
}
