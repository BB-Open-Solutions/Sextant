package web

import (
	"fmt"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
)

// pipeline.go: the deployment pipeline as one kanban board. It shows the whole
// process end to end - a change request flows Draft -> Building -> Ready, and
// once merged its revision rolls out through the ordered waves (test waves
// first, then phases). Read-only board (org Viewer); the actions live on the
// changes and rollout pages the cards link to.

// waveCol is one wave column on the board.
type waveCol struct {
	Index    int
	Label    string
	Group    string
	Status   string // Planned | Deploying | Soaking | Awaiting approval | Complete | Queued
	Active   bool
	Await    bool
	Manual   bool
	OnTarget int
	Total    int
	BarClass string
}

// barBucket rounds a fraction to the nearest 5% for a CSP-safe width class.
func barBucket(onTarget, total int) string {
	bucket := 0
	if total > 0 {
		pct := (onTarget*100 + total/2) / total
		bucket = ((pct + 2) / 5) * 5
		if bucket > 100 {
			bucket = 100
		}
	}
	return fmt.Sprintf("bar-w-%d", bucket)
}

func (s *Server) pipelinePage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	crs, listErr := s.svc.Changes.List(r.Context())
	var draft, building, ready []change.CR
	for _, cr := range crs {
		switch cr.Status {
		case change.Draft, change.Failed:
			draft = append(draft, cr)
		case change.Building:
			building = append(building, cr)
		case change.Ready:
			ready = append(ready, cr)
		}
	}

	f := s.svc.Config.Fleet()
	st, ringStatus, _ := s.svc.Rollouts.Status(r.Context())
	active := st != nil && st.Status == rollout.Active

	var waves []waveCol
	if f.Rollout != nil {
		for i, rr := range f.Rollout.Rings {
			col := waveCol{Index: i, Label: rr.Label(), Group: rr.Group, Manual: rr.RequireApproval}
			if i < len(ringStatus) {
				col.OnTarget, col.Total = ringStatus[i].OnTarget, ringStatus[i].Total
			}
			col.BarClass = barBucket(col.OnTarget, col.Total)
			switch {
			case !active:
				col.Status = "Planned"
			case i < st.Ring:
				col.Status = "Complete"
			case i == st.Ring:
				col.Active = true
				converged := col.Total > 0 && col.OnTarget >= col.Total
				_, approved := st.ApprovedAt[i]
				switch {
				case converged && rr.RequireApproval && !approved:
					col.Status, col.Await = "Awaiting approval", true
				case converged:
					col.Status = "Soaking"
				default:
					col.Status = "Deploying"
				}
			default:
				col.Status = "Queued"
			}
			waves = append(waves, col)
		}
	}

	data := map[string]any{
		"Title": "Pipeline", "Nav": "pipeline",
		"Draft": draft, "Building": building, "Ready": ready,
		"Waves":   waves,
		"State":   st,
		"Active":  active,
		"CanEdit": v.roleAt("org").Meets(identity.Editor),
		"CanOwn":  v.roleAt("org").Meets(identity.Owner),
	}
	if listErr != nil {
		data["Error"] = listErr.Error()
	}
	s.render(w, "pipeline", data, v)
}
