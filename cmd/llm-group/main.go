// Command llm-group asks Claude which suppliers sharing a leading name word
// belong to one corporate group, through the Message Batches API.
//
//	llm-group submit   # queue clusters not yet answered, or changed since
//	llm-group collect  # store results of finished batches
//
// Run cmd/derive afterwards to rebuild the groups. The API key is read as in
// cmd/llm-classify: never set ANTHROPIC_API_KEY on a development PC.
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

	"spendingexplorer/internal/corpgroup"
	"spendingexplorer/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: llm-group submit|collect [flags]")
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dbPath := fs.String("db", "spending.db", "SQLite database path")
	keyFile := fs.String("key-file", ".local/anthropic.key", "file holding the API key; used when ANTHROPIC_API_KEY is not set")
	dryRun := fs.Bool("n", false, "submit: report what would be sent without sending")
	fs.Parse(os.Args[2:])

	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" && !*dryRun {
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
		err = submit(ctx, client, conn, *dryRun)
	case "collect":
		err = collect(ctx, client, conn)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Fatal(err)
	}
}

// clusters returns the current clusters by key.
func clusters(ctx context.Context, q *db.Queries) ([]corpgroup.Cluster, []string, error) {
	rows, err := q.SuppliersForGrouping(ctx)
	if err != nil {
		return nil, nil, err
	}
	seen := map[int64]bool{}
	var members []corpgroup.Member
	for _, r := range rows {
		if seen[r.ID] || corpgroup.ActsForTrust(r.Name) {
			continue
		}
		seen[r.ID] = true
		members = append(members, corpgroup.Member{ID: r.ID, Name: r.Name, EntityType: r.EntityType, Country: r.Country, GLEIFParent: r.GleifParent})
	}
	kept, tooLarge := corpgroup.Clusters(members)
	return kept, tooLarge, nil
}

