package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// client is the thin /api/v1 client: bearer token, JSON bodies, typed
// failures. Exit-code policy lives in main; the client only reports.
type client struct {
	base  string
	token string
	http  *http.Client
}

func newClient(base, token string) *client {
	return &client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
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
	//nosec G704 - c.base is the operator's own console endpoint from their config/flag, and path is a fixed in-code API route, not attacker input.
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	//nosec G704 - req targets the operator's own configured console endpoint, not a request-derived URL.
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

// printJSON pretty-prints any value to stdout.
func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// table prints rows with aligned columns.
func table(header []string, rows [][]string) {
	width := make([]int, len(header))
	for i, h := range header {
		width[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(width) && len(c) > width[i] {
				width[i] = len(c)
			}
		}
	}
	line := func(cols []string) {
		var b strings.Builder
		for i, c := range cols {
			fmt.Fprintf(&b, "%-*s  ", width[i], c)
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}
	line(header)
	for _, r := range rows {
		line(r)
	}
}
