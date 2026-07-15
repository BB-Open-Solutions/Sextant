package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// RemoteBuilder implements ports.CacheBuilder against the gate-runner's
// /build endpoint: the runner realises the release at a revision and
// publishes it - signed - into the binary cache it serves. The endpoint is a
// poll-style job API, which matches EnsureBuilt's idempotent contract.
type RemoteBuilder struct {
	// URL is the gate-runner base URL, e.g. http://sextant-gate:8090.
	URL string
	// Token is the bearer secret; empty sends no Authorization header.
	Token string
	// Timeout bounds one poll call (the build itself runs runner-side). Zero
	// means 30s.
	Timeout time.Duration
	client  *http.Client
}

// NewRemoteBuilder returns a builder that delegates to the runner at url.
func NewRemoteBuilder(url, token string) *RemoteBuilder {
	return &RemoteBuilder{URL: url, Token: token, client: &http.Client{}}
}

type buildRequest struct {
	Rev   string   `json:"rev"`
	Hosts []string `json:"hosts"`
}

type buildResponse struct {
	Phase  string `json:"phase"`
	Detail string `json:"detail,omitempty"`
}

// EnsureBuilt implements ports.CacheBuilder.
func (b *RemoteBuilder) EnsureBuilt(ctx context.Context, rev string, hosts []string) (ports.BuildState, error) {
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(buildRequest{Rev: rev, Hosts: hosts})
	if err != nil {
		return ports.BuildState{}, fmt.Errorf("build: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL+"/build", bytes.NewReader(body))
	if err != nil {
		return ports.BuildState{}, fmt.Errorf("build: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if b.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.Token)
	}

	client := b.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The caller (rollout tick) treats an error as "hold and retry", so a
		// briefly unreachable runner never halts a run.
		return ports.BuildState{}, fmt.Errorf("build runner unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		// 4xx/5xx: configuration or request problems. Surface as an error (hold
		// + log), not as BuildFailed - a misconfigured runner should not halt a
		// rollout the way a genuinely broken build must.
		return ports.BuildState{}, fmt.Errorf("build runner error (status %d): %s", resp.StatusCode, string(raw))
	}
	var br buildResponse
	if err := json.Unmarshal(raw, &br); err != nil {
		return ports.BuildState{}, fmt.Errorf("build: decode runner response: %w", err)
	}
	switch br.Phase {
	case "done":
		return ports.BuildState{Phase: ports.BuildDone}, nil
	case "failed":
		return ports.BuildState{Phase: ports.BuildFailed, Detail: br.Detail}, nil
	default:
		return ports.BuildState{Phase: ports.BuildBuilding, Detail: br.Detail}, nil
	}
}
