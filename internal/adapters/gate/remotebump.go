package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BumpInput asks the gate-runner to compute the flake.lock that pins one
// input (the core image) to its current upstream head. The runner computes
// in a scratch copy of its overlay clone; committing the returned lock onto
// the change branch is the caller's job (the console holds the worktree and
// the push credential).
func (g *RemoteGate) BumpInput(ctx context.Context, input string) (lock []byte, rev string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"input": input})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL+"/bump", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
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
		return nil, "", fmt.Errorf("gate-runner unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Lock  string `json:"lock"`
		Rev   string `json:"rev"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || !out.OK {
		detail := out.Error
		if detail == "" {
			detail = string(raw)
		}
		return nil, "", fmt.Errorf("bump refused (%d): %s", resp.StatusCode, detail)
	}
	return []byte(out.Lock), out.Rev, nil
}
