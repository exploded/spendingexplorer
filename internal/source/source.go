// Package source records where each loaded file came from.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"spendingexplorer/internal/db"
)

// Register hashes the file and upserts its source_document row.
func Register(ctx context.Context, q *db.Queries, path, title, publisher, url, docType, budgetYear string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return 0, err
	}
	return q.UpsertSourceDocument(ctx, db.UpsertSourceDocumentParams{
		Title:      title,
		Publisher:  publisher,
		Url:        url,
		FileName:   filepath.Base(path),
		Sha256:     hex.EncodeToString(h.Sum(nil)),
		DocType:    docType,
		BudgetYear: budgetYear,
	})
}
