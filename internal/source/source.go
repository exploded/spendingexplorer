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
	return RegisterHash(ctx, q, hex.EncodeToString(h.Sum(nil)), filepath.Base(path), title, publisher, url, docType, budgetYear)
}

// RegisterData upserts a source_document for downloaded content, such as a
// set of API responses, identified by the SHA-256 of data.
func RegisterData(ctx context.Context, q *db.Queries, data []byte, name, title, publisher, url, docType, budgetYear string) (int64, error) {
	h := sha256.Sum256(data)
	return RegisterHash(ctx, q, hex.EncodeToString(h[:]), name, title, publisher, url, docType, budgetYear)
}

// RegisterHash upserts a source_document whose SHA-256 is already known.
func RegisterHash(ctx context.Context, q *db.Queries, sha, name, title, publisher, url, docType, budgetYear string) (int64, error) {
	return q.UpsertSourceDocument(ctx, db.UpsertSourceDocumentParams{
		Title:      title,
		Publisher:  publisher,
		Url:        url,
		FileName:   name,
		Sha256:     sha,
		DocType:    docType,
		BudgetYear: budgetYear,
	})
}
