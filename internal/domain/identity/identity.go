// Package identity is the pure authorization domain: users (from the IdP),
// roles, and per-scope role bindings. Bindings are config-as-data on the
// fleet document, so access changes ride the gated write transaction and
// land as audited git commits. Authentication (OIDC) is an adapter.
package identity

import (
	"fmt"
	"strings"
)

// Role orders permissions: viewer < editor < owner.
type Role int

const (
	// None grants nothing.
	None Role = iota
	// Viewer may read the scope.
	Viewer
	// Editor may change settings within the scope.
	Editor
	// Owner administers the scope: access, policies, merges, rollouts.
	Owner
)

// ParseRole converts the wire form.
func ParseRole(s string) (Role, error) {
	switch s {
	case "viewer":
		return Viewer, nil
	case "editor":
		return Editor, nil
	case "owner":
		return Owner, nil
	}
	return None, fmt.Errorf("unknown role %q (viewer|editor|owner)", s)
}

// String is the wire form.
func (r Role) String() string {
	switch r {
	case Viewer:
		return "viewer"
	case Editor:
		return "editor"
	case Owner:
		return "owner"
	}
	return "none"
}

// Meets reports whether r grants at least required.
func (r Role) Meets(required Role) bool { return r >= required }

// User is the authenticated principal: identity claims only, no verdicts.
// Authorization is derived per request from bindings, never stored.
type User struct {
	Subject string
	Name    string
	Email   string
	Groups  []string
	// Service marks a non-human principal (API token, rollout engine).
	Service bool
}

// Binding grants a role at a scope to everyone in an IdP group.
// Scope is "org" or "group:<name>"; a binding at org covers everything,
// a binding at a group covers its subtree (subgroups and their devices).
type Binding struct {
	Group string `json:"group"`
	Role  string `json:"role"`
	Scope string `json:"scope"`
}

// Validate rejects malformed bindings at write time.
func (b Binding) Validate() error {
	if b.Group == "" {
		return fmt.Errorf("binding needs an IdP group")
	}
	if _, err := ParseRole(b.Role); err != nil {
		return err
	}
	if b.Scope != "org" && !strings.HasPrefix(b.Scope, "group:") {
		return fmt.Errorf("binding scope %q: must be org or group:<name>", b.Scope)
	}
	return nil
}

// Resolver computes a user's effective role from the access bindings and the
// scope chain that governs a target scope ref, most general first. Group
// ancestry comes from the caller (the fleet document), keeping this package
// free of fleet imports.
//
//	org               -> [org]
//	group:<g>         -> [org, group:root, ..., group:g]
//	device:<tag>      -> [org, ...ancestry of each device group...]
type Resolver struct {
	// Ancestry returns a group's chain root..g (fleet.GroupAncestry).
	Ancestry func(group string) []string
	// DeviceGroups returns a device's direct groups.
	DeviceGroups func(tag string) []string
	// Bindings is the access list from the fleet document.
	Bindings []Binding
	// BaselineViewer, BaselineEditor, BaselineOwner are IdP groups granted
	// org-wide roles by server configuration (the PoC model), merged with
	// document bindings so simple deployments need no access list.
	BaselineViewer []string
	BaselineEditor []string
	BaselineOwner  []string
}

// RoleAt returns the highest role the user holds at the given scope ref
// ("org", "group:<g>" or "device:<tag>"). A binding anywhere on the scope's
// governing chain applies. Service principals are owners everywhere: they
// authenticated with the API credential.
func (rv Resolver) RoleAt(u User, ref string) Role {
	if u.Service {
		return Owner
	}
	member := make(map[string]bool, len(u.Groups))
	for _, g := range u.Groups {
		member[g] = true
	}

	best := None
	consider := func(role Role) {
		if role > best {
			best = role
		}
	}
	// Server-config baselines are org-wide.
	if intersects(member, rv.BaselineOwner) {
		consider(Owner)
	}
	if intersects(member, rv.BaselineEditor) {
		consider(Editor)
	}
	if intersects(member, rv.BaselineViewer) {
		consider(Viewer)
	}

	govern := rv.governingScopes(ref)
	for _, b := range rv.Bindings {
		if !member[b.Group] || !govern[b.Scope] {
			continue
		}
		if r, err := ParseRole(b.Role); err == nil {
			consider(r)
		}
	}
	return best
}

// CanViewAnything reports whether the user holds any role anywhere - the
// login-time gate (no role at all = no console access).
func (rv Resolver) CanViewAnything(u User) bool {
	if u.Service {
		return true
	}
	if rv.RoleAt(u, "org") >= Viewer {
		return true
	}
	member := make(map[string]bool, len(u.Groups))
	for _, g := range u.Groups {
		member[g] = true
	}
	for _, b := range rv.Bindings {
		if member[b.Group] {
			if r, err := ParseRole(b.Role); err == nil && r >= Viewer {
				return true
			}
		}
	}
	return false
}

// governingScopes expands a target ref into the set of scope refs whose
// bindings govern it.
func (rv Resolver) governingScopes(ref string) map[string]bool {
	out := map[string]bool{"org": true}
	addGroup := func(g string) {
		chain := []string{g}
		if rv.Ancestry != nil {
			chain = rv.Ancestry(g)
		}
		for _, anc := range chain {
			out["group:"+anc] = true
		}
	}
	switch {
	case ref == "org":
	case strings.HasPrefix(ref, "group:"):
		addGroup(strings.TrimPrefix(ref, "group:"))
	case strings.HasPrefix(ref, "device:"):
		if rv.DeviceGroups != nil {
			for _, g := range rv.DeviceGroups(strings.TrimPrefix(ref, "device:")) {
				addGroup(g)
			}
		}
	}
	return out
}

func intersects(member map[string]bool, groups []string) bool {
	for _, g := range groups {
		if member[g] {
			return true
		}
	}
	return false
}
