package web_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDownloadCLI serves a bundled sxctl as an attachment to a signed-in user.
func TestDownloadCLI(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sxctl")
	if err := os.WriteFile(bin, []byte("fake-binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEXTANT_CLI_PATH", bin)

	ts, _ := newConsole(t)
	c := client()
	resp, err := c.Get(ts.URL + "/downloads/sxctl")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("download = %d, want 200", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "sxctl") {
		t.Fatalf("Content-Disposition = %q, want an sxctl attachment", cd)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "fake-binary\n" {
		t.Fatalf("served %q, want the bundled binary bytes", body)
	}
}

// TestDownloadCLIAbsent 404s cleanly when no CLI is bundled, rather than 500.
func TestDownloadCLIAbsent(t *testing.T) {
	t.Setenv("SEXTANT_CLI_PATH", filepath.Join(t.TempDir(), "does-not-exist"))
	ts, _ := newConsole(t)
	c := client()
	resp, err := c.Get(ts.URL + "/downloads/sxctl")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("absent CLI = %d, want 404", resp.StatusCode)
	}
}
