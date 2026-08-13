package main

import (
	"bytes"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The committed page is generated output, like catalog.json and app.css. A
// manual that is edited by hand drifts on the first flag that changes, and
// nobody finds out until somebody follows it.
func TestManPageIsNotStale(t *testing.T) {
	var got bytes.Buffer
	if err := writeMan(&got); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../docs/man/sxctl.1")
	if err != nil {
		t.Fatalf("read the committed page: %v", err)
	}
	if got.String() != string(want) {
		t.Error("docs/man/sxctl.1 is out of date - run 'just man'")
	}
}

// dispatchCase finds the resources the CLI actually answers to.
var dispatchCase = regexp.MustCompile(`(?m)^\tcase "([a-z]+)":`)

// A resource that dispatch handles but the manual never names is a command
// nobody can discover. This reads the switch rather than a list somebody
// maintains, so adding a resource without documenting it fails here.
func TestEveryResourceIsInTheManual(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	block := commandBlock()
	if strings.TrimSpace(block) == "" {
		t.Fatal("the manual has no command section at all")
	}

	var missing []string
	seen := map[string]bool{}
	for _, m := range dispatchCase.FindAllStringSubmatch(string(src), -1) {
		res := m[1]
		if seen[res] {
			continue
		}
		seen[res] = true
		// The resource must open a line of the command block, which is how
		// the block lists them - a word appearing inside some other verb's
		// arguments does not count as documented.
		found := false
		for _, ln := range strings.Split(block, "\n") {
			if strings.HasPrefix(strings.TrimSpace(ln), res+" ") {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, res)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("dispatch answers to %v, and the manual never mentions them", missing)
	}
	if len(seen) < 10 {
		t.Errorf("only %d resources found in dispatch; the pattern probably stopped matching", len(seen))
	}
}

// The page has to be roff, not prose with a heading: a missing .TH means man
// renders it as a plain file and every section title disappears.
func TestManPageIsRoff(t *testing.T) {
	var b bytes.Buffer
	if err := writeMan(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.HasPrefix(out, ".TH SXCTL 1 ") {
		t.Error("no .TH header; man will not treat this as a manual page")
	}
	for _, sec := range []string{".SH NAME", ".SH SYNOPSIS", ".SH COMMANDS", ".SH ENVIRONMENT", ".SH EXIT STATUS"} {
		if !strings.Contains(out, sec) {
			t.Errorf("missing section %s", sec)
		}
	}
	// A line starting with a bare "-" inside .nf would be read as a request.
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "'") {
			t.Errorf("line starts with an apostrophe, which roff reads as a request: %q", ln)
		}
	}
}
