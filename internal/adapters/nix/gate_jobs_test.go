package nix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// jsonLine renders one nix-eval-jobs result the way the binary streams it.
func jsonLine(attr, evalErr string) (string, error) {
	b, err := json.Marshal(jobLine{Attr: attr, Error: evalErr})
	return string(b), err
}

// errExit stands in for a non-zero exit from the evaluator.
func errExit(code int) error { return fmt.Errorf("exit status %d", code) }

func asValidation(err error, target **ports.ValidationError) bool {
	return errors.As(err, target)
}

// jobsRunner fakes the two commands the nix-eval-jobs path shells out to and
// records the nix-eval-jobs argv, so the tests assert the exact invocation
// without a nix evaluation.
type jobsRunner struct {
	out  string
	err  error
	args []string
}

func (r *jobsRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "git" {
		return []byte("a1b2c3d4e5f60718293a4b5c6d7e8f9012345678\n"), nil
	}
	r.args = append([]string{name}, args...)
	return []byte(r.out), r.err
}

func jobsGate(r *jobsRunner) *EvalGate {
	return &EvalGate{JobsBin: "nix-eval-jobs", Workers: 3, MaxMemoryMB: 2500, run: r.run}
}

func okLine(attr string) string {
	return `{"attr":"` + attr + `","drvPath":"/nix/store/x-` + attr + `.drv"}`
}

func TestJobsGateAcceptsAnOverlayEveryHostEvaluates(t *testing.T) {
	r := &jobsRunner{out: okLine("lt-1") + "\n" + okLine("lt-2") + "\n"}
	if err := jobsGate(r).Validate(context.Background(), "/data/validate", []string{"lt-1", "lt-2"}); err != nil {
		t.Fatalf("a valid overlay was rejected: %v", err)
	}

	argv := strings.Join(r.args, " ")
	for _, want := range []string{
		"nix-eval-jobs", "--workers 3", "--max-memory-size 2500",
		// Validation asks whether a host evaluates; writing the .drv is work
		// it does not need, and the release build instantiates separately.
		"--no-instantiate",
		// The revision locks the flake reference: the candidate sits on a
		// detached commit, and getFlake refuses an unlocked ref. Reaching for
		// --impure instead would hand the expression the runner's environment.
		"rev=a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("invocation is missing %q:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, "--impure") {
		t.Error("the gate evaluated impurely; the expression could then read the runner's environment")
	}
	// Only the hosts asked for. The caller already narrowed the change to one
	// representative per configuration shape; evaluating the whole fleet would
	// throw that away.
	if !strings.Contains(argv, `"lt-1"`) || !strings.Contains(argv, `"lt-2"`) {
		t.Errorf("requested hosts missing from the expression:\n%s", argv)
	}
}

// A rejection must name the host and carry the reason. The whole point of the
// per-attribute stream is that an operator learns which shape is broken.
func TestJobsGateNamesTheHostThatFailed(t *testing.T) {
	trace := `error:
       … while evaluating the option ` + "`environment.systemPackages'" + `

       error: device lt-2: package 'geen-pakket' does not exist in nixpkgs`
	body, _ := jsonLine("lt-2", trace)
	r := &jobsRunner{out: okLine("lt-1") + "\n" + body + "\n", err: errExit(5)}

	err := jobsGate(r).Validate(context.Background(), "/data/validate", []string{"lt-1", "lt-2"})
	if err == nil {
		t.Fatal("a broken host was accepted")
	}
	var ve *ports.ValidationError
	if !asValidation(err, &ve) {
		t.Fatalf("error is %T, want a ValidationError the console can render", err)
	}
	if !strings.Contains(ve.Detail, "lt-2") {
		t.Errorf("the message does not name the failing host:\n%s", ve.Detail)
	}
	if !strings.Contains(ve.Detail, "does not exist in nixpkgs") {
		t.Errorf("the causal line was lost:\n%s", ve.Detail)
	}
	if strings.Contains(ve.Detail, "lt-1") {
		t.Errorf("a host that evaluated fine was reported as broken:\n%s", ve.Detail)
	}
}

// A worker killed for exceeding its memory budget takes its attribute's line
// with it. Silently proving fewer hosts than asked is the quiet half-answer
// this gate exists to prevent, so a missing result is a failure.
func TestJobsGateRefusesWhenAHostProducedNoResult(t *testing.T) {
	r := &jobsRunner{out: okLine("lt-1") + "\n"}
	err := jobsGate(r).Validate(context.Background(), "/data/validate", []string{"lt-1", "lt-2"})
	if err == nil {
		t.Fatal("a change was accepted while one host was never evaluated")
	}
	if !strings.Contains(err.Error(), "lt-2") {
		t.Errorf("the message does not name the unevaluated host: %v", err)
	}
}

// No output at all is a missing or crashed binary, not a clean overlay.
func TestJobsGateRefusesAnEmptyStream(t *testing.T) {
	r := &jobsRunner{out: "", err: errExit(127)}
	if err := jobsGate(r).Validate(context.Background(), "/data/validate", []string{"lt-1"}); err == nil {
		t.Fatal("an evaluator that produced nothing was treated as success")
	}
}

// nix writes warnings and progress into the same stream. They must not be
// mistaken for results, and must not derail the parse.
func TestJobsGateIgnoresNonJSONNoise(t *testing.T) {
	r := &jobsRunner{out: "warning: `--gc-roots-dir' not specified\n" +
		"evaluation warning: 'system' has been renamed\n" + okLine("lt-1") + "\n"}
	if err := jobsGate(r).Validate(context.Background(), "/data/validate", []string{"lt-1"}); err != nil {
		t.Fatalf("stream noise was treated as a failure: %v", err)
	}
}

// A host name reaches a nix expression. Anything but the known-safe shape is
// refused rather than quoted and hoped for.
func TestJobsExprRefusesAnUnsafeHostName(t *testing.T) {
	if _, err := jobsExpr("/data/validate", strings.Repeat("a", 40), []string{`lt-1"; evil = `}); err == nil {
		t.Fatal("an unsafe host name was accepted into the expression")
	}
}
