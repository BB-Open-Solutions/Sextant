// Command gate-runner is the nix-capable validation gate for the console.
// The console image ships no nix (small, sovereign); when it runs with
// --gate=remote it POSTs candidate configurations here. This service keeps a
// warm clone of the overlay repo, drops in the candidate fleet.json, and
// forces the affected hosts' toplevel derivation with `nix eval` - the same
// safety property as the in-process EvalGate, isolated in a nix runtime.
//
// It is intentionally tiny: one endpoint, one serialized evaluation at a
// time (a single overlay working tree), fail-closed by construction. A
// bounded semaphore admits at most -max-concurrent requests to wait for that
// single evaluation slot; anything beyond that fails fast (503) rather than
// piling up blocked goroutines.
//
// /validate requires a shared bearer token (GATE_TOKEN, env-only) unless
// -addr is loopback, so an arbitrary reachable caller cannot force an
// expensive nix eval for free.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/nix"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/health"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/logging"
)

func main() {
	var (
		addr          = flag.String("addr", envOr("GATE_ADDR", "0.0.0.0:8090"), "listen address")
		workdir       = flag.String("workdir", envOr("GATE_WORKDIR", "/data/overlay"), "overlay clone directory")
		remote        = flag.String("remote", os.Getenv("GATE_OVERLAY_REMOTE"), "overlay git remote URL")
		branch        = flag.String("branch", envOr("GATE_OVERLAY_BRANCH", "main"), "overlay branch to track")
		variants      = flag.String("host-variants", os.Getenv("GATE_HOST_VARIANTS"), "comma-separated host suffixes, e.g. ,-sb")
		evalSecs      = flag.Int("eval-timeout", 120, "per-evaluation timeout, seconds")
		chunkSize     = flag.Int("chunk-size", envOrInt("GATE_CHUNK_SIZE", 50), "max host toplevels forced per nix process (bounds peak memory)")
		evalWorkers   = flag.Int("eval-workers", envOrInt("GATE_EVAL_WORKERS", 1), "concurrent eval batches; peak memory = workers x batch")
		cacheDir      = flag.String("cache-dir", envOr("GATE_CACHE_DIR", ""), "binary-cache directory to publish releases into (empty disables /build and /cache)")
		cacheKey      = flag.String("cache-key", envOr("GATE_CACHE_KEY_FILE", ""), "path to the nix signing secret key for published releases")
		logFormat     = flag.String("log-format", envOr("GATE_LOG_FORMAT", "text"), "log format: text|json")
		logLevel      = flag.String("log-level", envOr("GATE_LOG_LEVEL", "info"), "log level: debug|info|warn|error")
		maxConcurrent = flag.Int("max-concurrent", 4, "max /validate requests admitted to wait for the evaluation slot at once")
		// nix-eval-jobs bounds memory PER WORKER, so an oversized evaluation
		// fails its own host instead of killing the runner - and with the
		// runner, every path that commits configuration. Empty keeps the
		// in-process chunker, so this binary still works where the tool is
		// absent.
		evalJobs     = flag.String("eval-jobs", envOr("GATE_EVAL_JOBS", ""), "nix-eval-jobs binary; empty uses the in-process chunker")
		releaseChunk = flag.Int("release-chunk-size", envOrInt("GATE_RELEASE_CHUNK_SIZE", 0), "host toplevels per nix process on the release build (0 = one invocation)")
		evalJobsMem  = flag.Int("eval-jobs-max-memory-mb", envOrInt("GATE_EVAL_JOBS_MAX_MEMORY_MB", 0), "per-worker memory ceiling for nix-eval-jobs, MB (0 = its default)")
	)
	flag.Parse()

	log := logging.New(os.Stderr, *logFormat, *logLevel)
	if *remote == "" {
		log.Error("no overlay remote configured (GATE_OVERLAY_REMOTE)")
		os.Exit(2)
	}
	if *evalSecs <= 0 {
		// A non-positive timeout would hand nix eval a degenerate context
		// (already expired, or effectively unbounded via a bad flag value);
		// fail at startup, not on the first request.
		log.Error("eval-timeout must be positive", "seconds", *evalSecs)
		os.Exit(2)
	}
	if *maxConcurrent <= 0 {
		log.Error("max-concurrent must be positive", "value", *maxConcurrent)
		os.Exit(2)
	}
	if *chunkSize <= 0 {
		log.Error("chunk-size must be positive", "value", *chunkSize)
		os.Exit(2)
	}
	if *evalWorkers <= 0 {
		log.Error("eval-workers must be positive", "value", *evalWorkers)
		os.Exit(2)
	}

	// The shared bearer token is env-only, never a flag: a flag default is
	// echoed by -h, and flag values can leak via process listings, neither
	// of which is acceptable for a credential. Fail-safe: a gate reachable
	// over the network MUST have a token configured, or it stays an
	// unauthenticated resource-exhaustion surface (any pod that can reach it
	// can force a nix eval). A loopback-only listener may run without one
	// (local dev / a sidecar reached only via localhost).
	token := os.Getenv("GATE_TOKEN")
	if token == "" {
		if isLoopback(*addr) {
			log.Warn("GATE_TOKEN not set; /validate accepts unauthenticated requests (allowed only because -addr is loopback)", "addr", *addr)
		} else {
			log.Error("GATE_TOKEN not set and -addr is not loopback; refusing to serve /validate unauthenticated over the network", "addr", *addr)
			os.Exit(2)
		}
	}

	srv := &server{
		log:      log,
		workdir:  *workdir,
		remote:   *remote,
		branch:   *branch,
		variants: splitVariants(*variants),
		gate: &nix.EvalGate{
			Timeout:     time.Duration(*evalSecs) * time.Second,
			ChunkSize:   *chunkSize,
			Workers:     *evalWorkers,
			JobsBin:     *evalJobs,
			MaxMemoryMB: *evalJobsMem,
		},
		sem:          make(chan struct{}, *maxConcurrent),
		token:        token,
		cacheToken:   os.Getenv("CACHE_TOKEN"),
		consoleURL:   strings.TrimRight(os.Getenv("SEXTANT_CONSOLE_URL"), "/"),
		verified:     map[string]time.Time{},
		builds:       map[string]*buildResponse{},
		buildSlot:    make(chan struct{}, 1),
		buildCancels: map[string]context.CancelFunc{},
	}
	// The release cache is opt-in: both the directory and the signing key must
	// be configured, or /build answers 501 and /cache stays dark. Half a
	// configuration (a cache without a key) must not publish unsigned paths.
	if *cacheDir != "" && *cacheKey != "" {
		if err := ensureCacheInfo(*cacheDir); err != nil {
			log.Error("cache dir not usable", "dir", *cacheDir, "err", err)
			os.Exit(2)
		}
		srv.cacheDir = *cacheDir
		srv.publisher = nix.NewPublisher(*cacheDir, *cacheKey)
		// Batch the realise+copy the same way the eval gate batches: peak
		// memory is a property of one nix process, not of the work.
		srv.publisher.ChunkSize = *releaseChunk
		log.Info("release cache enabled", "dir", *cacheDir)
	} else if *cacheDir != "" || *cacheKey != "" {
		log.Error("cache-dir and cache-key must be set together (refusing an unsigned cache)")
		os.Exit(2)
	}
	if err := srv.ensureClone(context.Background()); err != nil {
		log.Error("initial overlay clone failed", "err", err)
		os.Exit(1)
	}

	checks := health.New(5 * time.Second)
	checks.SetLogger(log)
	checks.Register("overlay-clone", srv.cloneUsable)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /validate", srv.handleValidate)
	mux.HandleFunc("POST /build", srv.handleBuild)
	mux.HandleFunc("POST /build/cancel", srv.handleBuildCancel)
	mux.HandleFunc("POST /parse", srv.handleParse)
	mux.HandleFunc("POST /bump", srv.handleBump)
	mux.HandleFunc("GET /cache/", srv.handleCache)
	mux.Handle("GET /healthz", checks.Liveness())
	mux.Handle("GET /readyz", checks.Readiness())

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Writes can block on a long eval; keep generous.
		WriteTimeout: time.Duration(*evalSecs+30) * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		log.Info("gate-runner listening", "addr", *addr, "branch", *branch, "workdir", *workdir, "max_concurrent", *maxConcurrent)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server error", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	log.Info("gate-runner stopped")
}

