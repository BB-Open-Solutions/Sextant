package web

import (
	"os"
	"regexp"
	"testing"
)

// An integration the console offers but the agent never probes reports "no
// reading" on every device, for ever. That looks like a fleet of broken
// devices rather than a missing probe, and nothing in either list points at
// the other.
//
// Both lists are edited by hand, in different languages, in different
// directories. This asks once whether they still agree, the way the API route
// audit asks its question of every endpoint at once rather than trusting a
// table somebody has to remember.

// integrationsWithoutAUnit are console integrations that deliberately have no
// systemd unit to probe, each for a stated reason. An entry here is a claim
// that "no reading" is the honest answer for it, not an oversight.
var integrationsWithoutAUnit = map[string]string{
	"localAdmin": "an account, not a service: there is no unit whose state would mean anything",
}

var rustUnitEntry = regexp.MustCompile(`\(\s*"([a-zA-Z]+)"\s*,\s*&\[`)

func TestEveryIntegrationTheConsoleShowsIsProbedByTheAgent(t *testing.T) {
	const probes = "../../../agent/src/collect.rs"

	src, err := os.ReadFile(probes)
	if err != nil {
		t.Fatalf("cannot read the agent's probe table at %s: %v", probes, err)
	}

	// Only the INTEGRATION_UNITS block, so an unrelated tuple elsewhere in the
	// file cannot make this audit pass by accident.
	body := string(src)
	start := regexpIndex(t, body, `const INTEGRATION_UNITS[^=]*=\s*&\[`)
	end := regexpIndexFrom(t, body, `\n\];`, start)

	probed := map[string]bool{}
	for _, m := range rustUnitEntry.FindAllStringSubmatch(body[start:end], -1) {
		probed[m[1]] = true
	}
	if len(probed) < 3 {
		t.Fatalf("found only %d probes; the parse is wrong and this audit is not "+
			"looking at anything", len(probed))
	}

	for _, ig := range knownIntegrations {
		if reason, exempt := integrationsWithoutAUnit[ig.Key]; exempt {
			if probed[ig.Key] {
				t.Errorf("%q is listed as having no unit (%s) but the agent probes it; "+
					"remove the exemption", ig.Key, reason)
			}
			continue
		}
		if !probed[ig.Key] {
			t.Errorf("the console offers %q but the agent has no unit for it, so every "+
				"device will report no reading for ever. Add it to INTEGRATION_UNITS in "+
				"%s, or list it in integrationsWithoutAUnit with the reason", ig.Key, probes)
		}
	}

	known := map[string]bool{}
	for _, ig := range knownIntegrations {
		known[ig.Key] = true
	}
	for key := range probed {
		if !known[key] {
			t.Errorf("the agent probes %q, which no console integration names. The reading "+
				"arrives and nothing shows it", key)
		}
	}
}

func regexpIndex(t *testing.T, s, pattern string) int {
	t.Helper()
	loc := regexp.MustCompile(pattern).FindStringIndex(s)
	if loc == nil {
		t.Fatalf("could not find %q in the agent source; the audit cannot run", pattern)
	}
	return loc[1]
}

func regexpIndexFrom(t *testing.T, s, pattern string, from int) int {
	t.Helper()
	loc := regexp.MustCompile(pattern).FindStringIndex(s[from:])
	if loc == nil {
		t.Fatalf("could not find %q after the table start; the audit cannot run", pattern)
	}
	return from + loc[0]
}
