package fleet

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// TestGeneratorContract runs the eval-only assertions in nix/tests.nix:
// the v3 generator's mkForce/mkDefault semantics, tie-breaks, additive
// apps, and the catalog export. Every assertion must be true; a false one
// names itself. Skips where nix is unavailable (CI provides it).
func TestGeneratorContract(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("nix not available")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("nix", "eval", root+"#lib.tests", "--json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr // warnings (dirty tree) must not pollute the JSON
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("nix eval .#lib.tests: %v\n%s", err, stderr.String())
	}
	var results map[string]bool
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(results) == 0 {
		t.Fatal("no assertions found")
	}
	names := make([]string, 0, len(results))
	for n := range results {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !results[n] {
			t.Errorf("generator assertion failed: %s", n)
		}
	}
}
