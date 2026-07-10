package fleet

import (
	"fmt"
	"slices"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// access.go: mutations for the access list (per-scope role bindings).

// Grant adds a role binding for an IdP group at a scope. Duplicate
// (group, scope) pairs are replaced, so a grant is also a role change.
func Grant(b AccessBinding) Mutation {
	return func(f *Fleet) error {
		ib := identity.Binding{Group: b.Group, Role: b.Role, Scope: b.Scope}
		if err := ib.Validate(); err != nil {
			return err
		}
		if strings.HasPrefix(b.Scope, "group:") {
			if _, ok := f.Groups[strings.TrimPrefix(b.Scope, "group:")]; !ok {
				return fmt.Errorf("unknown group in scope %q", b.Scope)
			}
		}
		f.Access = slices.DeleteFunc(f.Access, func(ex AccessBinding) bool {
			return ex.Group == b.Group && ex.Scope == b.Scope
		})
		f.Access = append(f.Access, b)
		return nil
	}
}

// Revoke removes the binding for (group, scope).
func Revoke(group, scope string) Mutation {
	return func(f *Fleet) error {
		before := len(f.Access)
		f.Access = slices.DeleteFunc(f.Access, func(ex AccessBinding) bool {
			return ex.Group == group && ex.Scope == scope
		})
		if len(f.Access) == before {
			return fmt.Errorf("no binding for group %q at %s", group, scope)
		}
		return nil
	}
}

// IdentityResolver builds the pure authorization resolver over this
// document plus the server-config baseline groups.
func (f *Fleet) IdentityResolver(baseViewer, baseEditor, baseOwner []string) identity.Resolver {
	bindings := make([]identity.Binding, 0, len(f.Access))
	for _, b := range f.Access {
		bindings = append(bindings, identity.Binding{Group: b.Group, Role: b.Role, Scope: b.Scope})
	}
	return identity.Resolver{
		Ancestry:       f.GroupAncestry,
		DeviceGroups:   func(tag string) []string { return f.Devices[tag].Groups },
		Bindings:       bindings,
		BaselineViewer: baseViewer,
		BaselineEditor: baseEditor,
		BaselineOwner:  baseOwner,
	}
}
