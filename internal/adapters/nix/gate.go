// Package nix implements the validation gate against the system nix binary.
// The gate is the safety property of the write path: no configuration
// reaches git unless the overlay's generator asserts and the NixOS module
// system accept it. Ported from the proven PoC gate.
package nix

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// runner executes a command and returns its combined output. Injectable so
// tests assert the exact invocation without a real nix evaluation.
type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	// #nosec G204 - name/args are the gate's own fixed nix invocation (code-controlled), passed as an argv slice with no shell.
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// EvalGate validates by forcing the toplevel DERIVATION (drvPath) of the
// affected hosts' nixosConfigurations. Forcing drvPath - not just attrNames,
// which never evaluates a config - runs the generator's asserts and the
// module system, catching unknown settings, bad types, non-existent option
// paths and any injection attempt. The full build (realising closures)
// remains the heavier CI gate.
type EvalGate struct {
	// Timeout bounds one evaluation. Zero means 120s.
	Timeout time.Duration
	// HostVariants lists the per-device host suffixes the overlay generator
	// emits (e.g. ["", "-sb"]); each host expands to tag+suffix. Nil means
	// just the tag itself.
	HostVariants []string

	run runner
}

// NewEvalGate returns the production gate.
func NewEvalGate() *EvalGate { return &EvalGate{run: execRunner} }

// Validate implements ports.Gate.
func (g *EvalGate) Validate(ctx context.Context, repoDir string, hosts []string) error {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := g.run
	if run == nil {
		run = execRunner
	}

	apply, err := g.applyExpr(hosts)
	if err != nil {
		return err
	}
	out, err := run(ctx, "nix", "eval", "--json", repoDir+"#nixosConfigurations",
		"--apply", apply, "--no-warn-dirty")
	if err != nil {
		return &ports.ValidationError{Detail: sanitize(string(out))}
	}
	return nil
}

// applyExpr builds the nix --apply expression. Scoped hosts keep the
// interactive gate fast; no hosts forces the whole set (e.g. a flake.lock
// update touches everything).
func (g *EvalGate) applyExpr(hosts []string) (string, error) {
	if len(hosts) == 0 {
		return "cfgs: builtins.attrValues (builtins.mapAttrs (_: c: c.config.system.build.toplevel.drvPath) cfgs)", nil
	}
	variants := g.HostVariants
	if len(variants) == 0 {
		variants = []string{""}
	}
	var b strings.Builder
	b.WriteString("cfgs: map (n: cfgs.${n}.config.system.build.toplevel.drvPath) [ ")
	for _, h := range hosts {
		for _, v := range variants {
			// hosts are validated slugs upstream; re-check here before
			// interpolation - this function must not trust that.
			hv := h + v
			if !hostRe.MatchString(hv) {
				return "", fmt.Errorf("invalid host %q", hv)
			}
			fmt.Fprintf(&b, "%q ", hv)
		}
	}
	b.WriteString("]")
	return b.String(), nil
}

// hostRe is defense-in-depth against nix expression injection: hosts are
// spliced into --apply/target expressions via %q, and callers already
// validate against fleet.ValidateSlug several layers away, but this
// function must not trust that a caller upstream did its job. Shared with
// build.go's targets.
var hostRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// storePathRe strips store-path noise so gate errors show the assert
// message, not derivation internals.
var storePathRe = regexp.MustCompile(`/nix/store/[a-z0-9]{32}-[^ '"\n]*`)

// sanitize trims nix eval output to the human-relevant rejection reason.
func sanitize(out string) string {
	out = storePathRe.ReplaceAllString(out, "<store>")
	lines := strings.Split(out, "\n")
	var keep []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "at ") || strings.HasPrefix(l, "warning:") {
			continue
		}
		keep = append(keep, l)
	}
	const maxLines = 12
	if len(keep) > maxLines {
		keep = keep[len(keep)-maxLines:]
	}
	if len(keep) == 0 {
		// Every line was filtered out (blank / "at " / "warning:" noise): a
		// rejection must never surface an empty Detail - that reads to the
		// operator as the gate itself being broken, exactly when they most
		// need a reason to act on.
		return "gate rejected the change (no parsable reason in nix output)"
	}
	return strings.Join(keep, "\n")
}
