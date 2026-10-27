# Federal Spending Explorer

A top-down explorer of Australian Commonwealth spending. See `CLAUDE.md` for the design.

## Run locally

PowerShell on your PC, in `C:\Projects\go\spendingexplorer`:

```
sh reload.sh      # rebuild spending.db from data/raw
.\build.bat       # build and serve on PORT from .env (default 8080)
```

The web app opens the database read-only. Templates and static files are embedded in the
binary.

## Load and reconcile

Raw files live in `data/raw/` (not committed). Sources are on data.gov.au:

- 2025-26 Budget and PBS tables: https://data.gov.au/data/dataset/4f025f38-0c70-45e2-8106-4c29a5e48637
- 2026-27 Budget and PBS tables: https://data.gov.au/data/dataset/5400a217-82f4-425c-b713-b7ba2ee78e5a

Each loader records the file's URL and SHA-256 in `source_document`, logs a `load_run`,
and replaces its own earlier rows when re-run.

```
go run ./cmd/load-pbs -budget 2025-26 -file data/raw/pbs-2025-26.csv -url <csv url>
go run ./cmd/load-pbs -budget 2026-27 -file data/raw/pbs-2026-27.xlsx -url <xlsx url>
go run ./cmd/load-bp1 -budget 2025-26 -file data/raw/bp1-2025-26-table-5.3.csv -url <bp1 zip url>
go run ./cmd/load-bp1 -budget 2026-27 -file data/raw/bp1-2026-27-table-6.3.csv -url <bp1 zip url>
go run ./cmd/reconcile
```

`reload.sh` runs all four loaders against a fresh `spending.db`. `reconcile` prints, per publication and year: line items by treatment rule, the itemised
total, the BP1 total expenses, and the difference ("whole-of-government adjustments").
Treatment rules are in `internal/pbs/treatment.go`.

## AusTender contracts

```
go run ./cmd/load-austender -from 2022-07-01   # weekly windows; cached in .local/austender
go run ./cmd/derive                            # rebuild annualised contract_fy
```

The loader keeps the latest release of each contract, fetches the full history of amended
contracts to record their original value, and matches agency names to PBS entities
(reviewed aliases in `data/agency_aliases.csv`). Re-running only fetches what is not cached,
plus the most recent week.

## Supplier entity types (ABR)

Suppliers registered as individuals or sole traders are left out of rankings (the rule is
in `CLAUDE.md`). Their entity type comes from the ABN Bulk Extract, refreshed weekly at
https://data.gov.au/data/dataset/abn-bulk-extract. Download the two `public_split_*.zip`
files (about 1 GB) into `.local/abr`, then:

```
go run ./cmd/load-abr   # entity type and status for supplier ABNs; given-name counts
go run ./cmd/derive     # rebuild supplier_individual
```

The loader scans about 20 million records in seconds and keeps only supplier ABNs. It
stores no individual's name, only counts of given names (kept at 200 registrations or
more), which `derive` uses to spot people's names among suppliers with no ABN.

## Program descriptions

Program objectives are quoted from each entity's PBS document. Extraction needs
`pdftotext` (poppler) and runs locally; its output, `data/program_descriptions_<year>.csv`,
is committed so the server only loads it.

```
go run ./cmd/extract-descriptions -crawl   # rebuild data/pbs_documents_<year>.csv
go run ./cmd/extract-descriptions          # download, extract, match, verify
go run ./cmd/load-descriptions
```

A quote is kept only when the cited page shows it directly under its own program heading.
Documents for other years, Additional Estimates and "linked programs" text are rejected.
PM&C's site blocks automated requests, so its documents need adding by hand.

## Signals ("worth a closer look")

`go run ./cmd/derive` rebuilds the `signal` table from the rules in `internal/signals`:
value increased after award, published long after it started (singly, or in a batch of ten
or more from one agency on one day), several contracts just under the open-tender threshold,
repeated limited tenders to one supplier, and limited tenders that grew past the threshold.
The threshold follows CPR paragraph 9.7 (the `tender_threshold` column of the `contract_pub`
view): $80,000, or $125,000 for contracts published from 17 November 2025; $400,000 for
corporate entities, listed in `data/corporate_agencies.csv` from Finance's List of
Commonwealth Entities and Companies; and $7.5 million for construction (UNSPSC segment 72).
Thresholds were calibrated on the full load from July 2022. Each rule states its threshold,
the rule or practice it relates to, and common legitimate explanations. The site shows them
at `/signals`, on entity pages and on contract pages.

## AI classification (server only)

`cmd/llm-classify` sends contract descriptions to Claude (`claude-haiku-5-5`) through the
Message Batches API, 100 contracts per request, with a fixed prompt and a structured-output
schema. Results are stored per task and prompt version in `llm_classification`.

On the Linode (`ssh -p 2222 deploy@194.195.254.92`), with `ANTHROPIC_API_KEY` in the
service `.env`:

```
llm-classify submit -limit 5000   # queue unclassified UNSPSC 80 contracts
llm-classify collect              # store results once batches have ended
```

Never set `ANTHROPIC_API_KEY` on the Windows PC; it would override the Claude Code login.
