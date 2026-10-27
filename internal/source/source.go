// Package source records where each loaded file came from.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
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

// ReadNames reads a reviewed name file of name,target[,note] rows after a
// header. An empty target is kept, so callers can treat it as "no match". A
// missing file means no names.
func ReadNames(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m := map[string]string{}
	for _, row := range rows[1:] {
		if len(row) >= 2 {
			m[row[0]] = row[1]
		}
	}
	return m, nil
}
