package oidc

import "strings"

// claims.go extracts group/role names from ID-token claims, tolerating
// every common provider shape so SSO works with or without a custom claim
// mapper. Ported from the proven PoC extractor:
//
//   - a flat string array at the configured groups claim ("groups": [...])
//   - Keycloak, Entra group claims, or a Zitadel Action emitting groups;
//   - Zitadel's native roles claim, a map role -> {orgID: domain}, at the
//     configured claim or at its fixed URN
//     ("urn:zitadel:iam:org:project:...roles") - the role keys are taken;
//   - Microsoft Entra app roles, a flat array at the fixed "roles" claim.
//
// Only the configured GroupsClaim plus these two fixed, documented shapes
// are consulted - never an arbitrary claim that merely happens to be named
// "*roles" - so an operator who configured GroupsClaim cannot have RBAC
// silently widened by an unrelated IdP claim. Merged and deduplicated.
func groupsFromClaims(claims map[string]any, groupsClaim string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ss []string) {
		for _, s := range ss {
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	add(strSlice(claims[groupsClaim]))
	add(mapKeys(claims[groupsClaim])) // configured claim is itself a roles map
	for k, v := range claims {
		if isDocumentedRolesClaim(k) {
			add(mapKeys(v))
			add(strSlice(v))
		}
	}
	return out
}

// zitadelRolesPrefix is the fixed URN namespace Zitadel uses for its
// project-roles claim; the trailing segment varies by project
// ("urn:zitadel:iam:org:project:roles" for the default project, or
// "...:<projectID>:roles" for others) but the prefix and "roles" suffix are
// stable across tenants.
const zitadelRolesPrefix = "urn:zitadel:iam:org:project:"

// isDocumentedRolesClaim reports whether k is one of the two fixed,
// documented multi-IdP claim shapes - Entra's "roles" or a Zitadel
// project-roles URN - as opposed to any claim that merely ends in "roles".
func isDocumentedRolesClaim(k string) bool {
	if k == "roles" {
		return true
	}
	return strings.HasPrefix(k, zitadelRolesPrefix) && strings.HasSuffix(k, "roles")
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := str(m[k]); s != "" {
			return s
		}
	}
	return ""
}

// mapKeys returns the keys of a JSON object claim (a roles map), else nil.
func mapKeys(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func strSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
