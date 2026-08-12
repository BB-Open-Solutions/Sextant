package web

import (
	"fmt"
	"html/template"
	"strings"
	"unicode"
	"unicode/utf8"
)

// diffLine is one classified line of a unified diff for the change viewer.
type diffLine struct {
	Kind string // add | del | hunk | meta | ctx
	Text string
}

// templateFuncs builds the template FuncMap: small template helpers. `list`
// lets a template iterate a literal set (e.g. the CLI toolbelt commands)
// without a data field.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"list":       func(items ...any) []any { return items },
		"hasPrefix":  strings.HasPrefix,
		"trimPrefix": strings.TrimPrefix,
		// policyName extracts the policy id from a resolved-setting provenance
		// string ("policy:<id>@<scope>"), so the device page can show "Policy:
		// baseline" instead of the raw "policy:baseline@org" the domain emits.
		"policyName": func(s string) string {
			name, _, _ := strings.Cut(strings.TrimPrefix(s, "policy:"), "@")
			return name
		},
		// renderValue renders a setting value the one canonical way (the
		// policy editor's key = value syntax), so a card and its edit form
		// never show the same value differently.
		"renderValue": renderValue,
		// macKey turns a MAC into a form-field-safe key (no colons) so a batch's
		// per-device CMDB-name input can be addressed as name-<macKey>.
		"macKey": macKey,
		// short trims a git revision to a readable 12-char prefix.
		"short": func(s string) string {
			if len(s) > 12 {
				return s[:12]
			}
			return s
		},
		// slug turns a setting key into a suggested secret-reference name, so a
		// secret field can deep-link to the Secrets page prefilled.
		"slug": slugify,
		// sentence and sentenceRest split a catalog description in two: the
		// opening sentence says WHAT a setting is and stays on the row, the
		// rest (why it exists, what it deliberately excludes, what it must not
		// be confused with) moves into the row's details panel. Catalog prose
		// is written to be read once and then never again, and a settings page
		// where every row carries a paragraph is a page nobody scans.
		"sentence":     func(s string) string { head, _ := splitSentence(s); return head },
		"sentenceRest": func(s string) string { _, rest := splitSentence(s); return rest },
		// initial is the uppercase first letter of a name, for the avatar
		// fallback when no profile photo is available.
		"initial": func(s string) string {
			for _, r := range strings.TrimSpace(s) {
				return strings.ToUpper(string(r))
			}
			return "?"
		},
		// contains reports whether a string slice holds v (e.g. is a
		// setting key in a policy's enforced/locked list).
		"contains": func(list []string, v string) bool {
			for _, s := range list {
				if s == v {
					return true
				}
			}
			return false
		},
		// difflines classifies a unified diff into coloured lines for the
		// change viewer (add/del/hunk/meta/context), CSP-safe via classes.
		"difflines": func(diff string) []diffLine {
			lines := strings.Split(diff, "\n")
			out := make([]diffLine, 0, len(lines))
			for _, ln := range lines {
				kind := "ctx"
				switch {
				case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"),
					strings.HasPrefix(ln, "diff "), strings.HasPrefix(ln, "index "):
					kind = "meta"
				case strings.HasPrefix(ln, "@@"):
					kind = "hunk"
				case strings.HasPrefix(ln, "+"):
					kind = "add"
				case strings.HasPrefix(ln, "-"):
					kind = "del"
				}
				out = append(out, diffLine{Kind: kind, Text: ln})
			}
			return out
		},
		// initials renders up to two uppercase initials from a display
		// name for the audit avatar (a display transform, not new data).
		"initials": initials,
		// indent maps a group-tree depth to a static padding class
		// (gd-0..gd-6, clamped). A class avoids inline style=, which
		// the CSP forbids.
		"indent": func(depth int) string {
			if depth < 0 {
				depth = 0
			}
			if depth > 6 {
				depth = 6
			}
			return fmt.Sprintf("gd-%d", depth)
		},
		// barW buckets a 0..100 percentage to the nearest 5 and returns a
		// static width class (bar-w-0..bar-w-100, defined in app.css),
		// avoiding inline style="width:N%" which the CSP forbids - same
		// pattern as indent above; barBucket (pipeline.go) feeds fractions
		// through the same helper.
		"barW": barClass,
	}
}

// initials renders up to two uppercase initials from a display name, for
// the audit avatar fallback when no profile photo is available. Each
// initial is the part's first RUNE, not its first byte: byte-slicing a
// multibyte name (e.g. a leading O-umlaut) would cut a lead byte off a
// rune and render invalid UTF-8/mojibake instead of the intended letter.
func initials(name string) string {
	parts := strings.Fields(name)
	var b strings.Builder
	count := 0
	for _, p := range parts {
		if count >= 2 {
			break
		}
		r, size := utf8.DecodeRuneInString(p)
		if size == 0 {
			continue
		}
		b.WriteString(strings.ToUpper(string(r)))
		count++
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}

// abbreviations are the trailing forms whose full stop does NOT end a
// sentence. Without them "a deployment has to say no on purpose (e.g. it
// ships a vault)" splits after "e.g." and the row shows half a clause.
var abbreviations = []string{"e.g.", "i.e.", "etc.", "cf.", "vs.", "approx.", "no.", "incl.", "resp."}

// splitSentence cuts a description after its first sentence and returns both
// halves; the rest is empty when there is only one sentence. A split needs a
// full stop (or ? / !) followed by whitespace AND an upper-case letter, which
// is what keeps "dawo.apps.office is enabled." intact - a dotted option path
// mid-sentence is followed by a lower-case word, never a capital.
func splitSentence(s string) (head, rest string) {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s)-1; i++ {
		if s[i] != '.' && s[i] != '!' && s[i] != '?' {
			continue
		}
		if s[i+1] != ' ' && s[i+1] != '\n' && s[i+1] != '\t' {
			continue
		}
		candidate := strings.TrimSpace(s[:i+1])
		lower := strings.ToLower(candidate)
		abbrev := false
		for _, a := range abbreviations {
			if strings.HasSuffix(lower, a) {
				abbrev = true
				break
			}
		}
		if abbrev {
			continue
		}
		tail := strings.TrimSpace(s[i+1:])
		if tail == "" {
			return candidate, ""
		}
		if r, _ := utf8.DecodeRuneInString(tail); !unicode.IsUpper(r) {
			continue
		}
		return candidate, tail
	}
	return s, ""
}

// barClass maps a 0..100 percentage onto its nearest-5% CSP-safe width
// class (bar-w-0..bar-w-100, app.css). The one place the bucketing rule
// lives - template bars and convergence bars must round identically.
func barClass(pct int) string {
	if pct < 0 {
		pct = 0
	}
	bucket := ((pct + 2) / 5) * 5
	if bucket > 100 {
		bucket = 100
	}
	return fmt.Sprintf("bar-w-%d", bucket)
}