type server struct {
	log      *slog.Logger
	workdir  string
	remote   string
	branch   string
	variants []string
	gate     *nix.EvalGate

	// cacheToken guards the read-only binary cache. Empty leaves it open,
	// which is the right default for a runner nobody exposes and for a local
	// run, but a cache on a public host should set it: a signed path proves
	// integrity, never confidentiality, so signatures stop somebody serving a
	// tampered closure and do nothing about them reading yours. What leaks
	// without it is one infrastructure hostname and a package manifest -
	// which versions of what a fleet runs (threat model, control 8).
	//
	// Separate from the gate token on purpose. Every device needs this one and
	// no device needs the gate's, and a single secret for both would put a
	// build trigger in the hands of every laptop in the fleet.
	cacheToken string

	// consoleURL, when set, verifies a substituting device's credential with
	// the console instead of comparing a shared token. Better in the way that
	// matters: the credential is per device and already on every machine, so
	// there is no secret to distribute and retiring a device takes its cache
	// access with it.
	consoleURL string
	// verified caches recent verdicts. A single substitution fetches hundreds
	// of paths, and asking the console once per file would turn every rollout
	// into a denial-of-service against our own control plane.
	verified   map[string]time.Time
	verifiedMu sync.Mutex

	// token is the shared bearer secret required on every /validate call.
	// Empty means the gate was started without one (only permitted for a
	// loopback -addr; see main), so every caller is accepted - that
	// fail-safe decision is made once at boot, not re-litigated per request.
	token string

	mu sync.Mutex // one evaluation at a time; single overlay working tree

	// sem is a buffered admission-control semaphore: it bounds how many
	// /validate requests may be waiting on mu at once, so a request burst
	// fails fast (503) instead of stacking up unbounded blocked goroutines
	// behind the single evaluation slot.
	sem chan struct{}

	// Release cache (build-before-promote). publisher nil = /build disabled.
	publisher *nix.Publisher
	cacheDir  string
	buildMu   sync.Mutex // guards builds
	builds    map[string]*buildResponse
	// buildCancels holds the stop switch for each in-flight build, so a
	// cancelled rollout can actually stop the work it started.
	buildCancels map[string]context.CancelFunc
	// buildSlot serialises actual build work (heavier than an eval) while the
	// job API stays responsive.
	buildSlot chan struct{}

	// Seams for the build path's tests: production wires these to the real
	// git subprocess and the nix Publisher; tests observe the exact calls
	// without a git repo or a nix evaluation. Nil falls back to production.
	gitRun    func(ctx context.Context, dir string, args ...string) error
	publishFn func(ctx context.Context, repoDir string, hosts []string) error
}

