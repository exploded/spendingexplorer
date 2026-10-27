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
