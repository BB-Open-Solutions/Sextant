package web_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every imaging status must have its own branch on the enrollment page.
//
// They did not. The template handled four of nine and let the rest fall into
// an else that renders "pending", so a job that had FINISHED read as one that
// had not started - and sb-pending, which is waiting on a person to toggle
// firmware at the machine, read as waiting on the station. Somebody would sit
// watching a queue that was waiting for them.
//
// This test reads both files rather than rendering, because the failure is
// structural: the domain gains a status, nothing breaks, and the new state
// quietly starts displaying as "pending". A build error would be better, but a
// test that names the gap is what a template can have.
func TestEveryImagingStatusHasItsOwnBranch(t *testing.T) {
	domain, err := os.ReadFile("../../domain/imaging/imaging.go")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := os.ReadFile("templates/enroll.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(tmpl)

	// The literal values, taken from the domain so the two cannot drift.
	statuses := regexp.MustCompile(`Status = "([a-z0-9-]+)"`).FindAllStringSubmatch(string(domain), -1)
	if len(statuses) < 5 {
		t.Fatalf("found %d statuses in the domain; the pattern stopped matching", len(statuses))
	}
	for _, m := range statuses {
		s := m[1]
		if s == "pending" {
			continue // pending is the else branch, by definition
		}
		if !strings.Contains(page, `"`+s+`"`) {
			t.Errorf("status %q has no branch on the enrollment page; it will render as \"pending\", "+
				"which is wrong for a finished job and dangerous for one waiting on a human", s)
		}
	}
}
