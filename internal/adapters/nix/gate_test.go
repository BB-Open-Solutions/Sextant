package nix

import (
	"context"
	"errors"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

func TestValidateInvocation(t *testing.T) {
	var gotName string
	var gotArgs []string
	g := &EvalGate{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return []byte("ok"), nil
	}}
	if err := g.Validate(context.Background(), "/repo", []string{"lt-1"}); err != nil {
		t.Fatal(err)
	}
	if gotName != "nix" {
		t.Fatalf("ran %q", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "/repo#nixosConfigurations") {
		t.Errorf("flake ref missing: %s", joined)
	}
	if !strings.Contains(joined, `"lt-1"`) {
		t.Errorf("host not scoped: %s", joined)
	}
	if !strings.Contains(joined, "toplevel.drvPath") {
		t.Errorf("gate must force drvPath, not attrNames: %s", joined)
	}
}

func TestHostVariantsExpand(t *testing.T) {
	g := &EvalGate{HostVariants: []string{"", "-sb"}}
	expr, err := g.applyExpr([]string{"lt-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(expr, `"lt-1"`) || !strings.Contains(expr, `"lt-1-sb"`) {
		t.Fatalf("variants not expanded: %s", expr)
	}
}

func TestEmptyHostsForcesWholeSet(t *testing.T) {
	g := &EvalGate{}
	expr, err := g.applyExpr(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(expr, "mapAttrs") {
		t.Fatalf("whole-set expr wrong: %s", expr)
	}
}

func TestRejectionBecomesValidationError(t *testing.T) {
	g := &EvalGate{run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte("error: assertion failed: unknown setting key 'apps.bogus'\nat /nix/store/abc:1\n"), errors.New("exit 1")
	}}
	err := g.Validate(context.Background(), "/repo", nil)
	var verr *ports.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T %v", err, err)
	}
	if !strings.Contains(verr.Detail, "apps.bogus") {
		t.Errorf("detail lost the reason: %q", verr.Detail)
	}
	if strings.Contains(verr.Detail, "at /nix/store") {
		t.Errorf("store noise not stripped: %q", verr.Detail)
	}
}

func TestSanitizeStripsStorePathsAndTruncates(t *testing.T) {
	long := strings.Repeat("error line\n", 40) +
		"final: /nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-drv.drv rejected\n"
	out := sanitize(long)
	if strings.Contains(out, "/nix/store/aaaa") {
		t.Error("store path not replaced")
	}
	if got := len(strings.Split(out, "\n")); got > 12 {
		t.Errorf("output not truncated: %d lines", got)
	}
	if !strings.Contains(out, "rejected") {
		t.Error("tail (most relevant part) lost")
	}
}