type validateRequest struct {
	Hosts []string `json:"hosts"`
	Fleet string   `json:"fleet"`
}

type validateResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *server) handleValidate(w http.ResponseWriter, r *http.Request) {
	// Authenticate before doing any work: an unauthenticated caller must not
	// be able to force body parsing or, worse, a nix evaluation.
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gate-runner"`)
		writeJSON(w, http.StatusUnauthorized, validateResponse{Error: "unauthorized"})
		return
	}

	var req validateRequest
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, validateResponse{Error: "bad request body"})
		return
	}
	if req.Fleet == "" {
		writeJSON(w, http.StatusBadRequest, validateResponse{Error: "missing fleet.json"})
		return
	}

	// Context-aware acquire: wait for an admission slot, but only as long as
	// the request's own context allows. A saturated queue therefore fails
	// with 503 once the caller's deadline passes or it disconnects, instead
	// of blocking the goroutine forever.
	select {
	case s.sem <- struct{}{}:
	case <-r.Context().Done():
		writeJSON(w, http.StatusServiceUnavailable, validateResponse{Error: "validation queue saturated, retry later"})
		return
	}
	defer func() { <-s.sem }()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Refresh the overlay so the generator and modules match the console's
	// base, then stage the candidate as a throwaway COMMIT in a scratch
	// worktree and evaluate that. A committed (clean) tree matters: nix
	// copies a dirty flake's entire source to the store on EVERY eval and
	// disables its eval cache, which put a multi-second floor under each
	// validation. The commit stays local to the runner - nothing pushes it.
	if err := s.sync(r.Context()); err != nil {
		s.log.Error("overlay sync failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, validateResponse{Error: "overlay sync failed"})
		return
	}
	scratch, err := s.stageCandidate(r.Context(), req.Fleet)
	if err != nil {
		s.log.Error("staging candidate failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, validateResponse{Error: "staging candidate failed"})
		return
	}

	s.gate.HostVariants = s.variants
	if err := s.gate.Validate(r.Context(), scratch, req.Hosts); err != nil {
		// A ValidationError is the expected "generator said no" verdict.
		writeJSON(w, http.StatusUnprocessableEntity, validateResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, validateResponse{OK: true})
}

// stageCandidate materialises the candidate fleet.json as a local commit in
// a reusable detached scratch worktree and returns that worktree's path.
// Caller holds s.mu (the worktree is shared per-runner state).
func (s *server) stageCandidate(ctx context.Context, fleetDoc string) (string, error) {
	scratch := filepath.Join(filepath.Dir(s.workdir), "validate")
	if _, err := os.Stat(filepath.Join(scratch, ".git")); err != nil {
		_ = s.git(ctx, s.workdir, "worktree", "remove", "--force", scratch)
		if err := s.git(ctx, s.workdir, "worktree", "add", "--detach", scratch, "origin/"+s.branch); err != nil {
			return "", err
		}
	} else if err := s.git(ctx, scratch, "checkout", "--detach", "--force", "origin/"+s.branch); err != nil {
		return "", err
	}
	// #nosec G306 - the candidate is a throwaway eval input, not a secret.
	if err := os.WriteFile(filepath.Join(scratch, "fleet.json"), []byte(fleetDoc), 0o644); err != nil {
		return "", err
	}
	if err := s.git(ctx, scratch, "add", "fleet.json"); err != nil {
		return "", err
	}
	// --allow-empty, because a candidate identical to the base is a normal
	// request and not an error. Every DAWO core update is exactly that: it
	// moves the flake's core pin and leaves fleet.json byte-identical, so
	// without this git exits 1 with "nothing to commit, working tree clean",
	// staging fails, and the console shows the change as Failed with a
	// gate-runner 500. Measured on production 2026-08-05: every core update
	// in the review queue had failed this way.
	//
	// The empty commit still matters. The eval needs a CLEAN tree - nix copies
	// a dirty flake's whole source to the store on every eval and disables its
	// eval cache - so the commit is what makes the worktree evaluable, whether
	// or not it carries a diff.
	if err := s.git(ctx, scratch,
		"-c", "user.name=gate-runner", "-c", "user.email=gate@localhost",
		"commit", "--quiet", "--no-verify", "--allow-empty", "-m", "candidate"); err != nil {
		return "", err
	}
	return scratch, nil
}

// authorized reports whether r carries the configured bearer token. An
// empty s.token means the gate was started without one, which main only
// allows for a loopback -addr - so every caller is accepted here.
func (s *server) authorized(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) == 1
}

// bearerToken extracts the token from a "Bearer <token>" Authorization
// header, or "" if the header is absent or a different scheme.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}

// isLoopback reports whether addr's host part is a loopback address. A
// bare ":8090" (empty host) is net.Listen's all-interfaces form and is
// deliberately NOT loopback. Kept as a small local copy rather than
// importing internal/platform/config, a separate concern (console flags)
// this binary should not depend on.
func isLoopback(addr string) bool {
	host := addr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}

// ensureClone clones the overlay if the workdir is not yet a repo.
func (s *server) ensureClone(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(s.workdir, ".git")); err == nil {
		return s.sync(ctx)
	}
	// #nosec G301 - parent of the overlay clone holds only public overlay source, not secrets; 0755 is fine.
	if err := os.MkdirAll(filepath.Dir(s.workdir), 0o755); err != nil {
		return err
	}
	return s.git(ctx, "", "clone", "--branch", s.branch, s.remote, s.workdir)
}

