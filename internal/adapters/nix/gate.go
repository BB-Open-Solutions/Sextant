// Package nix implements the validation gate against the system nix binary.
// The gate is the safety property of the write path: no configuration
// reaches git unless the overlay's generator asserts and the NixOS module
// system accept it. Ported from the proven PoC gate.
package nix

import (
	"context"
	"encoding/json"
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
	// Timeout bounds one evaluation (one batch). Zero means 120s.
	Timeout time.Duration
	// HostVariants lists the per-device host suffixes the overlay generator
	// emits (e.g. ["", "-sb"]); each host expands to tag+suffix. Nil means
	// just the tag itself.
	HostVariants []string
	// ChunkSize caps how many host toplevels are forced in a single nix
	// process, so peak memory stays flat regardless of fleet size: a large
	// host set is evaluated in sequential batches instead of loading every
	// toplevel into one evaluator (which OOM-killed the runner at scale).
	// Zero means defaultChunkSize.
	ChunkSize int

	run runner
}

// defaultChunkSize bounds one nix process to a batch of host toplevels. Sized
// so a batch fits comfortably under the runner's memory limit; a fleet of any
// size is validated as ceil(N/size) sequential evaluations.
const defaultChunkSize = 50

// NewEvalGate returns the production gate.
func NewEvalGate() *EvalGate { return &EvalGate{run: execRunner} }

// Validate implements ports.Gate. It resolves the change's blast radius to a
// concrete set of host-attr names, then forces each host's toplevel derivation
// in memory-bounded batches. Batching preserves the full guarantee (every
// affected host is evaluated) while keeping a 10k-host org-wide change from
// loading 10k toplevels into a single evaluator.
func (g *EvalGate) Validate(ctx context.Context, repoDir string, hosts []string) error {
	run := g.run
	if run == nil {
		run = execRunner
	}

	names, err := g.resolveHosts(ctx, run, repoDir, hosts)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		// A change that builds no host (empty fleet, or a scope that resolved
		// to only retired devices) is vacuously valid: there is nothing whose
		// toplevel could fail to evaluate.
		return nil
	}

	size := g.ChunkSize
	if size <= 0 {
		size = defaultChunkSize
	}
	for start := 0; start < len(names); start += size {
		end := start + size
		if end > len(names) {
			end = len(names)
		}
		if err := g.evalNames(ctx, run, repoDir, names[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// resolveHosts returns the concrete host-attr names to force. An explicit host
// list is expanded by HostVariants; an empty list means the whole fleet,
// discovered from nix itself - builtins.attrNames forces no config so it is
// cheap, and it covers even hand-defined infra hosts that back no device
// (which enumerating the fleet's devices would miss).
func (g *EvalGate) resolveHosts(ctx context.Context, run runner, repoDir string, hosts []string) ([]string, error) {
	if len(hosts) > 0 {
		return g.expandVariants(hosts)
	}
	return g.allHostNames(ctx, run, repoDir)
}

// expandVariants turns each host tag into tag+suffix for every HostVariant,
// re-validating each name before it can be interpolated into a nix expression
// (this layer must not trust that an upstream caller validated the slug).
func (g *EvalGate) expandVariants(hosts []string) ([]string, error) {
	variants := g.HostVariants
	if len(variants) == 0 {
		variants = []string{""}
	}
	out := make([]string, 0, len(hosts)*len(variants))
	for _, h := range hosts {
		for _, v := range variants {
			hv := h + v
			if !hostRe.MatchString(hv) {
				return nil, fmt.Errorf("invalid host %q", hv)
			}
			out = append(out, hv)
		}
	}
	return out, nil
}

// allHostNames asks nix for the fleet's host-attr names. attrNames does not
// force any config, so this is cheap even for a huge fleet; the returned names
// are then evaluated in batches. A failure here means the flake does not even
// enumerate - itself a rejection.
func (g *EvalGate) allHostNames(ctx context.Context, run runner, repoDir string) ([]string, error) {
	ctx, cancel := g.withTimeout(ctx)
	defer cancel()
	out, err := run(ctx, "nix", "eval", "--json", repoDir+"#nixosConfigurations",
		"--apply", "builtins.attrNames", "--no-warn-dirty")
	if err != nil {
		return nil, &ports.ValidationError{Detail: sanitize(string(out))}
	}
	var names []string
	if err := json.Unmarshal(out, &names); err != nil {
		return nil, &ports.ValidationError{Detail: "gate could not read the host set from nix"}
	}
	return names, nil
}

// evalNames forces the toplevel drvPath of one batch of host names in a single
// nix process.
func (g *EvalGate) evalNames(ctx context.Context, run runner, repoDir string, names []string) error {
	ctx, cancel := g.withTimeout(ctx)
	defer cancel()
	apply, err := applyExprExact(names)
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

// applyExprExact builds the nix --apply expression that forces the toplevel
// DERIVATION (drvPath) of exactly the given host names. Forcing drvPath - not
// attrNames, which never evaluates a config - runs the generator's asserts and
// the module system, catching unknown settings, bad types, non-existent option
// paths and any injection attempt. Every name is re-validated against hostRe
// before interpolation.
func applyExprExact(names []string) (string, error) {
	var b strings.Builder
	b.WriteString("cfgs: map (n: cfgs.${n}.config.system.build.toplevel.drvPath) [ ")
	for _, n := range names {
		if !hostRe.MatchString(n) {
			return "", fmt.Errorf("invalid host %q", n)
		}
		fmt.Fprintf(&b, "%q ", n)
	}
	b.WriteString("]")
	return b.String(), nil
}

// withTimeout bounds a single batch evaluation.
func (g *EvalGate) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return context.WithTimeout(ctx, timeout)
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
