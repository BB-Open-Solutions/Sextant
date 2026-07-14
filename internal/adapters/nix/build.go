package nix

import (
	"context"
	"fmt"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Builder implements ports.Builder by realising the affected hosts' system
// closures with nix build. This is the heavy gate a change request must
// pass before merge - it proves the configuration actually builds, beyond
// what the eval gate checks.
type Builder struct {
	// Timeout bounds one build run. Zero means 30 minutes.
	Timeout time.Duration
	// HostVariants mirrors EvalGate.HostVariants.
	HostVariants []string

	run runner
}

// NewBuilder returns the production builder.
func NewBuilder() *Builder { return &Builder{run: execRunner} }

// Build implements ports.Builder. Hosts must be non-empty: see targets for
// why a whole-set build has no valid flake target.
func (b *Builder) Build(ctx context.Context, repoDir string, hosts []string) error {
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := b.run
	if run == nil {
		run = execRunner
	}

	targets, err := b.targets(repoDir, hosts)
	if err != nil {
		return err
	}
	args := append([]string{"build", "--no-link", "--no-warn-dirty"}, targets...)
	if out, err := run(ctx, "nix", args...); err != nil {
		return &ports.ValidationError{Detail: sanitize(string(out))}
	}
	return nil
}

// targets expands hosts (with variants) into flake build attrs. Unlike the
// eval gate - which can force every drvPath in one go via mapAttrs over the
// whole nixosConfigurations attrset - a real build needs concrete output
// paths: "repoDir#nixosConfigurations" alone names an attrset, not a
// derivation, so "nix build" on it fails with "not a derivation or path".
// There is no flake-side "build everything" attribute to fall back to, so
// an empty host list is a caller error here rather than a whole-set build;
// the eval gate already validates the whole set on every apply, and in
// practice the realisation build is always scoped to the hosts a change
// affects.
func (b *Builder) targets(repoDir string, hosts []string) ([]string, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("build target must name at least one host")
	}
	variants := b.HostVariants
	if len(variants) == 0 {
		variants = []string{""}
	}
	var out []string
	for _, h := range hosts {
		for _, v := range variants {
			// hosts are validated slugs upstream; re-check here before
			// interpolation - this function must not trust that.
			hv := h + v
			if !hostRe.MatchString(hv) {
				return nil, fmt.Errorf("invalid host %q", hv)
			}
			out = append(out, fmt.Sprintf(
				"%s#nixosConfigurations.%q.config.system.build.toplevel", repoDir, hv))
		}
	}
	return out, nil
}