// sync fetches and hard-resets the clone to the tracked branch head, then
// wipes any leftover candidate so evaluation starts from a clean tree.
func (s *server) sync(ctx context.Context) error {
	if err := s.git(ctx, s.workdir, "fetch", "--quiet", "origin", s.branch); err != nil {
		return err
	}
	return s.git(ctx, s.workdir, "reset", "--hard", "--quiet", "origin/"+s.branch)
}

// cloneUsable is the /readyz check: the overlay clone must exist and be a
// git working tree git itself is willing to operate on, or /validate cannot
// do useful work (sync and eval both shell out to git/nix against workdir).
func (s *server) cloneUsable(ctx context.Context) error {
	return s.git(ctx, s.workdir, "rev-parse", "--is-inside-work-tree")
}

func (s *server) git(ctx context.Context, dir string, args ...string) error {
	if s.gitRun != nil {
		return s.gitRun(ctx, dir, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// #nosec G204 - fixed "git" binary with an internal argv slice (no shell, no user-composed command); args are code-controlled subcommands.
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// HOME is set so git reads the netrc for the private overlay remote.
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, string(out))
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// splitVariants parses a comma-separated host-suffix list. An empty string
// means no variants; note "" entries are meaningful (the bare tag itself),
// so a value like ",-sb" yields ["", "-sb"].
func splitVariants(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envOrInt reads an int env var, falling back to def when unset or unparseable.
// A bad value is validated at startup by the caller (it must be positive).
func envOrInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

type parseRequest struct {
	Code string `json:"code"`
}

type parseResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// handleParse is the editor's fast syntax check (design D4b): it runs
// `nix-instantiate --parse` on the submitted overlay module source - a pure
// parse, no evaluation, no I/O, no network - and returns the first syntax
// error with its line:column. It is cheaper feedback than the full gate eval
// on save (which remains the semantic validator). Parsing never executes the
// code, so an unreviewed buffer is safe to check.
func (s *server) handleParse(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gate-runner"`)
		writeJSON(w, http.StatusUnauthorized, parseResponse{Error: "unauthorized"})
		return
	}
	var req parseRequest
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, parseResponse{Error: "bad request body"})
		return
	}
	if req.Code == "" {
		writeJSON(w, http.StatusBadRequest, parseResponse{Error: "missing code"})
		return
	}
	f, err := os.CreateTemp("", "sextant-parse-*.nix")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, parseResponse{Error: "scratch file"})
		return
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(req.Code); err != nil {
		_ = f.Close()
		writeJSON(w, http.StatusInternalServerError, parseResponse{Error: "scratch write"})
		return
	}
	_ = f.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// #nosec G204 - fixed argv, the only caller-influenced input is the file
	// path we just created; --parse never evaluates the module.
	cmd := exec.CommandContext(ctx, "nix-instantiate", "--parse", f.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeJSON(w, http.StatusOK, parseResponse{Error: parseErrorLine(string(out))})
		return
	}
	writeJSON(w, http.StatusOK, parseResponse{OK: true})
}

