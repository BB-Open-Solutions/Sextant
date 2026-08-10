package web

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// i18n_keys_test.go: a missing catalog key is not an error, it is a word.
//
// Localizer.T falls back to the English catalog and then returns THE KEY
// ITSELF, so a typo or a new state nobody translated renders
// "devices.config_frobnicated" into the page. Nothing fails, nothing logs,
// and the first person to notice is a user reading a variable name.
//
// Measured 2026-08-10: 878 distinct keys asked for, 975 defined, and nothing
// missing in either locale. That is a good state that nothing was keeping
// true. These tests keep it.

var (
	tmplKeyRE   = regexp.MustCompile(`\.L\.T\s+"([^"]+)"`)
	goCallKeyRE = regexp.MustCompile(`\.T\("([^"]+)"\)`)
	// Keys handed to the templates through a struct field rather than
	// written in the template - TitleKey, StepKeys, StatusKey. The template
	// renders them with {{$.L.T .SomeKey}}, so a static scan of the HTML
	// cannot see them at all.
	goFieldKeyRE  = regexp.MustCompile(`(?:Key|Keys)\s*:\s*(?:\[\]string\{)?[^,}]*?"([a-z][a-z0-9_]*\.[a-z0-9_.-]+)"`)
	goReturnKeyRE = regexp.MustCompile(`return\s+"([a-z][a-z0-9_]*\.[a-z0-9_.-]+)"`)
)

// assertKeys fails naming every key the catalog cannot answer.
func assertKeys(t *testing.T, source string, keys map[string]string) {
	t.Helper()
	var missing []string
	for key, where := range keys {
		if _, ok := catalog["en"][key]; !ok {
			missing = append(missing, key+" ("+where+")")
			continue
		}
		// English is the fallback, so a key present in en and absent in nl
		// still renders - in the wrong language, silently. TestCatalogParity
		// covers the catalog against itself; this covers what is used.
		if _, ok := catalog["nl"][key]; !ok {
			missing = append(missing, key+" (nl only, "+where+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%s: %d key(s) the catalog cannot answer, so the page shows the key:", source, len(missing))
		for _, m := range missing {
			t.Errorf("    %s", m)
		}
	}
}

func TestTemplateKeysAreAllDefined(t *testing.T) {
	keys := map[string]string{}
	for name, body := range templateFiles(t) {
		for _, m := range tmplKeyRE.FindAllStringSubmatch(body, -1) {
			keys[m[1]] = name
		}
	}
	// Guard against the check quietly measuring nothing: 878 on 2026-08-10,
	// and a regex change that stops matching would otherwise read as a pass.
	if len(keys) < 500 {
		t.Fatalf("only %d template keys found; the scan is broken, not the templates", len(keys))
	}
	assertKeys(t, "templates", keys)
}

func TestGoSuppliedKeysAreAllDefined(t *testing.T) {
	paths, err := goSources()
	if err != nil || len(paths) == 0 {
		t.Fatalf("no Go sources found (%v)", err)
	}
	keys := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{goCallKeyRE, goFieldKeyRE, goReturnKeyRE} {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				// Only strings that look like catalog keys AND that the
				// catalog knows a sibling of; a returned "foo.bar" from
				// unrelated code is not a translation key.
				if _, ok := catalog["en"][m[1]]; ok {
					keys[m[1]] = p
					continue
				}
				if looksLikeCatalogKey(m[1]) {
					keys[m[1]] = p
				}
			}
		}
	}
	assertKeys(t, "go sources", keys)
}

// TestDeviceConfigChipCoversItsWholeInputSpace drives the real function over
// every combination of its inputs rather than restating the constants here.
//
// The template renders this as `printf "devices.config_%s"`, which is the
// shape most likely to grow a value nobody translates: adding a case to
// deviceConfigState is a one-line change in a file that has nothing to do
// with the catalog. Six booleans is sixty-four combinations, so exhaustive
// is cheaper than representative.
func TestDeviceConfigChipCoversItsWholeInputSpace(t *testing.T) {
	const rev, other = "aaaa", "bbbb"
	seen := map[string]bool{}
	for i := range 64 {
		b := func(n int) bool { return i&(1<<n) != 0 }
		target := rev
		if b(0) {
			target = other
		}
		state := deviceConfigState(rev, target, b(1), b(2), b(3), b(4))
		if state == "" {
			continue // no status yet: the template renders a dash, not a key
		}
		seen[state] = true
	}
	if len(seen) == 0 {
		t.Fatal("no chip states produced; the sweep proves nothing")
	}
	keys := map[string]string{}
	for state := range seen {
		keys["devices.config_"+state] = "deviceConfigState"
	}
	assertKeys(t, "device config chip", keys)
	t.Logf("chip states reachable: %d", len(seen))
}

// goSources lists the package's non-test Go files.
func goSources() ([]string, error) {
	all, err := filepath.Glob("*.go")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range all {
		if !strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
	}
	return out, nil
}

// catalogPrefixes is the set of namespaces the catalog actually uses
// ("nav.", "devices.", ...). A dotted lowercase string in some unrelated
// return statement is not ours to police; one that claims a namespace we
// translate almost certainly is.
var catalogPrefixes = func() map[string]bool {
	out := map[string]bool{}
	for k := range catalog["en"] {
		if i := strings.IndexByte(k, '.'); i > 0 {
			out[k[:i]] = true
		}
	}
	return out
}()

func looksLikeCatalogKey(s string) bool {
	i := strings.IndexByte(s, '.')
	return i > 0 && catalogPrefixes[s[:i]]
}
