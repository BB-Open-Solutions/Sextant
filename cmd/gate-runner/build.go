package main

// build.go: the runner's release-build surface (build-before-promote). The
// console asks POST /build to realise a set of hosts' closures at a git
// revision and publish them - signed - into the binary cache the runner
// serves under GET /cache/. The endpoint is a poll-style job API: the first
// call starts the build and answers "building"; subsequent calls report
// progress until "done" or "failed". Idempotent per (rev, hosts), so the
// rollout tick can ask as often as it likes.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type buildRequest struct {
	Rev   string   `json:"rev"`
	Hosts []string `json:"hosts"`
}

type buildResponse struct {
	Phase  string `json:"phase"` // building | done | failed
	Detail string `json:"detail,omitempty"`
}

// revRe: a git commit hash. The revision is interpolated into git argv (never
// a shell), but restricting it to hex also rejects option-injection ("-..."),
// refspec tricks and path escapes outright.
var revRe = regexp.MustCompile(`^[0-9a-f]{6,40}$`)

// buildKey identifies one release build: the revision plus the sorted host
// set. Hash the hosts so the key stays bounded for large rings.
func buildKey(rev string, hosts []string) string {
	sorted := append([]string(nil), hosts...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\x00")))
	return rev + "-" + hex.EncodeToString(sum[:8])
}

func (s *server) handleBuild(w http.ResponseWriter, r *http.Request) {
	// Authenticate before any work - a build is far heavier than an eval.
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gate-runner"`)
		writeJSON(w, http.StatusUnauthorized, buildResponse{Phase: "failed", Detail: "unauthorized"})
		return
	}
	if s.publisher == nil && s.publishFn == nil {
		writeJSON(w, http.StatusNotImplemented,
			buildResponse{Phase: "failed", Detail: "release cache not configured on this runner"})
		return
	}

	var req buildRequest
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, buildResponse{Phase: "failed", Detail: "bad request body"})
		return
	}
	if !revRe.MatchString(req.Rev) {
		writeJSON(w, http.StatusBadRequest, buildResponse{Phase: "failed", Detail: "rev must be a commit hash"})
		return
	}
	if len(req.Hosts) == 0 {
		writeJSON(w, http.StatusBadRequest, buildResponse{Phase: "failed", Detail: "hosts required"})
		return
	}

	key := buildKey(req.Rev, req.Hosts)
	s.buildMu.Lock()
	job, ok := s.builds[key]
	if !ok {
		job = &buildResponse{Phase: "building"}
		s.builds[key] = job
		go s.runBuild(key, req)
	}
	resp := *job // copy under the lock
	s.buildMu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

// runBuild executes one release build in the background: check out the
// revision into a scratch worktree, publish (build + signed copy), record the
// verdict. One build at a time - a second job waits for the slot, which is
// fine: the caller already got "building".
func (s *server) runBuild(key string, req buildRequest) {
	s.buildSlot <- struct{}{}
	defer func() { <-s.buildSlot }()

	err := s.buildAtRev(req)

	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	if err != nil {
		s.log.Error("release build failed", "rev", req.Rev, "err", err)
		s.builds[key] = &buildResponse{Phase: "failed", Detail: err.Error()}
		return
	}
	s.log.Info("release published", "rev", req.Rev, "hosts", len(req.Hosts))
	s.builds[key] = &buildResponse{Phase: "done"}
}

// buildAtRev materialises the overlay at the revision in a scratch worktree
// (the main workdir keeps serving /validate) and publishes the hosts from it.
func (s *server) buildAtRev(req buildRequest) error {
	// Deliberately detached from any request context: the build job outlives
	// the HTTP request that kicked it (poll-style API) and must not die when
	// that request's caller disconnects. The Publisher applies its own
	// timeout.
	ctx := context.Background()

	// The revision may be newer than the last sync; fetch under the eval lock
	// (the workdir's git state is shared with /validate).
	s.mu.Lock()
	err := s.git(ctx, s.workdir, "fetch", "--quiet", "origin", s.branch)
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("fetch before build: %w", err)
	}

	scratch := filepath.Join(filepath.Dir(s.workdir), "build-"+req.Rev)
	// A leftover worktree from a crashed run: remove and re-create.
	_ = s.git(ctx, s.workdir, "worktree", "remove", "--force", scratch)
	if err := s.git(ctx, s.workdir, "worktree", "add", "--detach", scratch, req.Rev); err != nil {
		return fmt.Errorf("checkout %s: %w", req.Rev, err)
	}
	defer func() {
		_ = s.git(ctx, s.workdir, "worktree", "remove", "--force", scratch)
	}()

	if s.publishFn != nil {
		return s.publishFn(ctx, scratch, req.Hosts)
	}
	s.publisher.HostVariants = s.variants
	return s.publisher.Publish(ctx, scratch, req.Hosts)
}

// handleCache serves the binary cache read-only. Substitution needs exactly
// two shapes - /nix-cache-info and flat files (narinfo, nar/*) - so directory
// listings are refused. Reads are deliberately unauthenticated: nix fetches
// over plain GET, and integrity comes from the signature on every path
// (devices pin the org public key), not from transport secrecy.
func (s *server) handleCache(w http.ResponseWriter, r *http.Request) {
	if s.cacheDir == "" {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/") {
		http.NotFound(w, r) // no listings
		return
	}
	http.StripPrefix("/cache/", http.FileServer(http.Dir(s.cacheDir))).ServeHTTP(w, r)
}

// ensureCacheInfo writes the nix-cache-info file nix requires of any
// substitution source, once, at boot. nix copy to a file:// store lays out
// narinfo/nar but does not create it.
func ensureCacheInfo(cacheDir string) error {
	// #nosec G301 - the binary cache holds only signed public store paths.
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	info := filepath.Join(cacheDir, "nix-cache-info")
	if _, err := os.Stat(info); err == nil {
		return nil
	}
	// #nosec G306 - world-readable by design: devices fetch it anonymously.
	return os.WriteFile(info, []byte("StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 30\n"), 0o644)
}
