package web

import "testing"

// splitSentence decides what a settings row says and what moves behind its
// "?", so every wrong split is visible on the page: a row that stops
// mid-clause, or a rationale paragraph that never left the row.
func TestSplitSentence(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		head, rest string
	}{
		{"single sentence stays whole",
			"Whether to enable media player (VLC).",
			"Whether to enable media player (VLC).", ""},
		{"no full stop at all",
			"Management server URL",
			"Management server URL", ""},
		{"rationale moves",
			"Password manager (KeePassXC). The one set that is opt-out: without a password manager people reuse passwords.",
			"Password manager (KeePassXC).",
			"The one set that is opt-out: without a password manager people reuse passwords."},
		{"e.g. is not a sentence end",
			"A deployment has to say no on purpose (e.g. it ships a vault). Nothing else does.",
			"A deployment has to say no on purpose (e.g. it ships a vault).",
			"Nothing else does."},
		{"a dotted option path is not a sentence end",
			"Office suite installed when dawo.apps.office is enabled. Collabora resonates more with users coming from Windows.",
			"Office suite installed when dawo.apps.office is enabled.",
			"Collabora resonates more with users coming from Windows."},
		{"three sentences keep the tail together",
			"Edit connections for every user. Separate from wifi on purpose. Hostname is not included.",
			"Edit connections for every user.",
			"Separate from wifi on purpose. Hostname is not included."},
		{"question mark ends a sentence too",
			"Who owns this? The fleet does.",
			"Who owns this?", "The fleet does."},
		{"trailing whitespace is not a second sentence",
			"Time sync. ",
			"Time sync.", ""},
		{"empty stays empty",
			"", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			head, rest := splitSentence(c.in)
			if head != c.head {
				t.Errorf("head = %q, want %q", head, c.head)
			}
			if rest != c.rest {
				t.Errorf("rest = %q, want %q", rest, c.rest)
			}
		})
	}
}

// The two halves must reconstruct the whole: a split that silently drops a
// clause would hide catalog prose nobody can get back to.
func TestSplitSentenceLosesNothing(t *testing.T) {
	in := "Block USB devices plugged in after boot. Devices present at boot keep working, so a running machine does not lose its keyboard."
	head, rest := splitSentence(in)
	if head+" "+rest != in {
		t.Errorf("halves do not rejoin:\n got %q\nwant %q", head+" "+rest, in)
	}
}
