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
	// Token is the bearer secret a token-protected gate-runner requires; empty
	// sends no Authorization header.
	Token  string
	client *http.Client
}

// NewRemoteGate returns a gate that delegates to the runner at url.
func NewRemoteGate(url string) *RemoteGate {
	return &RemoteGate{URL: url, client: &http.Client{}}
}

// WithToken sets the bearer secret presented to the gate-runner. Returns the
// gate for chaining at wiring time.
func (g *RemoteGate) WithToken(token string) *RemoteGate {
	g.Token = token
	return g
}

type validateRequest struct {
	Hosts []string `json:"hosts"`
	Fleet string   `json:"fleet"`
}

type validateResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Detail is the runner's underlying cause for an infrastructure failure.
	// Optional: an older runner does not send it, and the message degrades to
	// the classification alone rather than breaking.
	Detail string `json:"detail,omitempty"`
}

// Validate implements ports.Gate. It reads the candidate fleet.json the
// caller just wrote into repoDir and sends it to the runner; the runner's
// own overlay clone supplies the generator and modules.
func (g *RemoteGate) Validate(ctx context.Context, repoDir string, hosts []string) error {
	// #nosec G304 - repoDir is the service's own repo working dir (not request input) and the filename is a fixed literal.
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
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}

	client := g.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// Fail-closed: a gate we cannot reach must not wave writes through.
		return fmt.Errorf("gate-runner unreachable, refusing to commit unvalidated: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if len(raw) == 0 && readErr != nil {
		// A truncated response must not surface as an EMPTY rejection detail:
		// the operator would see a verdict with no reason.
		raw = []byte(fmt.Sprintf("(response body unreadable: %v)", readErr))
	}

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
		// An infrastructure failure, not a verdict. Render the runner's own
		// words rather than the JSON document: the operator who sees this is
		// the one who has to act on it, and a raw body sends them to the pod
		// log to find the sentence that was already in it.
		var vr validateResponse
		if err := json.Unmarshal(raw, &vr); err == nil && vr.Error != "" {
			msg := vr.Error
			if vr.Detail != "" {
				msg += ": " + vr.Detail
			}
			return fmt.Errorf("gate-runner error (status %d): %s", resp.StatusCode, msg)
		}
		return fmt.Errorf("gate-runner error (status %d): %s", resp.StatusCode, string(raw))
	}
}

type parseRequest struct {
	Code string `json:"code"`
}

type parseResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// CheckSyntax runs a fast syntax check of an overlay module's nix source on
// the runner (`nix-instantiate --parse`, no evaluation). It returns an empty
// string when the source parses, or the first syntax error otherwise. A
// transport failure returns an error so the caller can say "check
// unavailable" rather than "valid".
func (g *RemoteGate) CheckSyntax(ctx context.Context, code string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body, err := json.Marshal(parseRequest{Code: code})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL+"/parse", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	client := g.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gate-runner unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gate-runner parse returned %d", resp.StatusCode)
	}
	var pr parseResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return "", fmt.Errorf("decode parse response: %w", err)
	}
	return pr.Error, nil
}
