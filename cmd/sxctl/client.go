package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// client is the thin /api/v1 client: bearer token, JSON bodies, typed
// failures. Exit-code policy lives in main; the client only reports.
// out is where command output (printJSON/table/etc) is written - os.Stdout
// in production, a buffer in tests - so commands never hardcode os.Stdout
// and stay testable without redirecting process-global state.
type client struct {
	base  string
	token string
	http  *http.Client
	out   io.Writer
}

// writeTimeout bounds a request that may run the Nix gate.
//
// A write is validated before it is committed, and validation costs one
// evaluation per configuration SHAPE the edit touches - about eight seconds
// each, and every shape when something invalidates the verdict memo (a core
// bump, a rekey). Thirty seconds covered a couple of shapes and then reported
// "context deadline exceeded" for an edit the server was still applying and
// would apply successfully. A write that looks failed and is not is the worst
// possible answer: the obvious response is to run it again.
//
// Reads keep a short bound - they touch a snapshot and are either quick or
// genuinely wrong.
const (
	readTimeout  = 30 * time.Second
	writeTimeout = 5 * time.Minute
)

func newClient(base, token string, out io.Writer) *client {
	return &client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: readTimeout},
		out:   out,
	}
}

// timeoutFor picks the bound from the method: anything that mutates may have
// to wait for the gate.
func timeoutFor(method string) time.Duration {
	if method == http.MethodGet {
		return readTimeout
	}
	return writeTimeout
}

// apiError carries the server's error body and status.
type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%d: %s", e.Status, e.Msg)
}

// do performs a request; out (when non-nil) receives the parsed JSON body.
func (c *client) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	// #nosec G704 - c.base is the operator's own console endpoint from their config/flag, and path is a fixed in-code API route, not attacker input.
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return err
	}
	c.http.Timeout = timeoutFor(method)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// #nosec G704 - req targets the operator's own configured console endpoint, not a request-derived URL.
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return &apiError{Status: resp.StatusCode, Msg: msg}
	}
	if out != nil {
		if s, ok := out.(*string); ok { // text endpoints (diff)
			*s = string(raw)
			return nil
		}
		if len(raw) > 0 {
			return json.Unmarshal(raw, out)
		}
	}
	return nil
}

// printJSON pretty-prints any value to the client's output writer.
func (c *client) printJSON(v any) {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// table prints rows with aligned columns to the client's output writer.
func (c *client) table(header []string, rows [][]string) {
	width := make([]int, len(header))
	for i, h := range header {
		width[i] = len(h)
	}
	for _, r := range rows {
		for i, col := range r {
			if i < len(width) && len(col) > width[i] {
				width[i] = len(col)
			}
		}
	}
	line := func(cols []string) {
		var b strings.Builder
		for i, col := range cols {
			fmt.Fprintf(&b, "%-*s  ", width[i], col)
		}
		_, _ = fmt.Fprintln(c.out, strings.TrimRight(b.String(), " "))
	}
	line(header)
	for _, r := range rows {
		line(r)
	}
}
