package oidc

import "strings"

// claims.go extracts group/role names from ID-token claims, tolerating
// every common provider shape so SSO works with or without a custom claim
// mapper. Ported from the proven PoC extractor:
//
//   - a flat string array at the configured groups claim ("groups": [...])
//   - Keycloak, Entra group claims, or a Zitadel Action emitting groups;
//   - Zitadel's native roles claim, a map role -> {orgID: domain}, at the
//     configured claim or any "...roles" key - the role keys are taken;
//   - Microsoft Entra app roles, a flat array at the "roles" claim - picked
//     up at any "...roles" key regardless of the configured claim.
//
// Merged and deduplicated.
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
		if strings.HasSuffix(k, "roles") { // ":roles" (Zitadel) or "roles" (Entra)
			add(mapKeys(v))
			add(strSlice(v))
		}
	}
	return out
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
