// Command extract-investments builds data/investments.csv: the net cash flows
// from investments in financial assets for policy purposes, itemised, from
// each publication in data/investment_documents.csv. Budget Paper No. 1 gives
// estimates; the Final Budget Outcome gives the year's actual. The CSV is
// committed and reviewed, then loaded by load-investments, so the server
// never needs pdftotext.
//
// Requires pdftotext (poppler) on PATH. Documents are cached in .local/budgetdocs.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"spendingexplorer/internal/investments"
	"spendingexplorer/internal/pbs"
)

var client = &http.Client{Timeout: 2 * time.Minute}

func main() {
	docs := flag.String("docs", "data/investment_documents.csv", "publications to extract")
	out := flag.String("out", "data/investments.csv", "output CSV")
	cache := flag.String("cache", ".local/budgetdocs", "document cache directory")
	flag.Parse()
	if _, err := exec.LookPath("pdftotext"); err != nil {
		log.Fatal("pdftotext not found on PATH (install poppler)")
	}
	if err := run(context.Background(), *docs, *out, *cache); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, docsPath, outPath, cache string) error {
	docs, err := readCSV(docsPath)
	if err != nil {
		return err
	}
	rows := [][]string{{"doc_type", "budget_year", "url", "sha256", "title", "item", "item_key", "fiscal_year", "estimate_type", "amount_m", "is_total"}}
	// Budget estimates by publication year, item key and fiscal year, to check
	// each outcome's "estimate at Budget" column against the Budget itself.
	estimates := map[string]map[string]int64{}
	type outcome struct {
		budget string
		table  investments.Table
	}
	var outcomes []outcome
	for _, d := range docs {
		docType, budget, url := d[0], d[1], d[2]
		path, sha, err := fetch(ctx, url, cache)
		if err != nil {
			return err
		}
		text, err := exec.CommandContext(ctx, "pdftotext", "-layout", "-enc", "UTF-8", path, "-").Output()
		if err != nil {
			return fmt.Errorf("pdftotext %s: %w", url, err)
		}

		// Budget tables have one column per year plus a total; the outcome
		// table has the Budget estimate, the outcome and the change.
		var years []string
		var cols, keep int
		var title string
		switch docType {
		case "bp1_investments":
			years = investments.Years(string(text))
			cols = len(years) + 1
			title = budget + " Budget Paper No. 1"
		case "fbo_investments":
			years = []string{budget}
			cols, keep = 3, 1
			title = "Final Budget Outcome " + budget
		default:
			return fmt.Errorf("unknown doc_type %q", docType)
		}
		t, err := investments.Parse(string(text), cols)
		if err != nil {
			return fmt.Errorf("%s: %w", url, err)
		}
		title += " – " + t.Ref + ", net cash flows from investments in financial assets for policy purposes"
		if docType == "fbo_investments" {
			outcomes = append(outcomes, outcome{budget, t})
		}

		est := map[string]int64{}
		estimates[budget] = est
		for i, r := range append(t.Rows, t.Total) {
			total, key := "0", investments.Key(r.Item)
			if i == len(t.Rows) {
				total, key = "1", "total"
			}
			for j, fy := range years {
				v := r.Amounts[j+keep]
				estType := "actual"
				if docType == "bp1_investments" {
					estType = pbs.EstimateType(budget, fy)
					est[key+"|"+fy] = v
				}
				rows = append(rows, []string{docType, budget, url, sha, title, r.Item, key, fy, estType, strconv.FormatInt(v, 10), total})
			}
		}
		fmt.Printf("%s: %s, %d items, total %v\n", title, filepath.Base(url), len(t.Rows), t.Total.Amounts)
	}

	for _, o := range outcomes {
		est, ok := estimates[nextYear(o.budget)]
		if !ok {
			continue
		}
		for i, r := range append(o.table.Rows, o.table.Total) {
			key := investments.Key(r.Item)
			if i == len(o.table.Rows) {
				key = "total"
			}
			if v, ok := est[key+"|"+o.budget]; !ok || v != r.Amounts[0] {
				log.Printf("warning: %s %q: outcome table's Budget estimate %d, Budget paper has %d (found %v)", o.budget, r.Item, r.Amounts[0], v, ok)
			}
		}
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	w.WriteAll(rows)
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	fmt.Printf("wrote %d rows to %s\n", len(rows)-1, outPath)
	return f.Close()
}

// fetch downloads a document once into the cache and returns its path and SHA-256.
func fetch(ctx context.Context, u, cache string) (string, string, error) {
	h := sha256.Sum256([]byte(u))
	path := filepath.Join(cache, hex.EncodeToString(h[:8])+".pdf")
	body, err := os.ReadFile(path)
	if err != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return "", "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
		resp, err := client.Do(req)
		if err != nil {
			return "", "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/pdf" {
			return "", "", fmt.Errorf("%s: HTTP %d, %s", u, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if body, err = io.ReadAll(resp.Body); err != nil {
			return "", "", err
		}
		if err := os.MkdirAll(cache, 0o755); err != nil {
			return "", "", err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return "", "", err
		}
	}
	sum := sha256.Sum256(body)
	return path, hex.EncodeToString(sum[:]), nil
}

// nextYear turns "2025-26" into "2026-27".
func nextYear(fy string) string {
	y, err := strconv.Atoi(fy[:4])
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%02d", y+1, (y+2)%100)
}

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[1:], nil
}
