package historyserver

import (
	"database/sql"
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
