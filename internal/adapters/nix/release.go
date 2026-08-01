package nix

import (
	"context"
	"fmt"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Publisher realises hosts' system closures and copies them - signed - into
// the organisation's binary cache (build-before-promote). The cache is a
// nix binary-cache directory (narinfo + nar layout) the runner serves over
// HTTP; devices list it as a substituter and download the release instead of
// each compiling it locally.
type Publisher struct {
	// Timeout bounds one publish run (build + copy). Zero means 45 minutes.
	Timeout time.Duration
	// HostVariants mirrors EvalGate.HostVariants.
	HostVariants []string
	// CacheDir is the binary-cache directory to publish into.
	CacheDir string
	// KeyFile is the path to the nix signing secret key; the copy signs every
	// published path with it, so devices can pin the matching public key.
	KeyFile string

	run runner
}

// NewPublisher returns the production publisher.
func NewPublisher(cacheDir, keyFile string) *Publisher {
	return &Publisher{CacheDir: cacheDir, KeyFile: keyFile, run: execRunner}
}

// Publish builds the hosts' toplevels from the repo at dir and copies the
// closures into the cache. Idempotent: nix skips paths already built and
// already present in the cache.
func (p *Publisher) Publish(ctx context.Context, repoDir string, hosts []string) error {
	if p.CacheDir == "" || p.KeyFile == "" {
		return fmt.Errorf("publisher not configured (cache dir and signing key required)")
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := p.run
	if run == nil {
		run = execRunner
	}

	b := Builder{HostVariants: p.HostVariants}
	targets, err := b.targets(repoDir, hosts)
	if err != nil {
		return err
	}

	// Realise first: nix copy does not build, it only copies existing paths.
	buildArgs := append([]string{"build", "--no-link", "--no-warn-dirty"}, targets...)
	if out, err := run(ctx, "nix", buildArgs...); err != nil {
		return buildFailure(ctx, "building the release", timeout, out)
	}

	// Copy the closures into the cache, signing every path with the org key.
	dest := fmt.Sprintf("file://%s?secret-key=%s&compression=zstd", p.CacheDir, p.KeyFile)
	copyArgs := append([]string{"copy", "--no-warn-dirty", "--to", dest}, targets...)
	if out, err := run(ctx, "nix", copyArgs...); err != nil {
		return buildFailure(ctx, "copying the release into the cache", timeout, out)
	}
	return nil
}

// buildFailure turns a failed nix run into an error that says which KIND of
// failure it was.
//
// Running out of time is not a rejection, and reporting it as one sends an
// operator hunting for a configuration error that does not exist. It is
// distinguishable: on a deadline the process is killed mid-stream, so the
// output ends in progress lines with no "error:" anywhere - which is exactly
// what a halted rollout showed on 2026-08-01, twelve identical build lines and
// nothing to act on.
func buildFailure(ctx context.Context, what string, timeout time.Duration, out []byte) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s ran out of time after %s - the release was not rejected, it was cut off. "+
			"Raise the publisher timeout, or warm the cache so there is less to build", what, timeout)
	}
	return &ports.ValidationError{Detail: sanitize(string(out))}
}
