package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

func (s *Server) changesPage(w http.ResponseWriter, r *http.Request, v view) {
	// Diffs expose every scope: org-wide read required.
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	crs, err := s.changeList(r.Context())
	f := s.svc.Config.Fleet()
	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	data := map[string]any{"Title": "Changes", "Nav": "updates", "Changes": crs,
		"Groups":  groups,
		"CanEdit": v.roleAt("org").Meets(identity.Editor)}
	if err != nil {
		data["Error"] = err.Error()
	}
	s.render(w, "changes", data, v)
}

// diffPage shows an approver what a change would apply.
func (s *Server) diffPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	cr, ok, err := s.svc.Changes.Get(r.Context(), id)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	diff, err := s.svc.Changes.Diff(r.Context(), id)
	data := map[string]any{"Title": "Diff " + id, "Nav": "updates",
		"ID": id, "Change": cr, "Diff": diff}
	if err != nil {
		data["Error"] = err.Error()
	}
	s.render(w, "diff", data, v)
}

func (s *Server) postChange(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	_, err := s.svc.Changes.Open(r.Context(), r.FormValue("id"), r.FormValue("title"), webAuthor(v))
	if err != nil {
		return err
	}
	http.Redirect(w, r, "/updates", http.StatusSeeOther)
	return nil
}

func (s *Server) postChangeSubmit(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	// Submitting kicks the change's build/test gate - minutes, not
	// milliseconds. Grace-window: the board shows the card moving, the
	// outcome lands as a notification.
	id := r.PathValue("id")
	if err := s.runGated(r, v, "change "+id+" submitted", func(ctx context.Context) error {
		_, err := s.svc.Changes.Submit(ctx, id)
		return err
	}); err != nil {
		return err
	}
	http.Redirect(w, r, "/updates", http.StatusSeeOther)
	return nil
}

// postChangeMerge approves a change - or, without confirmed=1, renders a
// confirmation summary instead (fix B): "Approve" both merges AND
// immediately starts delivery (the "saving IS rolling out" model), so a
// single unguarded click is not enough for what it actually does. The
// four-eyes/author segregation-of-duties check stays exactly where it was -
// inside Changes.Merge, run only on the confirmed POST that actually merges.
func (s *Server) postChangeMerge(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	id := r.PathValue("id")
	if r.FormValue("confirmed") != "1" {
		cr, ok, err := s.svc.Changes.Get(r.Context(), id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("change %q not found", id)
		}
		f := s.svc.Config.Fleet()
		manualOnly := f.Assurance != nil && f.Assurance.ManualRolloutOnly
		var groups []string
		nothingDeployable := false
		if !cr.WholeFleet {
			groups = app.DeliveryGroups(f, cr.Hosts)
			// Mirrors WireAutoRollout: a recorded blast radius that maps to no
			// device group means auto-rollout will not start a run at all -
			// distinct from WholeFleet, so the confirmation must not claim
			// "whole fleet" for a merge that actually delivers to nobody.
			nothingDeployable = len(groups) == 0
		}
		data := map[string]any{
			"Title": "Confirm merge", "Nav": "updates",
			"ID": id, "ChangeTitle": cr.Title,
			"ManualRolloutOnly": manualOnly,
			"ScopeFleet":        cr.WholeFleet,
			"NothingDeployable": nothingDeployable,
			"Groups":            groups,
			"TestGated":         f.Rollout.HasTestGate(),
		}
		s.render(w, "merge_confirm", data, v)
		return nil
	}
	// The merge re-validates the merged result through the nix gate before
	// committing - same grace-window treatment as any gated write.
	author := webAuthor(v)
	if err := s.runGated(r, v, "change "+id+" merged", func(ctx context.Context) error {
		_, err := s.svc.Changes.Merge(ctx, id, author)
		return err
	}); err != nil {
		return err
	}
	http.Redirect(w, r, "/updates", http.StatusSeeOther)
	return nil
}

func (s *Server) postChangeAbandon(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	if _, err := s.svc.Changes.Abandon(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	http.Redirect(w, r, "/updates", http.StatusSeeOther)
	return nil
}

// changeList is the nil-safe read of the change queue: a console wired
// without a ChangeService (simulators, minimal tests) renders an empty
// board instead of panicking on the nil service.
func (s *Server) changeList(ctx context.Context) ([]change.CR, error) {
	if s.svc.Changes == nil {
		return nil, nil
	}
	return s.svc.Changes.List(ctx)
}
