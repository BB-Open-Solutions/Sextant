package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// licence_test.go: every vendored dependency must be under a licence
// compatible with this project's EUPL-1.2.
//
// Audit finding S2 (2026-08-07): all 27 vendored dependencies were BSD, MIT
// or Apache-2.0, and nothing checked. The answer was clean because the
// dependencies happen to be permissive, not because anything enforced it - a
// future dependency under a copyleft licence would be vendored, built,
// released and shipped with no signal at all. For an EUPL product
// distributed to public bodies that is a licensing problem before it is a
// technical one.
//
// The check is deliberately a test rather than a CI script: it runs where
// everything else runs, and it fails on the commit that introduces the
// dependency rather than in a review nobody scheduled.
//
// It reads the licence TEXT rather than a manifest field, because a manifest
// says what somebody typed and the text says what was granted.

// allowed lists the licence families this project may redistribute under
// EUPL-1.2. Permissive only, on purpose: a copyleft dependency is not
// forbidden by law here, but it is a decision somebody has to take
// deliberately, and adding a name to this list is how they take it.
var allowed = map[string]bool{
	"MIT": true, "BSD": true, "Apache-2.0": true, "ISC": true,
	// EUPL-1.2 is explicitly compatible with these, and the EUPL itself
	// obviously qualifies.
	"MPL-2.0": true, "EUPL-1.2": true,
}

// licencePatterns identify a licence from its opening text. Ordered: the
// first match wins, so the more specific patterns come first.
var licencePatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"EUPL-1.2", regexp.MustCompile(`(?i)EUROPEAN UNION PUBLIC LICENCE`)},
	{"Apache-2.0", regexp.MustCompile(`(?i)Apache License`)},
	{"MPL-2.0", regexp.MustCompile(`(?i)Mozilla Public License`)},
	// GPL/LGPL before the permissive ones: an LGPL file also contains
	// "GNU", and reading it as anything else would be the dangerous miss.
	{"LGPL", regexp.MustCompile(`(?i)GNU LESSER GENERAL PUBLIC`)},
	{"AGPL", regexp.MustCompile(`(?i)GNU AFFERO GENERAL PUBLIC`)},
	{"GPL", regexp.MustCompile(`(?i)GNU GENERAL PUBLIC LICENSE`)},
	{"ISC", regexp.MustCompile(`(?i)ISC License`)},
	{"MIT", regexp.MustCompile(`(?i)MIT License|Permission is hereby granted, free of charge`)},
	{"BSD", regexp.MustCompile(`(?i)Redistribution and use in source and binary forms`)},
}

func identify(text string) string {
	for _, p := range licencePatterns {
		if p.re.MatchString(text) {
			return p.name
		}
	}
	return ""
}

func TestVendoredDependenciesAreRedistributable(t *testing.T) {
	if _, err := os.Stat("vendor"); os.IsNotExist(err) {
		t.Skip("no vendor directory")
	}
	names := map[string]bool{
		"LICENSE": true, "LICENSE.txt": true, "LICENSE.md": true,
		"LICENCE": true, "COPYING": true, "COPYING.txt": true,
	}

	var checked int
	err := filepath.WalkDir("vendor", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !names[d.Name()] {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		module := strings.TrimPrefix(filepath.Dir(path), "vendor"+string(filepath.Separator))
		switch got := identify(string(b)); {
		case got == "":
			// Unrecognised is a failure, not a pass. A licence this test
			// cannot read is one nobody has read.
			t.Errorf("%s: licence not recognised - read it and either add the pattern or the dependency goes", module)
		case !allowed[got]:
			t.Errorf("%s is %s, which this project may not redistribute under EUPL-1.2. "+
				"If that is wrong, add it to `allowed` with the reasoning - deliberately, not to make a build pass.",
				module, got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk vendor: %v", err)
	}
	// A walk that found nothing would pass silently, which is the failure
	// mode this whole file exists to avoid.
	if checked == 0 {
		t.Fatal("no licence files found under vendor/; this check proved nothing")
	}
	t.Logf("checked %d vendored licences", checked)
}

// TestTheLicenceCheckRecognisesCopyleft proves the check can fail. A
// detector that only ever sees permissive licences is indistinguishable from
// one that returns "fine" unconditionally.
func TestTheLicenceCheckRecognisesCopyleft(t *testing.T) {
	cases := map[string]string{
		"GPL":        "                    GNU GENERAL PUBLIC LICENSE\n                       Version 3, 29 June 2007",
		"LGPL":       "                   GNU LESSER GENERAL PUBLIC LICENSE\n                       Version 3",
		"AGPL":       "                    GNU AFFERO GENERAL PUBLIC LICENSE\n                       Version 3",
		"MIT":        "MIT License\n\nCopyright (c) 2020 Somebody",
		"Apache-2.0": "                                 Apache License\n                           Version 2.0, January 2004",
		"EUPL-1.2":   "EUROPEAN UNION PUBLIC LICENCE v. 1.2",
	}
	for want, text := range cases {
		if got := identify(text); got != want {
			t.Errorf("identify(%s text) = %q, want %q", want, got, want)
		}
	}
	for _, copyleft := range []string{"GPL", "LGPL", "AGPL"} {
		if allowed[copyleft] {
			t.Errorf("%s is in the allowlist; this check would not stop it", copyleft)
		}
	}
	if identify("this is not a licence at all") != "" {
		t.Error("unrecognised text was identified as a licence")
	}
}
