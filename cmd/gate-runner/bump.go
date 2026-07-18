package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

// bump.go: POST /bump computes the flake.lock that pins an input to its
// current upstream head (delivery-process §9, phase two). The runner has nix
// and network; the CONSOLE has the change-request worktree and the push
// credential - so this endpoint only computes and returns the new lock, and
// the console commits it onto the CR branch. Nothing here mutates the
// runner's own overlay clone: the update runs in a throwaway copy.

// inputNameRe guards the one request field that reaches an argv: flake input
// names are simple identifiers.
var inputNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type bumpRequest struct {
	// Input is the flake input to update (e.g. "dawo", the core image).
	Input string `json:"input"`
}

type bumpResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Lock is the full new flake.lock content.
	Lock string `json:"lock,omitempty"`
	// Rev is the input's newly locked revision, for the CR description.
	Rev string `json:"rev,omitempty"`
}

func (s *server) handleBump(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var in bumpRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeBump(w, http.StatusBadRequest, bumpResponse{Error: "bad request: " + err.Error()})
		return
	}
	if !inputNameRe.MatchString(in.Input) {
		writeBump(w, http.StatusBadRequest, bumpResponse{Error: "bad input name"})
		return
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-r.Context().Done():
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	lock, rev, err := s.bumpInput(ctx, in.Input)
	if err != nil {
		writeBump(w, http.StatusUnprocessableEntity, bumpResponse{Error: err.Error()})
		return
	}
	writeBump(w, http.StatusOK, bumpResponse{OK: true, Lock: lock, Rev: rev})
}

// bumpInput copies the overlay clone to a scratch dir, updates one input's
// lock there and returns the resulting flake.lock plus the input's new rev.
func (s *server) bumpInput(ctx context.Context, input string) (lock, rev string, err error) {
	scratch, err := os.MkdirTemp("", "bump-*")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	// A plain copy of the checkout suffices: nix only needs flake.nix,
	// flake.lock and the local sources the flake references.
	// #nosec G204 - fixed binary, argv slice; paths are server-owned.
	if out, cerr := exec.CommandContext(ctx, "cp", "-a", s.workdir+"/.", scratch).CombinedOutput(); cerr != nil {
		return "", "", fmt.Errorf("scratch copy: %s", string(out))
	}
	// nix flake lock --update-input works on every nix we ship (the newer
	// `nix flake update <input>` spelling is 2.19+ only).
	// #nosec G204 - fixed binary; input passed the identifier whitelist above.
	cmd := exec.CommandContext(ctx, "nix", "flake", "lock", "--update-input", input)
	cmd.Dir = scratch
	if out, uerr := cmd.CombinedOutput(); uerr != nil {
		return "", "", fmt.Errorf("nix flake lock --update-input %s: %s", input, tail(string(out), 2000))
	}
	raw, err := os.ReadFile(filepath.Join(scratch, "flake.lock"))
	if err != nil {
		return "", "", err
	}
	var doc struct {
		Nodes map[string]struct {
			Locked struct {
				Rev string `json:"rev"`
			} `json:"locked"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", "", fmt.Errorf("parse new flake.lock: %w", err)
	}
	return string(raw), doc.Nodes[input].Locked.Rev, nil
}

func writeBump(w http.ResponseWriter, status int, resp bumpResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
