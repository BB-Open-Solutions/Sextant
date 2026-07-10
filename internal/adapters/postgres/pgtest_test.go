package postgres

// Test harness: boots a throwaway PostgreSQL with initdb + pg_ctl on a unix
// socket under t.TempDir(). Hermetic (no container, no TCP port) and fast;
// skips when the postgres binaries are absent.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// startPostgres returns a DSN for a fresh, empty database.
func startPostgres(t *testing.T) string {
	t.Helper()
	for _, bin := range []string{"initdb", "pg_ctl", "postgres"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("postgres binaries not available: %s", bin)
		}
	}
	base := t.TempDir()
	data := filepath.Join(base, "data")
	sock := filepath.Join(base, "sock")
	if err := os.MkdirAll(sock, 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(name string, args ...string) {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run("initdb", "-D", data, "-U", "sextant", "-A", "trust", "--no-sync")
	run("pg_ctl", "-D", data, "-w", "-t", "30", "-o",
		fmt.Sprintf("-k %s -c listen_addresses='' -c fsync=off -c full_page_writes=off", sock),
		"-l", filepath.Join(base, "pg.log"), "start")
	t.Cleanup(func() {
		_ = exec.Command("pg_ctl", "-D", data, "-m", "immediate", "stop").Run()
	})
	return fmt.Sprintf("postgres://sextant@/postgres?host=%s", sock)
}

// openStore boots Postgres, connects and migrates.
func openStore(t *testing.T) *Store {
	t.Helper()
	dsn := startPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
