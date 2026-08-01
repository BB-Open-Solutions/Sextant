package web

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"identity.bindSecret": "identity-bindsecret",
		"netbird.setupKey":    "netbird-setupkey",
		"  ..A_b/C..  ":       "a-b-c",
		"":                    "",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// distillGateError must surface the actionable last "error:" line from a noisy
// nix trace, so an operator sees "unknown hardware profile" instead of a wall
// of eval frames. This is the whole point of the gate-error fold in the UI.
func TestDistillGateError(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "last error line wins",
			in: "trace: while evaluating the attribute\n" +
				"error: builder for '/nix/store/x.drv' failed\n" +
				"       error: device lt-9: unknown hardware profile 'framework-99'",
			want: "device lt-9: unknown hardware profile 'framework-99'",
		},
		{
			name: "strips stack-trace suffix",
			in:   "error: assertion failed (stack trace truncated; use --show-trace)",
			want: "assertion failed",
		},
		{
			name: "case-insensitive match",
			in:   "ERROR: something broke",
			want: "something broke",
		},
		{
			name: "no error line falls back to whole detail",
			in:   "  just a warning line  ",
			want: "just a warning line",
		},
		{
			name: "empty detail yields a friendly default",
			in:   "   \n  ",
			want: "The change was rejected by the validation gate.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ports.DistillGateError(c.in); got != c.want {
				t.Fatalf("distillGateError = %q, want %q", got, c.want)
			}
		})
	}
}

// classifyActionError decides the HTTP status and whether a technical detail is
// exposed. A forbidden shows its own reason at 403 with no detail; a gate
// rejection shows the distilled line at 422 and keeps the full trace as detail;
// a write race and an unavailable dependency get friendly lines at their own
// codes; anything else is a plain 400 validation message.
func TestClassifyActionError(t *testing.T) {
	gate := &ports.ValidationError{Detail: "trace\nerror: bad option 'foo'"}
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
		wantDetail string
	}{
		{"forbidden", &webForbidden{"requires Owner at org (you hold Viewer)"},
			http.StatusForbidden, "requires Owner at org (you hold Viewer)", ""},
		{"gate rejection", gate,
			http.StatusUnprocessableEntity, "bad option 'foo'", gate.Detail},
		{"write race", fmt.Errorf("wrap: %w", ports.ErrConflict),
			http.StatusConflict, "Another change landed first. Reload and try again.", ""},
		{"dependency down", fmt.Errorf("wrap: %w", ports.ErrUnavailable),
			http.StatusServiceUnavailable, "A dependency is temporarily unavailable. Try again shortly.", ""},
		{"plain validation", errors.New("name already taken"),
			http.StatusBadRequest, "name already taken", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, msg, detail := classifyActionError(c.err)
			if status != c.wantStatus || msg != c.wantMsg || detail != c.wantDetail {
				t.Fatalf("classifyActionError = (%d, %q, %q), want (%d, %q, %q)",
					status, msg, detail, c.wantStatus, c.wantMsg, c.wantDetail)
			}
		})
	}
}
