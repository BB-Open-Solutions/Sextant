package nix

import (
	"context"
	"fmt"
	"strings"
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
	// ChunkSize caps how many host toplevels one nix invocation realises.
	//
	// The evaluation gate has been batched since it OOM-killed the runner;
	// this path never was, so a ring's release loaded every member's toplevel
	// into a single evaluator regardless of size. It is the same failure
	// waiting on a bigger fleet, and worse: when the gate dies mid-build,
	// nothing in the whole control plane can commit until it comes back.
	//
	// Zero means one invocation - today's behaviour, so enabling batching is
	// a deliberate act and not a silent change of what a release does.
	ChunkSize int

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
	// Both steps batch, and both keep going host by host rather than holding
	// the whole ring in one process.
	dest := fmt.Sprintf("file://%s?secret-key=%s&compression=zstd", p.CacheDir, p.KeyFile)
	for _, batch := range chunk(targets, p.ChunkSize) {
		buildArgs := append([]string{"build", "--no-link", "--no-warn-dirty"}, batch...)
		if out, err := run(ctx, "nix", buildArgs...); err != nil {
			return buildFailure(ctx, "building the release", timeout, out)
		}
		copyArgs := append([]string{"copy", "--no-warn-dirty", "--to", dest}, batch...)
		if out, err := run(ctx, "nix", copyArgs...); err != nil {
			return buildFailure(ctx, "copying the release into the cache", timeout, out)
		}
	}
	return nil
}

// killed reports whether the output looks like a process the kernel or a
// supervisor stopped, rather than a build that failed on merit.
//
// Cheap and deliberately narrow: an OOM kill produces no nix diagnostic, so
// the tell is the absence of one plus the kernel's own word for it. Guessing
// more aggressively would relabel real build failures as infrastructure
// problems, which is the mistake this is fixing, in reverse.
func killed(out []byte) bool {
	s := string(out)
	if strings.Contains(s, "error:") {
		return false
	}
	for _, m := range []string{"signal: killed", "Killed", "out of memory", "Out of memory", "exit status 137"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
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
	// Killed, not rejected. An out-of-memory kill leaves no "error:" line at
	// all - the process simply stops - and the console reported that to the
	// operator as "build runner unreachable", which sends somebody looking at
	// the network for a memory problem. It is the same distinction the timeout
	// branch above exists for: WHY it stopped decides what to do next.
	if killed(out) {
		return fmt.Errorf("%s was killed, most likely out of memory - it was not rejected. "+
			"Lower the release chunk size or raise the runner's memory limit", what)
	}
	return &ports.ValidationError{Detail: sanitize(string(out))}
}
