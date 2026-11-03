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
