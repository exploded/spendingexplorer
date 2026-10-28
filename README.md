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

## Deploy

The site runs at https://spending.mchugh.au on the Linode: service `spendingexplorer`,
directory `/var/www/spendingexplorer`, port 8995, behind Caddy. Pushing to `main` runs
`.github/workflows/deploy.yml`, which tests, builds the Linux binary and installs it with
`scripts/deploy-spendingexplorer`.

The database is not built on the server. Build it on the PC, then upload a snapshot.

PowerShell on your PC, in `C:\Projects\go\spendingexplorer`:

```
go run ./cmd/snapshot-db                        # writes dist/spending.db
scp -C dist/spending.db linode:/tmp/spending.db
```

On the Linode (`ssh linode`):

```
sudo install-db-spendingexplorer /tmp/spending.db
```

`snapshot-db` uses `VACUUM INTO`, so a loader running at the same time can't leave a
half-written copy, and switches the copy to a rollback journal. `install-db-spendingexplorer`
swaps the file in and restarts the service, which keeps the old file open until it restarts.

### Downloads and sitemap

The `/data` page offers the whole database as `spending-sqlite.zip` and CSVs of the main
tables as `spending-csv.zip`. The app builds both itself (`internal/export`), in the
background, when it starts on a database it hasn't exported yet. That takes about 20 seconds
and needs about 550 MB of free disk while it runs. Until a build finishes, the page serves the
previous one. Files go in `downloads/` beside the database, or in `DOWNLOADS_DIR` if set.
Change the CSV columns in `internal/export` and bump its `version` to force a rebuild.

`/sitemap.xml` is a sitemap index, listed in `robots.txt`. It points to one file for the
explorer's pages, one for suppliers (individuals left out) and one per 50,000 contracts.

First-time server setup is `scripts/server-setup.sh`:

```
curl -fsSL https://raw.githubusercontent.com/exploded/spendingexplorer/main/scripts/server-setup.sh | sudo bash
```

## Load and reconcile

Raw files live in `data/raw/` and are committed, so the server can rebuild the database. Sources are on data.gov.au:

- 2025-26 Budget and PBS tables: https://data.gov.au/data/dataset/4f025f38-0c70-45e2-8106-4c29a5e48637
- 2026-27 Budget and PBS tables: https://data.gov.au/data/dataset/5400a217-82f4-425c-b713-b7ba2ee78e5a

Each loader records the file's URL and SHA-256 in `source_document`, logs a `load_run`,
and replaces its own earlier rows when re-run.

```
go run ./cmd/load-pbs -budget 2025-26 -file data/raw/pbs-2025-26.csv -url <csv url>
go run ./cmd/load-pbs -budget 2026-27 -file data/raw/pbs-2026-27.xlsx -url <xlsx url>
go run ./cmd/load-bp1 -budget 2025-26 -file data/raw/bp1-2025-26-table-5.3.csv -url <bp1 zip url>
go run ./cmd/load-bp1 -budget 2026-27 -file data/raw/bp1-2026-27-table-6.3.csv -url <bp1 zip url>
go run ./cmd/load-bp1 -outcome -budget 2025-26 -file data/raw/fbo-2025-26-table-a.1.csv -url <fbo pdf url>
go run ./cmd/load-subfunctions -budget 2026-27 -file data/raw/bp1-2026-27-table-a.6.1.csv -table "Table A.6.1" -title T -url <bp1 statement 6 url>
go run ./cmd/load-subfunctions -budget 2026-27 -file data/raw/bp3-2026-27-general-revenue.csv -table "Table 3.1" -doc-type bp3_general_revenue -title T -url <bp3 pdf url>
go run ./cmd/reconcile
```

`reload.sh` runs all four loaders against a fresh `spending.db`. An entity keeps one row across
budget years: `data/entity_names.csv` maps renamed, misspelt or shortened PBS names to it, and
`load-pbs` merges any entity row an earlier load created under such a name. `reconcile` prints, per publication and year: line items by treatment rule, the itemised
total, the BP1 total expenses, and the difference ("whole-of-government adjustments").
Treatment rules are in `internal/pbs/treatment.go`.

Sub-functions come from Appendix A of BP1 Statement 6, which is published only as Word and
PDF, not in the data.gov.au tables zip. `data/raw/bp1-2026-27-table-a.6.1.csv` was extracted
from the Word file's table rows, with wrapped labels joined. `data/raw/bp3-2026-27-general-revenue.csv`
is typed in from BP3 Table 3.1 and the Financial Assistance Grant table, because the PDF
text misaligns Table 3.1's columns. `load-subfunctions` stops if sub-functions don't add up
to the BP1 function totals; it prints, without stopping, any gap between a BP1 sub-function
and the BP3 rows that itemise it. Descriptions shown under each row are in
`data/subfunction_notes.csv`.

## AusTender contracts

```
go run ./cmd/load-austender -from 2022-07-01   # weekly windows; cached in .local/austender
go run ./cmd/derive                            # rebuild annualised contract_fy
```

The loader keeps the latest release of each contract, fetches the full history of amended
contracts to record their original value, and matches agency names to PBS entities, including
names from earlier PBS files. Reviewed aliases are in `data/agency_aliases.csv`; a row with no
entity leaves that agency unmatched on purpose (for example the Reserve Bank, which is outside
the general government sector). Re-running only fetches what is not cached, plus the most
recent week. To re-match agency names after editing either name file, without fetching:

