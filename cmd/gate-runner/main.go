// Command gate-runner is the nix-capable validation gate for the console.
// The console image ships no nix (small, sovereign); when it runs with
// --gate=remote it POSTs candidate configurations here. This service keeps a
// warm clone of the overlay repo, drops in the candidate fleet.json, and
// forces the affected hosts' toplevel derivation with `nix eval` - the same
// safety property as the in-process EvalGate, isolated in a nix runtime.
//
// It is intentionally tiny: one endpoint, one serialized evaluation at a
// time (a single overlay working tree), fail-closed by construction.
package main

import (
	"context"
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
	"strings"
	"sync"
	"syscall"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/nix"
)

func main() {
	var (
		addr      = flag.String("addr", envOr("GATE_ADDR", "0.0.0.0:8090"), "listen address")
		workdir   = flag.String("workdir", envOr("GATE_WORKDIR", "/data/overlay"), "overlay clone directory")
		remote    = flag.String("remote", os.Getenv("GATE_OVERLAY_REMOTE"), "overlay git remote URL")
		branch    = flag.String("branch", envOr("GATE_OVERLAY_BRANCH", "main"), "overlay branch to track")
		variants  = flag.String("host-variants", os.Getenv("GATE_HOST_VARIANTS"), "comma-separated host suffixes, e.g. ,-sb")
		evalSecs  = flag.Int("eval-timeout", 120, "per-evaluation timeout, seconds")
		logFormat = flag.String("log-format", envOr("GATE_LOG_FORMAT", "text"), "log format: text|json")
	)
	flag.Parse()

	log := newLogger(*logFormat)
	if *remote == "" {
		log.Error("no overlay remote configured (GATE_OVERLAY_REMOTE)")
		os.Exit(2)
	}

	srv := &server{
		log:      log,
		workdir:  *workdir,
		remote:   *remote,
		branch:   *branch,
		variants: splitVariants(*variants),
		gate:     &nix.EvalGate{Timeout: time.Duration(*evalSecs) * time.Second},
	}
	if err := srv.ensureClone(context.Background()); err != nil {
		log.Error("initial overlay clone failed", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /validate", srv.handleValidate)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

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
		log.Info("gate-runner listening", "addr", *addr, "branch", *branch, "workdir", *workdir)
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

	mu sync.Mutex // one evaluation at a time; single overlay working tree
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

	s.mu.Lock()
	defer s.mu.Unlock()

	// Refresh the overlay so the generator and modules match the console's
	// base, then drop in the candidate fleet.json and evaluate.
	if err := s.sync(r.Context()); err != nil {
		s.log.Error("overlay sync failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, validateResponse{Error: "overlay sync failed"})
		return
	}
	if err := os.WriteFile(filepath.Join(s.workdir, "fleet.json"), []byte(req.Fleet), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, validateResponse{Error: "write candidate failed"})
		return
	}

	s.gate.HostVariants = s.variants
	if err := s.gate.Validate(r.Context(), s.workdir, req.Hosts); err != nil {
		// A ValidationError is the expected "generator said no" verdict.
		writeJSON(w, http.StatusUnprocessableEntity, validateResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, validateResponse{OK: true})
}

// ensureClone clones the overlay if the workdir is not yet a repo.
func (s *server) ensureClone(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(s.workdir, ".git")); err == nil {
		return s.sync(ctx)
	}
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

func (s *server) git(ctx context.Context, dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
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

func newLogger(format string) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}
