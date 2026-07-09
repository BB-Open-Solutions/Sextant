package git

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// run executes git in dir, failing the test on error.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo creates a working tree with one initial commit on main.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

// withRemote clones dir into a bare remote and wires origin.
func withRemote(t *testing.T, dir string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "remote.git")
	run(t, dir, "clone", "-q", "--bare", dir, bare)
	run(t, dir, "remote", "add", "origin", bare)
	run(t, dir, "push", "-q", "origin", "main")
	return bare
}

func TestOpenRejectsNonRepo(t *testing.T) {
	if _, err := Open(t.TempDir(), ""); err == nil {
		t.Fatal("non-repo dir accepted")
	}
}

func TestReadWriteConfinedToRepo(t *testing.T) {
	r, err := Open(initRepo(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile("fleet.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	b, err := r.ReadFile("fleet.json")
	if err != nil || string(b) != `{}` {
		t.Fatalf("read = %q, %v", b, err)
	}
	for _, bad := range []string{"", "/etc/passwd", "../escape", "a/../../b"} {
		if _, err := r.ReadFile(bad); err == nil {
			t.Errorf("read %q accepted", bad)
		}
		if err := r.WriteFile(bad, nil); err == nil {
			t.Errorf("write %q accepted", bad)
		}
	}
}

func TestCommitCarriesAuthor(t *testing.T) {
	dir := initRepo(t)
	r, _ := Open(dir, "")
	if err := r.WriteFile("fleet.json", []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	err := r.Commit(context.Background(), "settings: test edit",
		ports.Author{Name: "Ada Lovelace", Email: "ada@example.org"}, "fleet.json")
	if err != nil {
		t.Fatal(err)
	}
	got := run(t, dir, "log", "-1", "--format=%an <%ae> %s")
	if got != "Ada Lovelace <ada@example.org> settings: test edit" {
		t.Fatalf("log = %q", got)
	}
	// Empty author falls back to the service identity.
	r.WriteFile("fleet.json", []byte(`{"v":2}`))
	if err := r.Commit(context.Background(), "x", ports.Author{}, "fleet.json"); err != nil {
		t.Fatal(err)
	}
	if got := run(t, dir, "log", "-1", "--format=%an"); got != "sextant" {
		t.Fatalf("fallback author = %q", got)
	}
}

func TestSyncAndPush(t *testing.T) {
	dir := initRepo(t)
	withRemote(t, dir)
	r, _ := Open(dir, "origin")
	if !r.HasRemote() {
		t.Fatal("HasRemote = false")
	}

	r.WriteFile("fleet.json", []byte(`{"a":1}`))
	if err := r.Commit(context.Background(), "edit", ports.Author{}, "fleet.json"); err != nil {
		t.Fatal(err)
	}
	if err := r.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Sync is a no-op when in sync; must not error.
	if err := r.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPushConflictClassified(t *testing.T) {
	dir := initRepo(t)
	bare := withRemote(t, dir)

	// A second clone pushes first: our push must lose as ErrConflict.
	other := t.TempDir()
	run(t, other, "clone", "-q", bare, other)
	run(t, other, "-c", "user.name=o", "-c", "user.email=o@o", "commit", "-q", "--allow-empty", "-m", "race winner")
	run(t, other, "push", "-q", "origin", "main")

	r, _ := Open(dir, "origin")
	r.WriteFile("fleet.json", []byte(`{"b":2}`))
	if err := r.Commit(context.Background(), "loser", ports.Author{}, "fleet.json"); err != nil {
		t.Fatal(err)
	}
	err := r.Push(context.Background())
	if err == nil {
		t.Fatal("push should have been rejected")
	}
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("conflict not classified: %v", err)
	}

	// After Sync the local branch sits on the remote head; a new commit pushes.
	if err := r.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.WriteFile("fleet.json", []byte(`{"b":3}`))
	if err := r.Commit(context.Background(), "retry", ports.Author{}, "fleet.json"); err != nil {
		t.Fatal(err)
	}
	if err := r.Push(context.Background()); err != nil {
		t.Fatalf("push after sync: %v", err)
	}
}

func TestNoRemoteNoops(t *testing.T) {
	r, _ := Open(initRepo(t), "")
	if err := r.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
}
