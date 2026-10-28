// Command snapshot-db writes a consistent, compacted copy of spending.db for
// upload to the server. VACUUM INTO reads one snapshot, so loaders running in
// other sessions can't leave it half-written, and the copy uses a rollback
// journal so the read-only web app never needs a -wal or -shm file.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"spendingexplorer/internal/db"
)

func main() {
	dbPath := flag.String("db", "spending.db", "SQLite database path")
	out := flag.String("out", "dist/spending.db", "snapshot path (replaced if it exists)")
	flag.Parse()

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	os.Remove(*out)

	src, err := db.OpenReadOnly(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer src.Close()
	if _, err := src.Exec("VACUUM INTO ?", *out); err != nil {
		log.Fatal(err)
	}

	snap, err := sql.Open("sqlite", *out)
	if err != nil {
		log.Fatal(err)
	}
	defer snap.Close()
	if _, err := snap.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		log.Fatal(err)
	}
	var check string
	if err := snap.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
		log.Fatal(err)
	}
	if check != "ok" {
		log.Fatalf("quick_check on %s: %s", *out, check)
	}

	fi, err := os.Stat(*out)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s (%d MB)\n", *out, fi.Size()>>20)
}
