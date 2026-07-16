package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// groups.go: the group tree - create, re-parent, IdP mapping, remove.
// Mirrors the API's authorization: creating needs Owner at the parent
// scope; re-parenting and removing move governance, so org Owner.

// groupRow is one tree node, depth-first with indentation level.
type groupRow struct {
	Name           string
	Depth          int
	Parent         string
	IdpGroup       string
	Pin            string
	Devices        int
	CanOwn         bool
	AllowedClasses []string
}

// groupsPage renders the visible tree.
func (s *Server) groupsPage(w http.ResponseWriter, r *http.Request, v view) {
	f := s.svc.Config.Fleet().VisibleTo(v.canView)

	var rows []groupRow
	seen := map[string]bool{}
	add := func(name string, depth int) {
		g := f.Groups[name]
		rows = append(rows, groupRow{
			Name: name, Depth: depth, Parent: g.Parent,
			IdpGroup: g.IdpGroup, Pin: g.Pin,
			Devices:        len(f.GroupDevices(name)),
			CanOwn:         v.roleAt("group:" + name).Meets(identity.Owner),
			AllowedClasses: g.AllowedClasses,
		})
		seen[name] = true
	}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		kids := make([]string, 0)
		for name, g := range f.Groups {
			if g.Parent == parent {
				kids = append(kids, name)
			}
		}
		sort.Strings(kids)
		for _, name := range kids {
			add(name, depth)
			walk(name, depth+1)
		}
	}
	walk("", 0)
	// A scoped viewer may see a subtree whose ancestors are filtered out;
	// those visible orphans render at root level so nothing disappears.
	orphans := make([]string, 0)
	for name := range f.Groups {
		if !seen[name] {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		if seen[name] {
			continue // already rendered under an earlier orphan
		}
		add(name, 0)
		walk(name, 1)
	}

	all := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		all = append(all, g)
	}
	sort.Strings(all)

	data := map[string]any{
		"Title": "Groups", "Nav": "groups",
		"Rows": rows, "AllGroups": all,
		"CanOrgOwn":  v.roleAt("org").Meets(identity.Owner),
		"AllClasses": fleet.Classes,
	}
	// IdP-group picker: offer the real directory groups as a dropdown instead
	// of free text. Best-effort; a slow or absent directory must not break the
	// page (the template falls back to a text field).
	if s.svc.Directory != nil && v.roleAt("org").Meets(identity.Owner) {
		if dgs, err := s.svc.Directory.ListGroups(r.Context(), ""); err == nil {
			names := make([]string, 0, len(dgs))
			for _, g := range dgs {
				names = append(names, g.Name)
			}
			sort.Strings(names)
			data["DirGroups"] = names
		} else {
			s.log.Warn("directory browse failed", "err", err)
		}
	}
	s.render(w, "groups", data, v)
}

// postGroupAdd creates a group under an optional parent.
func (s *Server) postGroupAdd(w http.ResponseWriter, r *http.Request, v view) error {
	name := strings.TrimSpace(r.FormValue("name"))
	parent := r.FormValue("parent")
	scope := "org"
	if parent != "" {
		scope = "group:" + parent
	}
	if err := s.requireWeb(v, scope, identity.Owner); err != nil {
		return err
	}
	g := fleet.Group{Parent: parent, IdpGroup: strings.TrimSpace(r.FormValue("idpGroup"))}
	msg := "groups: add " + name
	if err := s.svc.Config.ApplyStructural(r.Context(), fleet.AddGroup(name, g), msg, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
	return nil
}

// postGroupUpdate re-parents and/or remaps a group (org owner).
func (s *Server) postGroupUpdate(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	name := r.PathValue("name")
	// Allowed-classes guardrail: a checkbox set (none = unrestricted). This
	// only gates future membership, changing no device's resolved config, so
	// it needs no host eval - apply it gated like the other group edits and
	// return. Kept as its own branch so it never mixes with a re-parent.
	if r.FormValue("setallowed") == "1" {
		classes := r.Form["allowedClass"]
		msg := "groups: set allowed classes on " + name
		if err := s.applyGated(r, v, fleet.SetGroupAllowedClasses(name, classes), msg); err != nil {
			return err
		}
		http.Redirect(w, r, "/groups", http.StatusSeeOther)
		return nil
	}
	var parent, idp *string
	if _, has := r.Form["parent"]; has || r.FormValue("parent") != "" || r.FormValue("reparent") == "1" {
		p := r.FormValue("parent")
		parent = &p
	}
	if val := strings.TrimSpace(r.FormValue("idpGroup")); r.FormValue("setidp") == "1" {
		idp = &val
	}
	if parent == nil && idp == nil {
		return fmt.Errorf("nothing to update")
	}
	msg := "groups: update " + name
	// A re-parent changes inheritance for exactly this group's subtree: those
	// devices are the blast radius, so the gate needs only them - not a
	// whole-fleet evaluation. The validation may take tens of seconds (a real
	// nix eval): fast answers inline, slow detaches and notifies.
	hosts := app.AffectedHosts(s.svc.Config.Fleet(), "group:"+name)
	author := webAuthor(v)
	if err := s.runGated(r, v, msg, func(ctx context.Context) error {
		return s.svc.Config.Apply(ctx, fleet.UpdateGroup(name, parent, idp), msg, author, hosts...)
	}); err != nil {
		return err
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
	return nil
}

// postGroupUnpin releases a group's rollout pin so it follows HEAD again.
// Pins are set by the rollout engine on promotion and deliberately survive a
// cancel (config is truth; the operator decides) - this IS that decision.
// Gated and scoped to the group's subtree: releasing a pin changes what its
// devices will build next.
func (s *Server) postGroupUnpin(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	name := r.PathValue("name")
	hosts := app.AffectedHosts(s.svc.Config.Fleet(), "group:"+name)
	msg := fmt.Sprintf("groups: release pin on %s (follow HEAD)", name)
	if err := s.applyGated(r, v, fleet.SetGroupPin(name, ""), msg, hosts...); err != nil {
		return err
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
	return nil
}

// postGroupRemove deletes an empty leaf group (org owner); the domain
// blocks anything still referenced.
func (s *Server) postGroupRemove(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	name := r.PathValue("name")
	msg := "groups: remove " + name
	if err := s.svc.Config.ApplyStructural(r.Context(), fleet.RemoveGroup(name), msg, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/groups", http.StatusSeeOther)
	return nil
}
