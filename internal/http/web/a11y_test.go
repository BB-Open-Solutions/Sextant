package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// a11y_test.go turns the structural half of the accessibility audit
// (docs/compliance/accessibility-audit.md) into a ratchet.
//
// WCAG 2.2 AA is a legal obligation for this product, and the first
// measurement on 2026-08-07 found 73 of 146 form fields with no accessible
// name. That number is too large to fix in one sitting and far too large to
// leave unwatched, so the baselines below are CEILINGS: the count may fall
// and must never rise. Lower the ceiling whenever the real number drops.
//
// This does not prove conformance. It proves the mechanical part is not
// getting worse while the manual round (keyboard, screen reader, contrast,
// reflow) is still outstanding.

const (
	// maxUnlabelledFields: 73 when first measured on 2026-08-07, ZERO by the
	// end of the same day. Now that it is zero the ceiling is an absolute
	// rule rather than a ratchet: every new form field must arrive with an
	// accessible name, and a field that does not fails this test on the
	// commit that introduces it rather than in an audit a year later.
	maxUnlabelledFields = 0
	// maxIconOnlyButtons: 11 when first measured, zero by the end of the same
	// day. A button whose only content is a Material symbol announces as
	// "button" or as the ligature name - a delete control that says nothing.
	maxIconOnlyButtons = 0
)

var (
	fieldRE     = regexp.MustCompile(`(?s)<(input|select|textarea)\b[^>]*>`)
	labelSpanRE = regexp.MustCompile(`(?s)<label\b.*?</label>`)
	buttonRE    = regexp.MustCompile(`(?s)<button\b.*?</button>`)
	iconSpanRE  = regexp.MustCompile(`(?s)<span[^>]*material-symbols-outlined[^>]*>.*?</span>`)
	tagRE       = regexp.MustCompile(`(?s)<[^>]+>`)
	actionRE    = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	skipTypeRE  = regexp.MustCompile(`type=["'](hidden|submit|button)["']`)
	idRE        = regexp.MustCompile(`id="([^"]+)"`)
	nameRE      = regexp.MustCompile(`name="([^"]+)"`)
)

func templateFiles(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob("templates/*.html")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no templates found (%v); this check would silently pass on nothing", err)
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(p)] = string(b)
	}
	return out
}

// TestEveryFormFieldHasAnAccessibleName is WCAG 3.3.2 and 4.1.2. A field
// with no name reaches a screen-reader user as "edit text, blank".
func TestEveryFormFieldHasAnAccessibleName(t *testing.T) {
	var offenders []string
	for file, s := range templateFiles(t) {
		spans := labelSpanRE.FindAllStringIndex(s, -1)
		inside := func(p int) bool {
			for _, sp := range spans {
				if p >= sp[0] && p <= sp[1] {
					return true
				}
			}
			return false
		}
		for _, m := range fieldRE.FindAllStringIndex(s, -1) {
			tag := s[m[0]:m[1]]
			if skipTypeRE.MatchString(tag) {
				continue
			}
			if strings.Contains(tag, "aria-label") || inside(m[0]) {
				continue
			}
			if id := idRE.FindStringSubmatch(tag); id != nil &&
				strings.Contains(s, `for="`+id[1]+`"`) {
				continue
			}
			name := "?"
			if n := nameRE.FindStringSubmatch(tag); n != nil {
				name = n[1]
			}
			offenders = append(offenders, file+" "+name)
		}
	}
	if len(offenders) > maxUnlabelledFields {
		t.Errorf("form fields with no accessible name: %d, ceiling %d\nnew ones since the baseline are in:\n  %s",
			len(offenders), maxUnlabelledFields, strings.Join(offenders, "\n  "))
	}
	if len(offenders) < maxUnlabelledFields {
		t.Logf("down to %d unlabelled fields (ceiling %d) - lower maxUnlabelledFields to %d",
			len(offenders), maxUnlabelledFields, len(offenders))
	}
}

// TestIconOnlyButtonsAreLabelled is WCAG 4.1.2.
func TestIconOnlyButtonsAreLabelled(t *testing.T) {
	var offenders []string
	for file, s := range templateFiles(t) {
		for _, b := range buttonRE.FindAllString(s, -1) {
			if strings.Contains(b, "aria-label") {
				continue
			}
			stripped := iconSpanRE.ReplaceAllString(b, "")
			text := actionRE.ReplaceAllString(tagRE.ReplaceAllString(stripped, ""), "X")
			if strings.TrimSpace(text) == "" {
				offenders = append(offenders, file)
			}
		}
	}
	if len(offenders) > maxIconOnlyButtons {
		t.Errorf("icon-only buttons with no accessible name: %d, ceiling %d (%v)",
			len(offenders), maxIconOnlyButtons, offenders)
	}
	if len(offenders) < maxIconOnlyButtons {
		t.Logf("down to %d icon-only buttons - lower maxIconOnlyButtons to %d", len(offenders), len(offenders))
	}
}

// TestEveryPageDeclaresItsLanguage is WCAG 3.1.1. Two documents carry an
// <html> element; both must declare a language, and the one a user sees in
// their own language must not hardcode another.
func TestEveryPageDeclaresItsLanguage(t *testing.T) {
	for file, s := range templateFiles(t) {
		i := strings.Index(s, "<html")
		if i < 0 {
			continue // a fragment, not a document
		}
		end := strings.Index(s[i:], ">")
		tag := s[i : i+end]
		if !strings.Contains(tag, "lang=") {
			t.Errorf("%s: <html> declares no language", file)
			continue
		}
		if strings.Contains(tag, `lang="en"`) && !strings.Contains(tag, "{{") {
			t.Errorf("%s: <html lang=\"en\"> is hardcoded; a Dutch page is announced with English phonetics", file)
		}
	}
}

// TestOneHeadingLevelOnePerPage is WCAG 1.3.1 in practice: two h1 elements
// leave a screen-reader user with no reliable "top of this page".
func TestOneHeadingLevelOnePerPage(t *testing.T) {
	for file, s := range templateFiles(t) {
		if n := strings.Count(s, "<h1"); n > 1 {
			t.Errorf("%s has %d <h1> elements", file, n)
		}
	}
}

// TestTheLayoutOffersASkipLink is WCAG 2.4.1. Both halves are asserted: a
// skip link pointing at an element that cannot receive focus moves the
// viewport and leaves the focus ring behind, which is worse than none - the
// user believes they have moved and their next Tab goes back to the sidebar.
func TestTheLayoutOffersASkipLink(t *testing.T) {
	files := templateFiles(t)
	layout, ok := files["layout.html"]
	if !ok {
		t.Fatal("layout.html not found")
	}
	if !strings.Contains(layout, `href="#content"`) {
		t.Error("the shared layout has no skip link; every page makes a keyboard user tab the whole sidebar")
	}
	if !strings.Contains(layout, `id="content"`) {
		t.Error("nothing carries id=\"content\": the skip link points nowhere")
	}
	if !strings.Contains(layout, `tabindex="-1"`) {
		t.Error("the skip target cannot receive focus; the viewport moves and the focus ring does not")
	}
}
