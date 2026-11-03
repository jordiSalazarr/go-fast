package historyserver

import (
	"database/sql"
	"testing"
)

// ExecForTest runs a statement on the database at path, outside the
// server.
func ExecForTest(path, statement string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(statement)
	return err
}

// CountForTest is the number a counting query answers, outside the
// server.
func CountForTest(t *testing.T, path, query string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// CreateProjectForTest makes project id in org with a secret the test
// chose, as if a member had created it.
func CreateProjectForTest(t *testing.T, s *Store, org, id, secret string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO projects (id, organisation, secret_sha256, created_by, created_at, secret_by, secret_at) VALUES (?, ?, ?, 'test', '2026-10-18T09:00:00Z', 'test', '2026-10-18T09:00:00Z')`,
		id, org, sha256Of(secret)); err != nil {
		t.Fatal(err)
	}
}
