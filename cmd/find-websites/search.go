package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"spendingexplorer/internal/db"
	"spendingexplorer/internal/website"
)

// searchCost is what one web search costs, in US dollars.
const searchCost = 0.01

func client(o options) (anthropic.Client, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		b, err := os.ReadFile(o.keyFile)
		if err != nil {
			return anthropic.Client{}, fmt.Errorf("no API key: ANTHROPIC_API_KEY is not set and %s cannot be read", o.keyFile)
		}
		key = strings.TrimSpace(string(b))
	}
	return anthropic.NewClient(option.WithAPIKey(key)), nil
}

func params(s website.Supplier, c website.Context) anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		Model:     website.Model,
		MaxTokens: 8000,
		System:    []anthropic.TextBlockParam{{Text: website.System()}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(website.Prompt(s, c)))},
		Tools: []anthropic.ToolUnionParam{{OfWebSearchTool20250305: &anthropic.WebSearchTool20250305Param{
			MaxUses:      anthropic.Int(website.MaxSearches),
			UserLocation: anthropic.UserLocationParam{Country: anthropic.String("AU")},
		}}},
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow},
	}
}

func text(msg anthropic.Message) string {
	var b strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// toSearch returns the top suppliers with no site that haven't been searched.
func toSearch(ctx context.Context, q *db.Queries, top int) ([]db.WebsiteCandidatesRow, error) {
	rows, err := candidates(ctx, q, top)
	if err != nil {
		return nil, err
	}
	searched, err := q.SearchedSuppliers(ctx)
	if err != nil {
		return nil, err
	}
	done := map[int64]bool{}
	for _, id := range searched {
		done[id] = true
	}
	var out []db.WebsiteCandidatesRow
	for _, r := range rows {
		if r.HasSite == 0 && !done[r.ID] {
			out = append(out, r)
		}
	}
	return out, nil
}

func submit(ctx context.Context, conn *sql.DB, o options) error {
	q := db.New(conn)
	todo, err := toSearch(ctx, q, o.top)
	if err != nil {
		return err
	}
	// Tokens cost about a tenth of a cent a supplier at batch prices; the
	// searches are most of it.
	log.Printf("submit: %d of the top %d suppliers to search; at most %d searches, US$%.2f, plus about US$%.2f of tokens",
		len(todo), o.top, len(todo)*website.MaxSearches, float64(len(todo)*website.MaxSearches)*searchCost, float64(len(todo))*0.002)
	if len(todo) == 0 || o.dryRun {
		return nil
	}
	cl, err := client(o)
	if err != nil {
		return err
	}
	requests := make([]anthropic.MessageBatchNewParamsRequest, len(todo))
	for i, r := range todo {
		p := params(supplierOf(r), website.Context{Agency: r.TopAgency, Description: r.TopDescription})
		requests[i] = anthropic.MessageBatchNewParamsRequest{
			CustomID: "s" + strconv.FormatInt(r.ID, 10),
			Params: anthropic.MessageBatchNewParamsRequestParams{
				Model: p.Model, MaxTokens: p.MaxTokens, System: p.System, Messages: p.Messages, Tools: p.Tools, OutputConfig: p.OutputConfig,
			},
		}
	}
	batch, err := cl.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: requests})
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
		BatchID: batch.ID, Task: website.Task, PromptVersion: website.PromptVersion, Model: website.Model, Requests: int64(len(requests)),
	}); err != nil {
		return err
	}
	for i, r := range todo {
		if err := tq.InsertLLMWebsiteBatchItem(ctx, db.InsertLLMWebsiteBatchItemParams{
			BatchID: batch.ID, CustomID: requests[i].CustomID, SupplierID: r.ID,
		}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("submitted batch %s: %d suppliers. Run find-websites collect -wait", batch.ID, len(requests))
	return nil
}

func collect(ctx context.Context, conn *sql.DB, o options) error {
	q := db.New(conn)
	cl, err := client(o)
	if err != nil {
		return err
	}
	for {
		open, err := q.OpenLLMBatches(ctx, website.Task)
		if err != nil {
			return err
		}
		if len(open) == 0 {
			log.Print("no batches waiting")
			return nil
		}
		waiting := 0
		for _, b := range open {
			batch, err := cl.Messages.Batches.Get(ctx, b.BatchID, anthropic.MessageBatchGetParams{})
			if err != nil {
				return err
			}
			if batch.ProcessingStatus != anthropic.MessageBatchProcessingStatusEnded {
				waiting++
				c := batch.RequestCounts
				log.Printf("batch %s: %s (%d done, %d processing)", b.BatchID, batch.ProcessingStatus, c.Succeeded+c.Errored, c.Processing)
				continue
			}
			if err := store(ctx, cl, conn, b, o.workers); err != nil {
				return fmt.Errorf("batch %s: %w", b.BatchID, err)
			}
		}
		if waiting == 0 || !o.wait {
			return nil
		}
		time.Sleep(time.Minute)
	}
}

type answer struct {
	s        website.Supplier
	url      string
	text     string
	searches int64
	result   website.Result
}

// store reads a finished batch, checks each site the model gave, and saves
// both the answer and the check. Suppliers whose request failed are left for
// the next submit.
func store(ctx context.Context, cl anthropic.Client, conn *sql.DB, b db.OpenLLMBatchesRow, workers int) error {
	q := db.New(conn)
	items, err := q.LLMWebsiteBatchItems(ctx, b.BatchID)
	if err != nil {
		return err
	}
	sent := map[string]website.Supplier{}
	for _, it := range items {
		sent[it.CustomID] = website.Supplier{ID: it.ID, Name: it.Name, ABN: it.Abn, Postcode: it.Postcode, Country: it.Country}
	}
	var answers []answer
	failed, searches := 0, int64(0)
	stream := cl.Messages.Batches.ResultsStreaming(ctx, b.BatchID, anthropic.MessageBatchResultsParams{})
	for stream.Next() {
		res := stream.Current()
		s, ok := sent[res.CustomID]
		if !ok {
			continue
		}
		if res.Result.Type != "succeeded" {
			failed++
			log.Printf("%s: %s", s.Name, res.Result.Type)
			continue
		}
		msg := res.Result.Message
		searches += msg.Usage.ServerToolUse.WebSearchRequests
		if msg.StopReason != anthropic.StopReasonEndTurn {
			failed++
			log.Printf("%s: stopped with %s", s.Name, msg.StopReason)
			continue
		}
		t := text(msg)
		u, err := website.Parse(t)
		if err != nil {
			failed++
			log.Printf("%s: %v", s.Name, err)
			continue
		}
		answers = append(answers, answer{s: s, url: u, text: t, searches: msg.Usage.ServerToolUse.WebSearchRequests})
	}
	if err := stream.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	ck := website.NewChecker()
	checked := map[int64]website.Result{}
	if err := each(answers, workers, func(a answer) website.Result {
		if a.url == "" {
			return website.Result{}
		}
		return ck.Visit(ctx, a.s, a.url, website.Plain)
	}, func(a answer, r website.Result) error {
		checked[a.s.ID] = r
		return nil
	}); err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tq := db.New(tx)
	found, none, rejected := 0, 0, 0
	for _, a := range answers {
		if err := tq.PutWebsiteSearch(ctx, db.PutWebsiteSearchParams{
			SupplierID: a.s.ID, Url: a.url, Answer: a.text, WebSearches: a.searches, Model: b.Model, PromptVersion: b.PromptVersion,
			BatchID: b.BatchID, SearchedAt: today(),
		}); err != nil {
			return err
		}
		if a.url == "" {
			none++
			continue
		}
		r := checked[a.s.ID]
		if err := tq.PutWebsiteCheck(ctx, db.PutWebsiteCheckParams{
			SupplierID: a.s.ID, Url: a.url, Method: "search", Passed: boolInt(r.Passed), Detail: r.Evidence + r.Detail, CheckedAt: today(),
		}); err != nil {
			return err
		}
		if !r.Passed {
			rejected++
			log.Printf("rejected %s: %s (%s)", a.s.Name, a.url, r.Detail)
			continue
		}
		found++
		if err := tq.AddWebsite(ctx, db.AddWebsiteParams{
			SupplierID: a.s.ID, Url: website.Home(r.URL), Basis: "search", Evidence: r.Evidence, CheckedAt: today(),
		}); err != nil {
			return err
		}
	}
	if err := tq.MarkLLMBatchCollected(ctx, b.BatchID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("batch %s: %d sites linked, %d rejected by the checks, %d with no site, %d requests failed (resubmit to retry); %d web searches (US$%.2f)",
		b.BatchID, found, rejected, none, failed, searches, float64(searches)*searchCost)
	return nil
}

// test searches for one supplier and prints the answer and its check.
func test(ctx context.Context, conn *sql.DB, o options) error {
	q := db.New(conn)
	rows, err := candidates(ctx, q, 1_000_000)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.ID != o.id {
			continue
		}
		cl, err := client(o)
		if err != nil {
			return err
		}
		s := supplierOf(r)
		c := website.Context{Agency: r.TopAgency, Description: r.TopDescription}
		fmt.Print(website.Prompt(s, c))
		msg, err := cl.Messages.New(ctx, params(s, c))
		if err != nil {
			return err
		}
		t := text(*msg)
		fmt.Printf("\n--- %s, %d web searches, %d in / %d out tokens\n%s\n---\n",
			msg.StopReason, msg.Usage.ServerToolUse.WebSearchRequests, msg.Usage.InputTokens, msg.Usage.OutputTokens, t)
		u, err := website.Parse(t)
		if err != nil || u == "" {
			return err
		}
		res := website.NewChecker().Visit(ctx, s, u, website.Plain)
		fmt.Printf("check: passed=%v %s%s (%s)\n", res.Passed, res.Evidence, res.Detail, res.URL)
		return nil
	}
	return fmt.Errorf("supplier %d not found, or an individual", o.id)
}
