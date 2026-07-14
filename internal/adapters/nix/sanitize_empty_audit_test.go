package nix

import "testing"

// TestSanitizeNeverReturnsEmpty guards against a rejection with no visible
// reason: if nix output consists solely of filtered noise (blank lines,
// "at " frames, "warning:" lines), sanitize must fall back to a non-empty
// message rather than let ValidationError.Detail come out "" - an empty
// Detail reads to the operator as the gate itself being broken.
func TestSanitizeNeverReturnsEmpty(t *testing.T) {
	noisy := "\n\nat /nix/store/x-drv.drv:12:3\nwarning: deprecated option\n\n"
	got := sanitize(noisy)
	if got == "" {
		t.Fatal("sanitize returned an empty Detail")
	}
}

// TestSanitizeEmptyInputFallsBack: the degenerate empty-string case too.
func TestSanitizeEmptyInputFallsBack(t *testing.T) {
	if got := sanitize(""); got == "" {
		t.Fatal("sanitize(\"\") returned empty")
	}
}
