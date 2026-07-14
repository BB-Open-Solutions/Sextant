package main

import "testing"

// TestRedactRemoteMasksEmbeddedCredential guards the fix for a leaked git
// credential: an HTTPS remote with userinfo (https://user:token@host/repo)
// must never be logged verbatim.
func TestRedactRemoteMasksEmbeddedCredential(t *testing.T) {
	got := redactRemote("https://svc:s3cr3t-token@forgejo.example.com/org/repo.git")
	if got == "" {
		t.Fatal("redactRemote returned empty")
	}
	if got == "https://svc:s3cr3t-token@forgejo.example.com/org/repo.git" {
		t.Fatal("credential not redacted")
	}
	if want := "https://***@forgejo.example.com/org/repo.git"; got != want {
		t.Fatalf("redactRemote = %q, want %q", got, want)
	}
}

// TestRedactRemoteLeavesPlainURLAlone: no userinfo, nothing to mask.
func TestRedactRemoteLeavesPlainURLAlone(t *testing.T) {
	plain := "https://forgejo.example.com/org/repo.git"
	if got := redactRemote(plain); got != plain {
		t.Fatalf("redactRemote(%q) = %q, want unchanged", plain, got)
	}
}

// TestRedactRemoteHandlesEmptyAndMalformed: no remote configured, or an
// unparseable value, must not panic and must not itself become an error.
func TestRedactRemoteHandlesEmptyAndMalformed(t *testing.T) {
	if got := redactRemote(""); got != "" {
		t.Fatalf("redactRemote(\"\") = %q", got)
	}
	malformed := "not a url ://"
	if got := redactRemote(malformed); got != malformed {
		t.Fatalf("redactRemote(%q) = %q, want unchanged", malformed, got)
	}
}
