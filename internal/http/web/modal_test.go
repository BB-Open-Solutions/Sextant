package web_test

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The edit windows are checkbox-and-label, because the strict CSP allows no
// script. That buys a window with no JavaScript and costs two things a test
// has to hold up, since neither shows as a broken page:
//
//   - the window must be the toggle's IMMEDIATE next sibling (the stylesheet
//     matches with "+"). With "~" every later window on the page opened at
//     once and the last one drawn is the one you saw, so clicking "packages"
//     showed the overlays list.
//   - the window carries its own form, so it must not sit inside another one.
//     A form inside a form is invalid HTML; browsers drop the inner one and
//     the Save button silently does nothing.

// toggleThenModal matches an edit-window toggle followed by its window with
// nothing but whitespace between them.
var toggleThenModal = regexp.MustCompile(`(?s)<input type="checkbox" id="([^"]+)" class="modal-toggle">\s*<div class="modal">`)

var anyToggle = regexp.MustCompile(`id="([^"]+)" class="modal-toggle"`)

// TestEditWindowsAreAdjacentToTheirToggle pins the sibling relation the CSS
// depends on, on every page that has an edit window.
func TestEditWindowsAreAdjacentToTheirToggle(t *testing.T) {
	ts, _ := newConsole(t)
	// The seed fleet carries no policy, and a page with no policies has no
	// policy window to check. Create one, so the assertion covers both pages
	// rather than passing vacuously on one of them.
	if code := postForm(t, ts, "/policies", url.Values{
		"csrf": {"dev-csrf"}, "id": {"win-test"}, "settings": {"apps.office = true"},
	}); code != 303 {
		t.Fatalf("seeding a policy: status %d", code)
	}
	for _, path := range []string{"/settings?scope=org", "/policies"} {
		_, page := getPage(t, ts, path)
		all := anyToggle.FindAllStringSubmatch(page, -1)
		if len(all) == 0 {
			t.Fatalf("%s renders no edit window at all", path)
		}
		adjacent := map[string]bool{}
		for _, m := range toggleThenModal.FindAllStringSubmatch(page, -1) {
			adjacent[m[1]] = true
		}
		for _, m := range all {
			if !adjacent[m[1]] {
				t.Errorf("%s: window %q is not its toggle's next sibling; the stylesheet will not open it", path, m[1])
			}
			// A toggle nobody can reach is a window nobody can open.
			if !strings.Contains(page, `for="`+m[1]+`"`) {
				t.Errorf("%s: nothing labels toggle %q", path, m[1])
			}
		}
	}
}

// TestAppListReadsBeforeItEdits: the summary prints the first ten names and
// says how many are left, and the window holds the whole list one name per
// line. The old single comma-separated field could not be read top to bottom,
// which is how anybody checks a list - and a save wrote whatever that line
// happened to contain.
//
// The save below deliberately mixes separators: this is what a list pasted
// from a terminal or a spreadsheet looks like, and it used to arrive as one
// long nonsense package name.
func TestAppListReadsBeforeItEdits(t *testing.T) {
	ts, _ := newConsole(t)
	names := "firefox\nlibreoffice, vlc\ngimp\ninkscape\nkeepassxc\nthunderbird\ngit\nhtop\njq\nripgrep\nfd"
	if code := postForm(t, ts, "/apps", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"org"}, "kind": {"packages"}, "names": {names},
	}); code != 303 {
		t.Fatalf("saving the list: status %d", code)
	}
	_, page := getPage(t, ts, "/settings?scope=org")

	// Twelve names in, ten shown, two counted.
	if !strings.Contains(page, "2 more") {
		t.Error("the summary does not say how many names it left out")
	}
	if strings.Contains(page, ">ripgrep<") && strings.Contains(page, "2 more") {
		// ripgrep is the eleventh alphabetically-sorted name; seeing it in the
		// summary means the cap did not apply.
		if strings.Count(page, ">ripgrep<") > 1 {
			t.Error("the summary printed a name it claimed to have left out")
		}
	}
	// The window carries the whole list, one per line, including the two names
	// that arrived on one comma-separated line.
	window := regexp.MustCompile(`(?s)<textarea name="names"[^>]*>(.*?)</textarea>`).FindStringSubmatch(page)
	if window == nil {
		t.Fatal("no edit window for the app lists")
	}
	lines := strings.Split(strings.TrimSpace(window[1]), "\n")
	if len(lines) != 12 {
		t.Errorf("window holds %d lines, want 12 (one per name): %q", len(lines), window[1])
	}
	for _, want := range []string{"libreoffice", "vlc"} {
		if !strings.Contains(window[1], want+"\n") && !strings.HasSuffix(strings.TrimSpace(window[1]), want) {
			t.Errorf("%q did not survive as its own entry", want)
		}
	}
}

// TestEmptyAppListSaysSo: an empty list must read as a statement, not as a
// blank space somebody has to interpret.
func TestEmptyAppListSaysSo(t *testing.T) {
	ts, _ := newConsole(t)
	_, page := getPage(t, ts, "/settings?scope=org")
	if !strings.Contains(page, "nothing at this scope") {
		t.Error("an empty app list renders as nothing at all")
	}
}

// TestNoNestedForms: the settings page carries one big save form plus the app
// edit windows, and the policies page carries a form per policy. Nesting any
// of them would make the inner Save a no-op in every browser.
func TestNoNestedForms(t *testing.T) {
	ts, _ := newConsole(t)
	for _, path := range []string{"/settings?scope=org", "/policies", "/compliance", "/devices"} {
		_, page := getPage(t, ts, path)
		depth, line := 0, 0
		for _, tok := range regexp.MustCompile(`</?form\b`).FindAllString(page, -1) {
			if tok == "<form" {
				depth++
				if depth > 1 {
					t.Errorf("%s: a form opens inside another form (occurrence %d)", path, line)
				}
			} else {
				depth--
			}
			line++
		}
		if depth != 0 {
			t.Errorf("%s: unbalanced form tags (depth %d at end)", path, depth)
		}
	}
}

// TestStylesheetOpensOnlyTheAdjacentWindow guards the exact regression above
// in the built stylesheet, which is the artefact that actually ships.
func TestStylesheetOpensOnlyTheAdjacentWindow(t *testing.T) {
	css, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".modal-toggle:checked+.modal") {
		t.Error("no adjacent-sibling rule: the edit windows cannot open")
	}
	if strings.Contains(string(css), ".modal-toggle:checked~.modal") {
		t.Error("general-sibling rule is back: one toggle opens every window on the page")
	}
}
