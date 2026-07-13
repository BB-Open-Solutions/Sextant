package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"sync"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// downloads.go serves the command-line client (sxctl) straight from the
// console, so an operator can grab a CLI that matches the running server
// version instead of hunting for a release. The binary ships in the same
// container image; SEXTANT_CLI_PATH overrides its location for other layouts.

// cliPath returns where the sxctl binary lives in this deployment.
func cliPath() string {
	if p := os.Getenv("SEXTANT_CLI_PATH"); p != "" {
		return p
	}
	return "/usr/local/bin/sxctl"
}

var (
	cliSumOnce sync.Once
	cliSum     string
)

// cliChecksumHex returns the sha256 of the CLI binary, computed once. Empty
// if the binary is not present (e.g. a dev run without the packaged CLI).
func cliChecksumHex() string {
	cliSumOnce.Do(func() {
		f, err := os.Open(cliPath())
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return
		}
		cliSum = hex.EncodeToString(h.Sum(nil))
	})
	return cliSum
}

// downloadCLI streams the sxctl binary as an attachment. Any signed-in user
// may fetch it; it is a client, not privileged data.
func (s *Server) downloadCLI(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f, err := os.Open(cliPath())
	if err != nil {
		http.Error(w, "the command-line client is not bundled in this deployment", http.StatusNotFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="sxctl"`)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "sxctl", info.ModTime(), f)
}

// cliChecksum returns the sha256 of the offered binary as text, so a download
// can be verified (sha256sum -c) before it is trusted.
func (s *Server) cliChecksum(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	sum := cliChecksumHex()
	if sum == "" {
		http.Error(w, "the command-line client is not bundled in this deployment", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, sum+"  sxctl\n")
}
