package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newBuildServer wires a server whose git and publish are observable stubs -
// the release build path (the binary-cache write path devices trust) then
// tests without a git repo or a nix evaluation.
func newBuildServer(t *testing.T, publish func(ctx context.Context, dir string, hosts []string) error) (*server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	calls := []string{}
	s := &server{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		workdir:   filepath.Join(t.TempDir(), "overlay"),
		branch:    "main",
		token:     "sesame",
		builds:    map[string]*buildResponse{},
		buildSlot: make(chan struct{}, 1),
		gitRun: func(_ context.Context, dir string, args ...string) error {
			mu.Lock()
			calls = append(calls, strings.Join(args, " "))
			mu.Unlock()
			return nil
		},
		publishFn: publish,
	}
	return s, &calls
}

func postBuild(t *testing.T, s *server, token, body string) (int, buildResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/build", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.handleBuild(rec, req)
	var br buildResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &br)
	return rec.Code, br
}

// waitPhase polls the job API until the phase settles (the build runs in a
// background goroutine).
func waitPhase(t *testing.T, s *server, token, body, want string) buildResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, br := postBuild(t, s, token, body); br.Phase == want {
			return br
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, br := postBuild(t, s, token, body)
	t.Fatalf("phase = %q, want %q", br.Phase, want)
	return br
}

func TestBuildRequiresAuthAndValidInput(t *testing.T) {
	s, _ := newBuildServer(t, func(context.Context, string, []string) error { return nil })

	if code, _ := postBuild(t, s, "", `{"rev":"abcdef1","hosts":["h1"]}`); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated build = %d", code)
	}
	if code, _ := postBuild(t, s, "wrong", `{"rev":"abcdef1","hosts":["h1"]}`); code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", code)
	}
	// A revision must be a commit hash: option/refspec injection dies here.
	for _, bad := range []string{`{"rev":"--upload-pack=/x","hosts":["h1"]}`,
		`{"rev":"HEAD","hosts":["h1"]}`, `{"rev":"abcdef1"}`, `not json`} {
		if code, _ := postBuild(t, s, "sesame", bad); code != http.StatusBadRequest {
			t.Fatalf("bad input %q = %d, want 400", bad, code)
		}
	}
}

func TestBuildLifecycleIdempotentJob(t *testing.T) {
	var mu sync.Mutex
	published := 0
	release := make(chan struct{})
	s, gitCalls := newBuildServer(t, func(_ context.Context, dir string, hosts []string) error {
		<-release
		mu.Lock()
		published++
		mu.Unlock()
		if len(hosts) != 2 || !strings.Contains(dir, "build-abcdef1") {
			return fmt.Errorf("wrong publish args: %s %v", dir, hosts)
		}
		return nil
	})

	body := `{"rev":"abcdef1","hosts":["h1","h2"]}`
	// First call starts the job and reports building; repeats report the SAME
	// job rather than spawning another build.
	for range 3 {
		code, br := postBuild(t, s, "sesame", body)
		if code != http.StatusOK || br.Phase != "building" {
			t.Fatalf("kick = %d %+v", code, br)
		}
	}
	close(release)
	waitPhase(t, s, "sesame", body, "done")
	mu.Lock()
	if published != 1 {
		t.Fatalf("job ran %d times for one (rev,hosts) key", published)
	}
	mu.Unlock()

	// The scratch worktree was created at the revision and cleaned up after.
	joined := strings.Join(*gitCalls, "\n")
	if !strings.Contains(joined, "worktree add --detach") || !strings.Contains(joined, "abcdef1") {
		t.Fatalf("no detached checkout at the revision:\n%s", joined)
	}
	if !strings.Contains(joined, "worktree remove --force") {
		t.Fatalf("scratch worktree not cleaned up:\n%s", joined)
	}
}

func TestBuildFailureIsReportedWithDetail(t *testing.T) {
	s, _ := newBuildServer(t, func(context.Context, string, []string) error {
		return fmt.Errorf("nix build exploded")
	})
	body := `{"rev":"abcdef1","hosts":["h1"]}`
	postBuild(t, s, "sesame", body)
	br := waitPhase(t, s, "sesame", body, "failed")
	if !strings.Contains(br.Detail, "nix build exploded") {
		t.Fatalf("failure detail lost: %+v", br)
	}
}

func TestBuildWithoutCacheConfigured(t *testing.T) {
	s, _ := newBuildServer(t, nil)
	s.publishFn = nil // no publisher, no seam: cache disabled
	if code, _ := postBuild(t, s, "sesame", `{"rev":"abcdef1","hosts":["h1"]}`); code != http.StatusNotImplemented {
		t.Fatalf("unconfigured cache = %d, want 501", code)
	}
}

func TestEnsureCacheInfoAndCacheServing(t *testing.T) {
	dir := t.TempDir()
	if err := ensureCacheInfo(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.ReadFile(filepath.Join(dir, "nix-cache-info"))
	if err != nil || !strings.Contains(string(info), "StoreDir: /nix/store") {
		t.Fatalf("nix-cache-info wrong: %s %v", info, err)
	}
	// Idempotent: a second call must not clobber a customised file.
	if err := os.WriteFile(filepath.Join(dir, "nix-cache-info"), []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureCacheInfo(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "nix-cache-info")); string(got) != "custom" {
		t.Fatal("ensureCacheInfo overwrote an existing file")
	}

	s := &server{cacheDir: dir}
	get := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.handleCache(rec, req)
		return rec.Code
	}
	if got := get("/cache/nix-cache-info"); got != http.StatusOK {
		t.Fatalf("cache file = %d", got)
	}
	// Listings are refused; substitution only ever fetches concrete files.
	if got := get("/cache/"); got != http.StatusNotFound {
		t.Fatalf("directory listing = %d, want 404", got)
	}
	// A runner without a cache serves nothing.
	s.cacheDir = ""
	if got := get("/cache/nix-cache-info"); got != http.StatusNotFound {
		t.Fatalf("dark cache = %d, want 404", got)
	}
}
