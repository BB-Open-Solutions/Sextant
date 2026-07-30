package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
		body, err := getWithRetry(ctx, client, req)
		if err != nil {
			return nil, err
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

// graphAttempts is how many times one page is tried before the login fails.
const graphAttempts = 3

// getWithRetry performs one Graph page request, retrying a transient failure.
//
// This sits on the LOGIN path: the caller turns any error into a 502 and the
// whole sign-in fails. The fallback it serves only runs for users in more than
// 150 groups - which in practice means administrators - so without a retry the
// people with the most rights got the flakiest login, from a single dropped
// packet or one rate-limit response. Git's network operations have retried
// transient failures for a long time; this did not.
//
// Retried: a transport error, 429, and 5xx. Not retried: 4xx other than 429,
// because a rejected token or a malformed request will be rejected identically
// on the second attempt and retrying only delays a real answer.
func getWithRetry(ctx context.Context, client *http.Client, req *http.Request) ([]byte, error) {
	var lastErr error
	for attempt := range graphAttempts {
		if attempt > 0 {
			// Honour Retry-After when the server sent one; otherwise back off
			// 200ms, 400ms. Graph's rate limiter states how long to wait and
			// ignoring it is how a retry storm starts.
			select {
			case <-time.After(retryDelay(attempt, lastErr)):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		resp, err := client.Do(req.Clone(ctx))
		if err != nil {
			lastErr = &graphError{err: fmt.Errorf("graph request: %w", err)}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		retryAfter := resp.Header.Get("Retry-After")
		status := resp.StatusCode
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = &graphError{err: readErr}
			continue
		}
		if status == http.StatusOK {
			return body, nil
		}
		httpErr := fmt.Errorf("graph returned %d: %.200s", status, body)
		if status != http.StatusTooManyRequests && status < 500 {
			return nil, httpErr
		}
		lastErr = &graphError{err: httpErr, retryAfter: retryAfter}
	}
	var ge *graphError
	if errors.As(lastErr, &ge) {
		return nil, fmt.Errorf("graph unreachable after %d attempts: %w", graphAttempts, ge.err)
	}
	return nil, lastErr
}

// graphError is a retryable failure, carrying any Retry-After the server asked
// for. A non-retryable status never becomes one of these - it is returned
// straight away.
type graphError struct {
	err        error
	retryAfter string
}

func (e *graphError) Error() string { return e.err.Error() }
func (e *graphError) Unwrap() error { return e.err }

// retryDelay is the server's Retry-After when it gave a usable one, else an
// exponential back-off. Capped so a hostile or confused header cannot park a
// login request for minutes.
func retryDelay(attempt int, lastErr error) time.Duration {
	var ge *graphError
	if errors.As(lastErr, &ge) && ge.retryAfter != "" {
		if secs, err := strconv.Atoi(ge.retryAfter); err == nil && secs > 0 {
			if d := time.Duration(secs) * time.Second; d <= 5*time.Second {
				return d
			}
			return 5 * time.Second
		}
	}
	return time.Duration(200*(1<<(attempt-1))) * time.Millisecond
}
