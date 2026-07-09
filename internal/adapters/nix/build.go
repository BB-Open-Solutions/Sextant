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

// Build implements ports.Builder. Empty hosts builds every host the flake
// exposes (via the eval expression forcing all drvPaths, then realising).
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

	targets := b.targets(repoDir, hosts)
	args := append([]string{"build", "--no-link", "--no-warn-dirty"}, targets...)
	if out, err := run(ctx, "nix", args...); err != nil {
		return &ports.ValidationError{Detail: sanitize(string(out))}
	}
	return nil
}

// targets expands hosts (with variants) into flake build attrs; empty hosts
// builds the whole set via the flake's nixosConfigurations.
func (b *Builder) targets(repoDir string, hosts []string) []string {
	if len(hosts) == 0 {
		// Realising every host: rely on the flake exposing a build-all
		// check or fall back to the bare flake (its default outputs).
		return []string{repoDir + "#nixosConfigurations"}
	}
	variants := b.HostVariants
	if len(variants) == 0 {
		variants = []string{""}
	}
	var out []string
	for _, h := range hosts {
		for _, v := range variants {
			out = append(out, fmt.Sprintf(
				"%s#nixosConfigurations.%q.config.system.build.toplevel", repoDir, h+v))
		}
	}
	return out
}
