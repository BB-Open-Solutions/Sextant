// Package gate holds the remote validation-gate adapter. The console image
// carries no nix (staying small and sovereign), so when --gate=remote it
// delegates evaluation to a nix-capable gate-runner over HTTP. The runner
// evaluates the same overlay generator the in-process EvalGate would; this
// adapter only ships the candidate fleet.json and maps the verdict.
package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// RemoteGate validates by asking a gate-runner service. It is fail-closed:
// if the runner is unreachable or errors, the write is rejected rather than
// committed unvalidated - the gate is a security property, not a best effort.
type RemoteGate struct {
	// URL is the gate-runner base URL, e.g. http://sextant-gate:8090.
	URL string
	// Timeout bounds one validation call. Zero means 130s (a little over the
	// runner's own 120s eval budget).
	Timeout time.Duration
	client  *http.Client
}

// NewRemoteGate returns a gate that delegates to the runner at url.
func NewRemoteGate(url string) *RemoteGate {
	return &RemoteGate{URL: url, client: &http.Client{}}
}

type validateRequest struct {
	Hosts []string `json:"hosts"`
	Fleet string   `json:"fleet"`
}

type validateResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Validate implements ports.Gate. It reads the candidate fleet.json the
// caller just wrote into repoDir and sends it to the runner; the runner's
// own overlay clone supplies the generator and modules.
func (g *RemoteGate) Validate(ctx context.Context, repoDir string, hosts []string) error {
	fleet, err := os.ReadFile(filepath.Join(repoDir, "fleet.json"))
	if err != nil {
		return fmt.Errorf("gate: read candidate fleet.json: %w", err)
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 130 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(validateRequest{Hosts: hosts, Fleet: string(fleet)})
	if err != nil {
		return fmt.Errorf("gate: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		g.URL+"/validate", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gate: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := g.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// Fail-closed: a gate we cannot reach must not wave writes through.
		return fmt.Errorf("gate-runner unreachable, refusing to commit unvalidated: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		var vr validateResponse
		if err := json.Unmarshal(raw, &vr); err != nil {
			return fmt.Errorf("gate: decode runner response: %w", err)
		}
		if vr.OK {
			return nil
		}
		// A well-formed rejection: the generator/module system said no.
		return &ports.ValidationError{Detail: vr.Error}
	case http.StatusUnprocessableEntity:
		var vr validateResponse
		_ = json.Unmarshal(raw, &vr)
		detail := vr.Error
		if detail == "" {
			detail = string(raw)
		}
		return &ports.ValidationError{Detail: detail}
	default:
		return fmt.Errorf("gate-runner error (status %d): %s", resp.StatusCode, string(raw))
	}
}
