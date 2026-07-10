package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// graph.go handles Microsoft Entra's groups "overage": when an account is
// in more than ~150 groups, the ID token carries no groups claim - only a
// _claim_names/_claim_sources pointer telling the client to ask Microsoft
// Graph. Without this fallback, RBAC silently sees zero groups for
// precisely the largest tenants.

// defaultGraphURL is the Graph endpoint returning every group the user is
// in, including nested membership.
const defaultGraphURL = "https://graph.microsoft.com/v1.0/me/transitiveMemberOf/microsoft.graph.group?$select=id,displayName&$top=999"

// hasGroupsOverage detects the overage marker in ID-token claims.
func hasGroupsOverage(claims map[string]any) bool {
	names, ok := claims["_claim_names"].(map[string]any)
	if !ok {
		return false
	}
	_, has := names["groups"]
	return has
}

// fetchGroupsFromGraph pages through the Graph membership list using the
// OAuth2 access token from the code exchange. Both the group object id and
// the display name are returned, so bindings may use either.
func fetchGroupsFromGraph(ctx context.Context, client *http.Client, baseURL, accessToken string) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	url := baseURL
	if url == "" {
		url = defaultGraphURL
	}
	seen := map[string]bool{}
	var out []string
	// Bound the paging loop: 50 pages x 999 groups is far beyond any sane
	// tenant; a broken nextLink chain must not loop forever.
	for page := 0; url != "" && page < 50; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph request: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph returned %d: %.200s", resp.StatusCode, body)
		}
		var pageDoc struct {
			Value []struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := json.Unmarshal(body, &pageDoc); err != nil {
			return nil, fmt.Errorf("graph response: %w", err)
		}
		for _, g := range pageDoc.Value {
			for _, name := range []string{g.ID, g.DisplayName} {
				if name != "" && !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
		url = pageDoc.NextLink
	}
	return out, nil
}
