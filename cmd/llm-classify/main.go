// Command llm-classify classifies AusTender contract descriptions with Claude
// through the Message Batches API (half price, results within 24 hours).
//
//	llm-classify submit [-limit N]   # queue unclassified contracts
//	llm-classify collect             # store results of finished batches
//
// On a development PC the API key is read from a file (-key-file, default
// .local/anthropic.key) and passed to the client directly. Never set
// ANTHROPIC_API_KEY there: Claude Code would use it in place of the
// subscription login. On the server the key may come from the service
// environment instead.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"spendingexplorer/internal/classify"
	"spendingexplorer/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: llm-classify submit|collect [flags]")
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dbPath := fs.String("db", "spending.db", "SQLite database path")
	limit := fs.Int("limit", 5000, "submit: most contracts to queue")
	segment := fs.String("unspsc", "80", "submit: UNSPSC prefix to classify (80 = management and business professionals)")
	keyFile := fs.String("key-file", ".local/anthropic.key", "file holding the API key; used when ANTHROPIC_API_KEY is not set")
	fs.Parse(os.Args[2:])

	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			log.Fatalf("no API key: ANTHROPIC_API_KEY is not set and %s cannot be read", *keyFile)
		}
		key = strings.TrimSpace(string(b))
	}
	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()
	client := anthropic.NewClient(option.WithAPIKey(key))

	switch cmd {
	case "submit":
		err = submit(ctx, client, conn, *segment, *limit)
	case "collect":
		err = collect(ctx, client, conn)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func submit(ctx context.Context, client anthropic.Client, conn *sql.DB, segment string, limit int) error {
	q := db.New(conn)
	rows, err := q.ContractsToClassify(ctx, db.ContractsToClassifyParams{
		UnspscPrefix: sql.NullString{String: segment, Valid: true}, Task: classify.Task, PromptVersion: classify.PromptVersion, Lim: int64(limit),
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		log.Print("nothing to classify")
		return nil
	}
	items := make([]classify.Item, len(rows))
	for i, r := range rows {
		items[i] = classify.Item{CNID: r.CnID, Description: r.Description, Agency: r.Agency, Supplier: r.Supplier}
	}

	var requests []anthropic.MessageBatchNewParamsRequest
	groups := map[string][]classify.Item{}
	for start := 0; start < len(items); start += classify.PerRequest {
		group := items[start:min(start+classify.PerRequest, len(items))]
		prompt, err := classify.Prompt(group)
		if err != nil {
			return err
		}
		customID := fmt.Sprintf("r%05d", len(requests))
		groups[customID] = group
		requests = append(requests, anthropic.MessageBatchNewParamsRequest{
			CustomID: customID,
			Params: anthropic.MessageBatchNewParamsRequestParams{
				Model:     classify.Model,
				MaxTokens: 16000,
				System:    []anthropic.TextBlockParam{{Text: classify.System(), CacheControl: anthropic.NewCacheControlEphemeralParam()}},
				Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
				OutputConfig: anthropic.OutputConfigParam{
					Effort: anthropic.OutputConfigEffortLow,
					Format: anthropic.JSONOutputFormatParam{Schema: classify.Schema()},
				},
			},
		})
	}

	batch, err := client.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: requests})
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tq := db.New(tx)
	if err := tq.InsertLLMBatch(ctx, db.InsertLLMBatchParams{
		BatchID: batch.ID, Task: classify.Task, PromptVersion: classify.PromptVersion, Model: classify.Model, Requests: int64(len(requests)),
	}); err != nil {
		return err
	}
	for customID, group := range groups {
		for _, it := range group {
			if err := tq.InsertLLMBatchItem(ctx, db.InsertLLMBatchItemParams{
				BatchID: batch.ID, CustomID: customID, CnID: it.CNID, InputHash: classify.InputHash(it),
			}); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("submitted batch %s: %d contracts in %d requests", batch.ID, len(items), len(requests))
	return nil
}

func collect(ctx context.Context, client anthropic.Client, conn *sql.DB) error {
	q := db.New(conn)
	open, err := q.OpenLLMBatches(ctx)
	if err != nil {
		return err
	}
	for _, b := range open {
		batch, err := client.Messages.Batches.Get(ctx, b.BatchID, anthropic.MessageBatchGetParams{})
		if err != nil {
			return err
		}
		if batch.ProcessingStatus != anthropic.MessageBatchProcessingStatusEnded {
			log.Printf("batch %s: %s", b.BatchID, batch.ProcessingStatus)
			continue
		}
		if err := store(ctx, client, conn, b); err != nil {
			return fmt.Errorf("batch %s: %w", b.BatchID, err)
		}
	}
	return nil
}

// store reads a finished batch's results and saves each classification.
func store(ctx context.Context, client anthropic.Client, conn *sql.DB, b db.OpenLLMBatchesRow) error {
	q := db.New(conn)
	rows, err := q.LLMBatchItems(ctx, b.BatchID)
	if err != nil {
		return err
	}
	sent := map[string][]classify.Item{}
	hashes := map[string]string{}
	for _, r := range rows {
		sent[r.CustomID] = append(sent[r.CustomID], classify.Item{CNID: r.CnID, Description: r.Description, Agency: r.Agency, Supplier: r.Supplier})
		hashes[r.CnID] = r.InputHash
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tq := db.New(tx)
	stored, failed := 0, 0
	stream := client.Messages.Batches.ResultsStreaming(ctx, b.BatchID, anthropic.MessageBatchResultsParams{})
	for stream.Next() {
		res := stream.Current()
		if res.Result.Type != "succeeded" {
			failed++
			log.Printf("%s: %s", res.CustomID, res.Result.Type)
			continue
		}
		msg := res.Result.Message
		if msg.StopReason != anthropic.StopReasonEndTurn {
			failed++
			log.Printf("%s: stopped with %s", res.CustomID, msg.StopReason)
			continue
		}
		var text string
		for _, block := range msg.Content {
			if t, ok := block.AsAny().(anthropic.TextBlock); ok {
				text += t.Text
			}
		}
		results, err := classify.Parse(text, sent[res.CustomID])
		if err != nil {
			failed++
			log.Printf("%s: %v", res.CustomID, err)
			continue
		}
		for _, r := range results {
			if err := tq.UpsertLLMClassification(ctx, db.UpsertLLMClassificationParams{
				CnID: r.CNID, Task: b.Task, PromptVersion: b.PromptVersion, Model: b.Model,
				Category: r.Category, Confidence: r.Confidence, InputHash: hashes[r.CNID], BatchID: b.BatchID,
			}); err != nil {
				return err
			}
			stored++
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if err := tq.MarkLLMBatchCollected(ctx, b.BatchID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("batch %s: stored %d classifications, %d requests failed", b.BatchID, stored, failed)
	return nil
}