func submit(ctx context.Context, client anthropic.Client, conn *sql.DB, dryRun bool) error {
	q := db.New(conn)
	all, tooLarge, err := clusters(ctx, q)
	if err != nil {
		return err
	}
	done, err := q.GroupedClusterHashes(ctx, corpgroup.PromptVersion)
	if err != nil {
		return err
	}
	answered := map[string]bool{}
	for _, d := range done {
		answered[d.ClusterKey+"\x00"+d.InputHash] = true
	}
	pending, err := q.PendingGroupClusters(ctx, corpgroup.PromptVersion)
	if err != nil {
		return err
	}
	waiting := map[string]bool{}
	for _, k := range pending {
		waiting[k] = true
	}
	var todo []corpgroup.Cluster
	suppliers := 0
	for _, c := range all {
		if !answered[c.Key+"\x00"+corpgroup.InputHash(c)] && !waiting[c.Key] {
			todo = append(todo, c)
			suppliers += len(c.Members)
		}
	}
	log.Printf("%d clusters, %d to send (%d suppliers); %d clusters too large to send: %s",
		len(all), len(todo), suppliers, len(tooLarge), strings.Join(tooLarge, ", "))
	if len(todo) == 0 || dryRun {
		return nil
	}

	// Whole clusters per request, up to about PerRequest suppliers.
	var requests []anthropic.MessageBatchNewParamsRequest
	sent := map[string][]corpgroup.Cluster{}
	for start := 0; start < len(todo); {
		end, n := start, 0
		for end < len(todo) && (end == start || n+len(todo[end].Members) <= corpgroup.PerRequest) {
			n += len(todo[end].Members)
			end++
		}
		group := todo[start:end]
		start = end
		prompt, err := corpgroup.Prompt(group)
		if err != nil {
			return err
		}
		customID := fmt.Sprintf("r%05d", len(requests))
		sent[customID] = group
		requests = append(requests, anthropic.MessageBatchNewParamsRequest{
			CustomID: customID,
			Params: anthropic.MessageBatchNewParamsRequestParams{
				Model:     corpgroup.Model,
				MaxTokens: 16000,
				System:    []anthropic.TextBlockParam{{Text: corpgroup.System(), CacheControl: anthropic.NewCacheControlEphemeralParam()}},
				Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
				OutputConfig: anthropic.OutputConfigParam{
					Effort: anthropic.OutputConfigEffortMedium,
					Format: anthropic.JSONOutputFormatParam{Schema: corpgroup.Schema()},
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
		BatchID: batch.ID, Task: corpgroup.Task, PromptVersion: corpgroup.PromptVersion, Model: corpgroup.Model, Requests: int64(len(requests)),
	}); err != nil {
		return err
	}
	for customID, group := range sent {
		for _, c := range group {
			hash := corpgroup.InputHash(c)
			for _, m := range c.Members {
				if err := tq.InsertLLMGroupBatchItem(ctx, db.InsertLLMGroupBatchItemParams{
					BatchID: batch.ID, CustomID: customID, ClusterKey: c.Key, InputHash: hash, SupplierID: m.ID,
				}); err != nil {
					return err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("submitted batch %s: %d clusters in %d requests", batch.ID, len(todo), len(requests))
	return nil
}

func collect(ctx context.Context, client anthropic.Client, conn *sql.DB) error {
	q := db.New(conn)
	open, err := q.OpenLLMBatches(ctx, corpgroup.Task)
	if err != nil {
		return err
	}
	if len(open) == 0 {
		log.Print("no batches waiting")
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

// store reads a finished batch's results and saves each cluster's answer.
// Clusters left out of a response are sent again by the next submit.
func store(ctx context.Context, client anthropic.Client, conn *sql.DB, b db.OpenLLMBatchesRow) error {
	q := db.New(conn)
	rows, err := q.LLMGroupBatchItems(ctx, b.BatchID)
	if err != nil {
		return err
	}
	// Rebuild the clusters as sent; the model only needs ids back.
	sent := map[string][]corpgroup.Cluster{}
	hashes := map[string]string{}
	for _, r := range rows {
		cs := sent[r.CustomID]
		if len(cs) == 0 || cs[len(cs)-1].Key != r.ClusterKey {
			cs = append(cs, corpgroup.Cluster{Key: r.ClusterKey})
		}
		cs[len(cs)-1].Members = append(cs[len(cs)-1].Members, corpgroup.Member{ID: r.SupplierID, Name: r.Name})
		sent[r.CustomID] = cs
		hashes[r.ClusterKey] = r.InputHash
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tq := db.New(tx)
	stored, grouped, failed, short := 0, 0, 0, 0
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
		results, err := corpgroup.Parse(text, sent[res.CustomID])
		if err != nil {
			failed++
			log.Printf("%s: %v", res.CustomID, err)
			continue
		}
		answered := map[string]bool{}
		for _, a := range results {
			if !answered[a.ClusterKey] {
				answered[a.ClusterKey] = true
				if err := tq.DeleteLLMSupplierGroupCluster(ctx, db.DeleteLLMSupplierGroupClusterParams{ClusterKey: a.ClusterKey, PromptVersion: b.PromptVersion}); err != nil {
					return err
				}
			}
			if err := tq.UpsertLLMSupplierGroup(ctx, db.UpsertLLMSupplierGroupParams{
				SupplierID: a.SupplierID, PromptVersion: b.PromptVersion, ClusterKey: a.ClusterKey, InputHash: hashes[a.ClusterKey],
				GroupLabel: a.Label, GroupName: a.GroupName, Confidence: a.Confidence, Model: b.Model, BatchID: b.BatchID,
			}); err != nil {
				return err
			}
			stored++
			if a.Label != "" {
				grouped++
			}
		}
		if missing := len(sent[res.CustomID]) - len(answered); missing > 0 {
			short += missing
			log.Printf("%s: %d of %d clusters missing from the response", res.CustomID, missing, len(sent[res.CustomID]))
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
	log.Printf("batch %s: stored %d suppliers (%d in groups), %d requests failed, %d clusters to resubmit", b.BatchID, stored, grouped, failed, short)
	return nil
}
