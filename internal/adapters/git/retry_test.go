package git

import "testing"

func TestIsTransientNet(t *testing.T) {
	transient := []string{
		"fatal: unable to access 'https://x/': Could not resolve host: forgejo.bb-open.com",
		"ssh: connect to host x port 22: Connection refused",
		"fatal: the remote end hung up unexpectedly: early EOF",
	}
	for _, m := range transient {
		if !isTransientNet(m) {
			t.Errorf("should retry: %q", m)
		}
	}
	terminal := []string{
		"! [rejected] main -> main (non-fast-forward)",
		"fatal: repository 'x' not found",
		"error: insufficient permission",
	}
	for _, m := range terminal {
		if isTransientNet(m) {
			t.Errorf("must not retry: %q", m)
		}
	}
}