```
go run ./cmd/load-austender -match-only
```

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

## Corporate groups of suppliers

`/suppliers` ranks suppliers with companies of one corporate group combined (for example
Boeing's Australian and US companies, or KPMG's partnership and its service companies). A
group is formed in two ways, and each member's page and the group's page say which:

- **GLEIF parent (reported).** Download the LEI-CDF and relationship-record full files, CSV
  format, from https://www.gleif.org/en/lei-data/gleif-golden-copy/download-the-golden-copy
  (about 500 MB, CC0) into `.local/gleif`. `load-gleif` matches Australian LEI records to
  supplier ABNs by ACN and keeps each one's current ultimate parent.
- **AI-grouped.** `llm-group` clusters suppliers by the first word of their names and asks
  Claude (`claude-haiku-5-5`, Batch API) which of them belong together. Only clusters that
  are new or changed are sent; the first full run (about 3,900 clusters) costs well under $1.

PowerShell on your PC, in `C:\Projects\go\spendingexplorer`:

```
go run ./cmd/load-gleif           # LEI records and parents for supplier companies
go run ./cmd/llm-group submit     # -n shows what would be sent
go run ./cmd/llm-group collect    # once the batch has ended
go run ./cmd/derive               # rebuild supplier_group
```

## Loans and investments

Loans and equity the government provides are not expenses, so they sit outside the total. Treasury
itemises them in the table that reconciles the underlying and headline cash balances: Budget
Paper No. 1, Statement 3, and the Final Budget Outcome, Part 1. These are PDF only. The
publications are listed in `data/investment_documents.csv`; extraction needs `pdftotext` and runs
locally, and its output, `data/investments.csv`, is committed so the server only loads it.

```
go run ./cmd/extract-investments   # check that every column sums to the published total
go run ./cmd/load-investments
```

To add a new Budget or Final Budget Outcome, add a row to `data/investment_documents.csv`.

Major projects funded this way, and every published estimate of their cost, are curated by hand in
`data/projects.csv` and `data/project_estimates.csv` (amounts in $m). Each estimate needs its
source URL and the source's words containing the figure; a video link is optional. A project's
`item_keys` (separated by `|`) link it to the investment lines that fund it. Its `entities` (entity
names) and `supplier_abns`, also separated by `|`, make those department and supplier pages link to
the project; `links_note` records the source for each link. An unknown entity name stops the load.

```
go run ./cmd/load-projects
```

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
repeated limited tenders to one supplier, limited tenders that grew past the threshold, and
suppliers whose ABN had been active for under 90 days at award (from the ABR status date).
The threshold follows CPR paragraph 9.7 (the `tender_threshold` column of the `contract_pub`
view): $80,000, or $125,000 for contracts published from 17 November 2025; $400,000 for
corporate entities, listed in `data/corporate_agencies.csv` from Finance's List of
Commonwealth Entities and Companies; and $7.5 million for construction (UNSPSC segment 72).
Thresholds were calibrated on the full load from July 2022. Each rule states its threshold,
the rule or practice it relates to, and common legitimate explanations. The site shows them
at `/signals`, on entity pages and on contract pages.

## Contract spending reported by entities (Transparency Portal)

Non-corporate entities report their consultancy and other contract spending, and the
organisations paid the most, in their annual reports. The Transparency Portal has no bulk
download or API we may use, so the four data sets are exported by hand from its Data
Explorer (https://www.transparency.gov.au/): choose the data sets under "Consultancy and
non-consultancy contracts", two at a time, with Body Type = non-corporate Commonwealth entity
and the financial years only (up to 10). Save the exports, with the names the portal gives
them, in `data/raw/transparency/`, then:

```
go run ./cmd/load-transparency
```

Every figure is checked against the entity's Budget expenses, its contract count and its own
total (rules in `internal/transparency`). Figures that fail are kept as published and marked
"looks wrong" on the site; none of these figures is used in any total.

## Grants (GrantConnect)

GrantConnect has no API, and its Grant Award List search exports at most 50,000 awards. Its
export also repeats blocks of 10,000 rows in large searches, so search by Publish Date over
ranges that return 10,000 awards or fewer ("Showing 1-15 of N records"), and use Download
Results for each. Save the exports, with the names GrantConnect gives them, in
`data/raw/grantconnect/`, then:

```
go run ./cmd/load-grants
```

The loader skips capped exports, counts each award once by its GA ID (latest version), matches
agencies to entities, and spreads each award over its term in `grant_fy` (labelled estimate).
It writes only its own tables. Check its log: the files' award counts should add up to the
search's total for the whole period.

## AI classification

`cmd/llm-classify` sends contract descriptions to Claude (`claude-haiku-5-5`) through the
Message Batches API, 100 contracts per request, with a fixed prompt and a structured-output
schema. Results are stored per task and prompt version in `llm_classification`.

It reads the API key from `.local/anthropic.key` (not committed), or from `ANTHROPIC_API_KEY`
when that is set, as in a server's service `.env`. Never set `ANTHROPIC_API_KEY` on the
Windows PC; it would override the Claude Code login.

PowerShell on your PC, in `C:\Projects\go\spendingexplorer`:

```
go run ./cmd/llm-classify submit -limit 5000   # queue unclassified UNSPSC 80 contracts
go run ./cmd/llm-classify collect              # store results once batches have ended
```