// parseErrorLine pulls the actionable "error:" line out of nix-instantiate's
// output, rewriting the scratch path away so the operator sees a clean
// "line:col: message". Falls back to the trimmed output.
func parseErrorLine(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if i := strings.Index(strings.ToLower(ln), "error:"); i >= 0 {
			msg := strings.TrimSpace(ln[i+len("error:"):])
			// Drop the scratch file path prefix if present.
			if j := strings.LastIndex(msg, "/sextant-parse-"); j >= 0 {
				if k := strings.Index(msg[j:], ".nix:"); k >= 0 {
					msg = "at line " + msg[j+k+len(".nix:"):]
				}
			}
			return "syntax error: " + msg
		}
	}
	if s := strings.TrimSpace(out); s != "" {
		return s
	}
	return "syntax error"
}

// cacheAuthorized checks the credential a substituting device presents.
//
// HTTP Basic rather than a bearer header, because that is what nix speaks: a
// netrc entry becomes Basic auth and nothing else is on offer. The username is
// ignored - netrc requires one, and a per-device name here would imply a
// per-device secret this does not have.
func (s *server) cacheAuthorized(r *http.Request) bool {
	if s.cacheToken == "" && s.consoleURL == "" {
		return true
	}
	_, pass, ok := r.BasicAuth()
	if !ok || pass == "" {
		return false
	}
	// A shared token, when one is configured, stays valid: it is the escape
	// hatch for a runner that cannot reach the console, and for the imaging
	// station, which substitutes before any device credential exists.
	if s.cacheToken != "" &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(s.cacheToken)) == 1 {
		return true
	}
	return s.consoleURL != "" && s.deviceCredentialValid(r.Context(), pass)
}

// cacheVerifyTTL is how long a verdict is reused. Short enough that revoking a
// device's credential takes effect within minutes, long enough that a rollout
// does not hammer the console: one substitution fetches hundreds of paths.
const cacheVerifyTTL = 5 * time.Minute

// deviceCredentialValid asks the console whether this is a live device
// credential, remembering the answer briefly.
//
// Only POSITIVE answers are cached. Caching a rejection would make a device
// that has just been re-credentialled wait out the TTL for no reason, and the
// cost of re-asking on failure is paid by whoever is presenting a bad
// credential, which is the right person to make wait.
func (s *server) deviceCredentialValid(ctx context.Context, cred string) bool {
	sum := sha256.Sum256([]byte(cred))
	key := hex.EncodeToString(sum[:])

	s.verifiedMu.Lock()
	until, ok := s.verified[key]
	s.verifiedMu.Unlock()
	if ok && time.Now().Before(until) {
		return true
	}

	body, _ := json.Marshal(map[string]string{"credential": cred})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.consoleURL+"/api/v1/device-auth", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		// Fail CLOSED. An unreachable console must not open the cache: the
		// device falls back to building locally, which is slow and visible,
		// where opening up is fast and invisible.
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	s.verifiedMu.Lock()
	s.verified[key] = time.Now().Add(cacheVerifyTTL)
	// Bounded: a stream of bogus credentials must not grow this map forever.
	// Positives only ever come from real devices, so the cap is generous.
	if len(s.verified) > 4096 {
		for k, exp := range s.verified {
			if time.Now().After(exp) {
				delete(s.verified, k)
			}
		}
	}
	s.verifiedMu.Unlock()
	return true
}
