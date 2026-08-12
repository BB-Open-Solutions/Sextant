package postgres

// Test harness: boots ONE throwaway PostgreSQL for the whole package with
// initdb + pg_ctl on a unix socket, and gives every test its own database on
// it. Hermetic (no container, no TCP port) and skips when the postgres
// binaries are absent.
//
// WHY ONE INSTANCE. It used to be one per test, which is a stronger isolation
// than these tests need and it cost the whole package's runtime. Measured
// 2026-08-12 on the developer machine:
//
//	initdb        393 ms
//	pg_ctl start  115 ms
//	createdb       54 ms
//
// 57 openStore calls at 508 ms of instance setup each is 29 of the package's
// 39 seconds, spent before a single assertion runs. That mattered beyond
// patience: the pre-push hook runs the full suite, and a guard that takes
// long enough to be annoying is a guard people start bypassing.
//
// Separate databases on one server give these tests exactly what separate
// servers did. They share no schema, no rows and no sequences, and none of
// them touches a cluster-wide object. What they no longer get is isolation
// from a server-level crash, and a test that crashed the server would now
// fail its neighbours too - loudly, which is the acceptable direction.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// templateDB carries the migrated schema. CREATE DATABASE ... TEMPLATE copies
// it, so each test starts migrated without running the migrations again.
const templateDB = "sextant_template"

var (
	// sharedSocket is the running server's socket directory, empty when the
	// binaries are absent and every test skips.
	sharedSocket string
	// dbCounter names each test's database. Atomic because a future t.Parallel
	// would otherwise hand two tests the same name, and the failure would look
	// like data from another test rather than like a harness bug.
	dbCounter atomic.Int64
)

func TestMain(m *testing.M) {
	// No defer. os.Exit does not run deferred functions, so a deferred
	// cleanup here would leak a postgres data directory and a socket on every
	// failing run - into /tmp, which on this machine is a tmpfs with a quota,
	// and a full one stops builds with "disk quota exceeded" rather than with
	// anything that names the cause. Found by the linter, after exactly that
	// had already happened once from other leftovers.
	code, cleanup := bootShared()
	if code != 0 {
		cleanup()
		os.Exit(code)
	}
	code = m.Run()
	cleanup()
	os.Exit(code)
}

// bootShared starts the server and prepares the template. On missing binaries
// it returns without a socket, and openStore skips each test individually so
// the reason appears per test rather than as one silent package skip.
func bootShared() (int, func()) {
	noop := func() {}
	for _, bin := range []string{"initdb", "pg_ctl", "postgres", "createdb"} {
		if _, err := exec.LookPath(bin); err != nil {
			return 0, noop
		}
	}
	base, err := os.MkdirTemp("", "sxt-pg-data")
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: temp dir:", err)
		return 1, noop
	}
	data := filepath.Join(base, "data")
	// The unix socket path must stay well under the ~107-char sun_path limit,
	// so it lives in a short top-level temp dir rather than beside the data
	// directory.
	sock, err := os.MkdirTemp("", "sxt-pg")
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: socket dir:", err)
		return 1, noop
	}
	cleanup := func() {
		_ = exec.Command("pg_ctl", "-D", data, "-m", "immediate", "stop").Run()
		_ = os.RemoveAll(sock)
		_ = os.RemoveAll(base)
	}

	run := func(name string, args ...string) error {
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %v: %w\n%s", name, args, err, out)
		}
		return nil
	}
	if err := run("initdb", "-D", data, "-U", "sextant", "-A", "trust", "--no-sync"); err != nil {
		fmt.Fprintln(os.Stderr, "harness:", err)
		cleanup()
		return 1, noop
	}
	if err := run("pg_ctl", "-D", data, "-w", "-t", "30", "-o",
		fmt.Sprintf("-k %s -c listen_addresses='' -c fsync=off -c full_page_writes=off", sock),
		"-l", filepath.Join(base, "pg.log"), "start"); err != nil {
		fmt.Fprintln(os.Stderr, "harness:", err)
		cleanup()
		return 1, noop
	}

	// Migrate the template once. The connection is closed before any test
	// runs: CREATE DATABASE ... TEMPLATE refuses while anybody else is
	// connected to the template, and that refusal would surface as a
	// confusing failure in whichever test happened to go first.
	if err := run("createdb", "-h", sock, "-U", "sextant", templateDB); err != nil {
		fmt.Fprintln(os.Stderr, "harness:", err)
		cleanup()
		return 1, noop
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tpl, err := Open(ctx, dsnFor(sock, templateDB))
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: migrate template:", err)
		cleanup()
		return 1, noop
	}
	tpl.Close()

	sharedSocket = sock
	return 0, cleanup
}

func dsnFor(sock, db string) string {
	return fmt.Sprintf("postgres://sextant@/%s?host=%s", db, sock)
}

// openStore gives this test its own migrated database.
func openStore(t *testing.T) *Store {
	t.Helper()
	if sharedSocket == "" {
		t.Skip("postgres binaries not available")
	}
	name := fmt.Sprintf("sxt_test_%d", dbCounter.Add(1))
	out, err := exec.Command("createdb", "-h", sharedSocket, "-U", "sextant",
		"-T", templateDB, name).CombinedOutput()
	if err != nil {
		t.Fatalf("createdb %s: %v\n%s", name, err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Open migrates, which on a template copy finds everything already
	// applied. Kept rather than bypassed: every test then exercises the same
	// path production takes, including migration idempotence.
	s, err := Open(ctx, dsnFor(sharedSocket, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// The change from one server per test to one database per test rests on this
// and nothing else: two stores must not see each other's rows. Asserted
// rather than assumed, because if it were ever false the symptom would be a
// test failing over data it never wrote, and the first suspect would be the
// code under test rather than the harness.
func TestEachTestGetsItsOwnDatabase(t *testing.T) {
	a, b := openStore(t), openStore(t)
	ctx := context.Background()

	const tenant, subject = "t", "sub-isolation"
	if err := a.RecordUser(ctx, tenant, subject, "a@example.org", "A", nil); err != nil {
		t.Fatal(err)
	}

	// Same query, other database: nothing.
	email, _, err := b.EmailForSubject(ctx, tenant, subject)
	if err != nil {
		t.Fatal(err)
	}
	if email != "" {
		t.Fatalf("the second store saw %q written by the first; the databases "+
			"are shared and every test in this package is now suspect", email)
	}

	// And the first still has it, so the emptiness above is isolation rather
	// than a write that silently failed.
	email, _, err = a.EmailForSubject(ctx, tenant, subject)
	if err != nil {
		t.Fatal(err)
	}
	if email != "a@example.org" {
		t.Fatalf("the first store lost its own row: %q", email)
	}
}
