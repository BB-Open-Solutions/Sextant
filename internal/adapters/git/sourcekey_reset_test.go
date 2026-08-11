package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts a file in the repo, creating parents.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// key reads the source key with fleet.json excluded, which is the only
// exclusion the console ever passes: the change flow writes that one file and
// asserts the rest of the tree is unchanged.
func key(t *testing.T, r *Repo) string {
	t.Helper()
	k, err := r.SourceKey(context.Background(), "fleet.json")
	if err != nil {
		t.Fatal(err)
	}
	if k == "" {
		t.Fatal("empty source key")
	}
	return k
}

// SourceKey lets a gate verdict be memoised, so what it must get right is
// exactly which changes are allowed to leave the key standing. One: the
// excluded path, because the caller is asserting the tree differs from HEAD
// there and nowhere else. Everything else has to move it, and moving it means
// every cached verdict falls, which is the safe direction.
//
// The failure this prevents has no symptom. A key that stands still while the
// tree changed means nix evaluates one thing and the cache answers for
// another, and the answer is a gate verdict.
func TestSourceKeyMovesForEverythingExceptTheExcludedPath(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "fleet.json", `{"version":3}`)
	write(t, dir, "flake.lock", `{"nodes":{}}`)
	write(t, dir, "modules/base.nix", "{}")
	run(t, dir, "add", ".")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	r, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	base := key(t, r)

	t.Run("reading it twice gives the same answer", func(t *testing.T) {
		if again := key(t, r); again != base {
			t.Errorf("key is not stable: %s then %s", base, again)
		}
	})

	t.Run("editing the excluded path leaves the key alone", func(t *testing.T) {
		write(t, dir, "fleet.json", `{"version":3,"org":{"settings":{"desktop":"gnome"}}}`)
		if got := key(t, r); got != base {
			t.Errorf("key moved on an edit to the excluded path: %s -> %s", base, got)
		}
	})

	t.Run("a dirty file that is not excluded moves it", func(t *testing.T) {
		write(t, dir, "flake.lock", `{"nodes":{"nixpkgs":{}}}`)
		defer func() { run(t, dir, "checkout", "--", "flake.lock") }()
		if got := key(t, r); got == base {
			t.Error("a modified flake.lock did not move the key; a cached verdict " +
				"would survive a core change")
		}
	})

	t.Run("an untracked file moves it", func(t *testing.T) {
		write(t, dir, "modules/extra.nix", "{ }")
		defer func() { _ = os.Remove(filepath.Join(dir, "modules/extra.nix")) }()
		if got := key(t, r); got == base {
			t.Error("an untracked module did not move the key; nix would evaluate " +
				"a file the key does not know about")
		}
	})

	t.Run("a deleted file moves it", func(t *testing.T) {
		p := filepath.Join(dir, "modules/base.nix")
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		defer func() { write(t, dir, "modules/base.nix", string(body)) }()
		if got := key(t, r); got == base {
			t.Error("a deleted module did not move the key")
		}
	})

	t.Run("a new commit moves it", func(t *testing.T) {
		write(t, dir, "modules/base.nix", "{ imports = []; }")
		run(t, dir, "add", "modules/base.nix")
		run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "change base")
		if got := key(t, r); got == base {
			t.Error("a commit to the tree did not move the key")
		}
	})
}

// Excluding one path must not excuse a path that merely resembles it. The
// exclusion is a whole-path match, and a suffix comparison written slightly
// wrong would let `not-fleet.json` or `sub/fleet.json` slip through with it.
func TestSourceKeyExclusionIsNotASuffixMatch(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "fleet.json", "{}")
	write(t, dir, "not-fleet.json", "{}")
	write(t, dir, "sub/fleet.json", "{}")
	run(t, dir, "add", ".")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	r, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	base := key(t, r)
	for _, other := range []string{"not-fleet.json", "sub/fleet.json"} {
		write(t, dir, other, `{"changed":true}`)
		got := key(t, r)
		run(t, dir, "checkout", "--", other)
		if got == base {
			t.Errorf("editing %q left the key standing; it was excluded along with "+
				"fleet.json", other)
		}
	}
}

// ResetHard discards commits, and the only thing between its argument and
// `git reset --hard` is revHashRe. A ref expression reaching that argv is a
// destructive operation taking input it was never meant to take, so the
// refusals are pinned by name rather than left to the regex being read
// correctly.
func TestResetHardRefusesAnythingThatIsNotACommitHash(t *testing.T) {
	dir := initRepo(t)
	r, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	for _, bad := range []string{
		"HEAD",           // a ref, not a hash: resolves to something that moves
		"HEAD~1",         // a ref expression
		"main",           // a branch name
		"--hard",         // a flag smuggled in as a revision
		"-f",             // ditto, short form
		"abc123 --force", // two arguments wearing one coat
		"abc123;rm -rf /",
		"abc123\nrm -rf /",
		"../../etc",
		"ABC123",   // uppercase is not a git hash
		"abc12",    // five characters, under the minimum
		"g0od1dea", // 'g' is not hex
		"",
	} {
		if err := r.ResetHard(ctx, bad); err == nil {
			t.Errorf("ResetHard accepted %q", bad)
		}
	}
}

func TestResetHardDiscardsCommitsAfterTheGivenOne(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "a.txt", "one")
	run(t, dir, "add", "a.txt")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "one")
	target := run(t, dir, "rev-parse", "HEAD")

	write(t, dir, "a.txt", "two")
	run(t, dir, "add", "a.txt")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "two")

	r, err := Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ResetHard(context.Background(), target); err != nil {
		t.Fatal(err)
	}

	if head := run(t, dir, "rev-parse", "HEAD"); head != target {
		t.Errorf("HEAD = %s, want %s", head, target)
	}
	// --hard, so the working tree comes back too. A reset that moved the ref
	// and left the file would leave the next gate evaluating "two".
	body, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "one" {
		t.Errorf("working tree = %q, want %q", body, "one")
	}
}
