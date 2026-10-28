package db

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var SchemaSQL string

// addedColumns are columns added to schema.sql after their table was first
// created. CREATE TABLE IF NOT EXISTS leaves an existing table as it is, so
// Open adds them to older databases before applying the schema, whose indexes
// may use them.
var addedColumns = []struct{ table, column, decl string }{
	{"grant_award", "recipient_key", "TEXT NOT NULL DEFAULT ''"},
}

// Open opens (creating if needed) the SQLite database at path and applies the schema.
func Open(path string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := addColumns(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("add columns: %w", err)
	}
	if _, err := conn.Exec(SchemaSQL); err != nil {
		conn.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return conn, nil
}

// addColumns adds each of addedColumns to its table if the table exists
// without it. A missing table is left for the schema to create.
func addColumns(conn *sql.DB) error {
	for _, c := range addedColumns {
		var cols, has int
		err := conn.QueryRow("SELECT COUNT(*), COUNT(CASE WHEN name = ? THEN 1 END) FROM pragma_table_info(?)", c.column, c.table).Scan(&cols, &has)
		if err != nil {
			return err
		}
		if cols == 0 || has > 0 {
			continue
		}
		if _, err := conn.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.decl)); err != nil {
			return fmt.Errorf("%s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

// OpenReadOnly opens an existing database for the web app, which never writes.
func OpenReadOnly(path string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// Snapshot writes a consistent, compacted copy of src's database to out, which
// must not exist. VACUUM INTO reads one snapshot, so a loader writing at the
// same time can't leave the copy half-written, and the copy uses a rollback
// journal so a read-only reader never needs a -wal or -shm file beside it.
func Snapshot(src *sql.DB, out string) error {
	if _, err := src.Exec("VACUUM INTO ?", out); err != nil {
		return err
	}
	snap, err := sql.Open("sqlite", out)
	if err != nil {
		return err
	}
	defer snap.Close()
	if _, err := snap.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		return err
	}
	var check string
	if err := snap.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("quick_check on %s: %s", out, check)
	}
	return nil
}
