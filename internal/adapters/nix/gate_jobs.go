package nix

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// gate_jobs.go: evaluation through nix-eval-jobs instead of our own chunker.
//
// WHY. We wrote a chunker, an admission semaphore and a single evaluation
// mutex to keep one oversized evaluation from taking the runner down.
// nix-eval-jobs already does that, and does it at the right level: its
// -max-memory-size is PER WORKER, so an evaluation that outgrows its budget
// fails its own attribute while the service stays up. That distinction is the
// whole point. On 2026-08-01 a five-device fleet OOM-killed the gate three
// times, and because every commit path runs through /validate, nothing in the
// fleet could change until it came back.
//
// It is also faster, and our own configuration was the slow one. Five hosts,
// same overlay, same machine: one process for all five 35.6s, chunk-size 1 in
// five processes 47.6s, nix-eval-jobs with four workers 24.4s. Chunking made
// it worse because every chunk re-pays the fixed cost of loading nixpkgs and
// the module system.
//
// The per-attribute JSON it streams is a better answer than ours as well: an
// operator learns WHICH hosts failed and which passed, rather than that "the
// change was rejected".

// jobLine is one attribute's result. nix-eval-jobs writes one JSON object per
// line, as each worker finishes, so a large validation reports progressively
// instead of all at once.
type jobLine struct {
	Attr    string `json:"attr"`
	DrvPath string `json:"drvPath"`
	// Error carries nix's full evaluation trace when the attribute failed.
	// Its lines are INDENTED, so a naive strings.HasPrefix(line, "error:")
	// finds nothing - sanitize handles that, a hand-rolled scan would not.
	Error string `json:"error"`
}

// evalWithJobs forces every requested host's toplevel through nix-eval-jobs.
//
// The host set is spelled out in the expression rather than mapped over the
// whole attrset: the caller has already narrowed the blast radius to one
// representative per configuration shape, and evaluating the rest would throw
// that saving away.
func (g *EvalGate) evalWithJobs(ctx context.Context, run runner, repoDir string, names []string) error {
	ctx, cancel := g.withTimeout(ctx)
	defer cancel()

	// getFlake refuses an unlocked reference, and the candidate lives on a
	// detached commit in a scratch worktree that no branch points at. Naming
	// the revision locks it without reaching for --impure, which would let the
	// expression read the runner's environment.
	rev, err := g.headRev(ctx, run, repoDir)
	if err != nil {
		return err
	}
	expr, err := jobsExpr(repoDir, rev, names)
	if err != nil {
		return err
	}

	// --no-instantiate: validation asks whether the host EVALUATES, and does
	// not need the .drv written to prove it. The release build instantiates
	// separately when it realises the closure. Requires nix-eval-jobs 2.34+ -
	// the image pins one, and an older binary fails loudly on the unknown flag
	// rather than quietly doing more work.
	//
	// It also makes --gc-roots-dir moot: nothing is written, so there is
	// nothing for the collector to take.
	args := []string{
		"--expr", expr,
		"--workers", strconv.Itoa(g.workers()),
		"--no-instantiate",
	}
	if mb := g.MaxMemoryMB; mb > 0 {
		args = append(args, "--max-memory-size", strconv.Itoa(mb))
	}

	out, runErr := run(ctx, g.JobsBin, args...)
	// A non-zero exit means at least one attribute failed, and the failures are
	// in the output we just captured. Parse first and let the per-attribute
	// errors speak; fall back to the raw output only when there is nothing to
	// read, which is what a crashed or missing binary looks like.
	failed, parseErr := parseJobLines(out, names)
	if parseErr != nil {
		if runErr != nil {
			return &ports.ValidationError{Detail: sanitize(string(out))}
		}
		return parseErr
	}
	if len(failed) > 0 {
		return &ports.ValidationError{Detail: describeFailures(failed)}
	}
	if runErr != nil {
		return &ports.ValidationError{Detail: sanitize(string(out))}
	}
	return nil
}

// headRev reads the staged candidate's commit.
func (g *EvalGate) headRev(ctx context.Context, run runner, repoDir string) (string, error) {
	out, err := run(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("gate: read candidate revision: %w", err)
	}
	rev := strings.TrimSpace(string(out))
	if len(rev) != 40 {
		return "", fmt.Errorf("gate: candidate revision %q is not a commit id", rev)
	}
	return rev, nil
}

// jobsExpr builds the attrset nix-eval-jobs walks: one entry per requested
// host, whose value is that host's toplevel derivation.
func jobsExpr(repoDir, rev string, names []string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, `let f = builtins.getFlake "git+file://%s?rev=%s"; in `, repoDir, rev)
	b.WriteString(`builtins.listToAttrs (map (n: { name = n; value = f.nixosConfigurations.${n}.config.system.build.toplevel; }) [ `)
	for _, n := range names {
		// The same guard the chunked path applies: a host name reaches a nix
		// expression, so anything but the known-safe shape is refused rather
		// than quoted and hoped for.
		if !hostRe.MatchString(n) {
			return "", fmt.Errorf("invalid host %q", n)
		}
		fmt.Fprintf(&b, "%q ", n)
	}
	b.WriteString("])")
	return b.String(), nil
}

// parseJobLines reads the streamed results and returns the failures.
//
// It also insists every requested host was accounted for. A worker killed for
// exceeding its memory budget takes its attribute's line with it, and a
// validation that silently proved fewer hosts than it was asked to is exactly
// the kind of quiet half-answer this gate exists to prevent.
func parseJobLines(out []byte, want []string) (map[string]string, error) {
	failed := map[string]string{}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue // nix's own warnings and progress share the stream
		}
		var j jobLine
		if err := json.Unmarshal(line, &j); err != nil || j.Attr == "" {
			continue
		}
		seen[j.Attr] = true
		if j.Error != "" {
			failed[j.Attr] = sanitize(j.Error)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("gate: read evaluation output: %w", err)
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("gate: the evaluator produced no results")
	}
	for _, n := range want {
		if !seen[n] {
			failed[n] = "the evaluator produced no result for this host; it may have exceeded its memory budget"
		}
	}
	return failed, nil
}

// describeFailures renders the per-host verdicts an operator has to act on.
// Naming which hosts passed matters as much as which failed: it turns "your
// change was rejected" into "these two shapes are broken, the rest are fine".
func describeFailures(failed map[string]string) string {
	names := make([]string, 0, len(failed))
	for n := range failed {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	if len(names) == 1 {
		fmt.Fprintf(&b, "%s does not build:\n%s", names[0], failed[names[0]])
		return b.String()
	}
	fmt.Fprintf(&b, "%d hosts do not build (%s).\n", len(names), strings.Join(names, ", "))
	for _, n := range names {
		fmt.Fprintf(&b, "\n%s:\n%s\n", n, failed[n])
	}
	return strings.TrimRight(b.String(), "\n")
}

func (g *EvalGate) workers() int {
	if g.Workers > 1 {
		return g.Workers
	}
	return 1
}
