package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keycardai/keycard-slack-agent-demo/crypto"
)

func openAt(t *testing.T, name string) *DB {
	t.Helper()
	cipher, err := crypto.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	database, err := Open(filepath.Join(t.TempDir(), name), cipher)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestForeignKeysAreEnforced(t *testing.T) {
	// foreign_keys is a per-connection pragma, not stored in the file, so the REFERENCES clauses in
	// schema.sql did nothing until the DSN set it. This test is the only thing standing between the
	// constraint and it silently becoming decorative again.
	database := openAt(t, "fk.db")
	_, err := database.conn.Exec(
		`INSERT INTO messages (id, session_id, role, content) VALUES ('m1', 'no-such-session', 'user', 'hi')`,
	)
	if err == nil {
		t.Fatal("expected the insert to violate the foreign key on messages.session_id")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("expected a foreign key error, got %v", err)
	}
}

func TestSchemaIsEmbeddedNotReadFromTheWorkingDirectory(t *testing.T) {
	// The schema used to be read from ./schema.sql at runtime, so the binary only started when it
	// was launched from the right directory. Opening from a temp dir proves it travels with the code.
	database := openAt(t, "embedded.db")
	var count int
	if err := database.conn.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'`,
	).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Fatal("expected the embedded schema to have created the users table")
	}
}

func TestDSNPreservesCallerParameters(t *testing.T) {
	got := dsn("/data/agent.db?cache=shared")
	if !strings.HasPrefix(got, "/data/agent.db?cache=shared&") {
		t.Fatalf("caller parameters were dropped: %q", got)
	}
	if !strings.Contains(got, "_foreign_keys=on") || !strings.Contains(got, "_busy_timeout=") {
		t.Fatalf("connection parameters missing: %q", got)
	}
	if strings.Count(dsn("/data/agent.db"), "?") != 1 {
		t.Fatalf("expected exactly one query separator: %q", dsn("/data/agent.db"))
	}
}

func TestOpenCreatesAMissingParentDirectory(t *testing.T) {
	// The failure this fixes: .env.example shipped DATABASE_PATH=/data/slack-agent.db, the Fly volume
	// path, so a local run died with a bare "no such file or directory" that named nothing. SQLite
	// creates the file but not the directory above it.
	cipher, err := crypto.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	nested := filepath.Join(t.TempDir(), "does", "not", "exist", "agent.db")
	database, err := Open(nested, cipher)
	if err != nil {
		t.Fatalf("expected the parent directory to be created, got %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
}

func TestOpenNamesThePathItFailedOn(t *testing.T) {
	// A message that does not say which path it tried is the whole reason this took a round trip.
	cipher, err := crypto.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	// A path whose parent is an existing regular file cannot become a directory.
	blocker := filepath.Join(t.TempDir(), "iamafile")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = Open(filepath.Join(blocker, "agent.db"), cipher)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), blocker) {
		t.Fatalf("error should name the path: %v", err)
	}
}
