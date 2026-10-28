#!/bin/sh
# Reloads the PBS and BP1 files in data/raw into spending.db. Loaders replace
# their own rows, so contract data is kept. Pass --fresh to start from empty.
set -e
if [ "$1" = "--fresh" ]; then
  rm -f spending.db spending.db-wal spending.db-shm
fi
go run ./cmd/load-pbs -budget 2025-26 -file data/raw/pbs-2025-26.csv -url "https://data.gov.au/data/dataset/4f025f38-0c70-45e2-8106-4c29a5e48637/resource/7cc8640f-b5d4-41de-a665-3197739b965f/download/2025-26-pbs-program-expense-line-items-final-2025-03-26.csv"
go run ./cmd/load-pbs -budget 2026-27 -file data/raw/pbs-2026-27.xlsx -url "https://data.gov.au/data/dataset/5400a217-82f4-425c-b713-b7ba2ee78e5a/resource/689b0a92-3c46-4f8f-b9cf-a86dd7e384f0/download/2026-27-pbs-4.updated-2026-06-18-w.-corrigenda.xlsx"
go run ./cmd/load-bp1 -budget 2025-26 -file data/raw/bp1-2025-26-table-5.3.csv -url "https://data.gov.au/data/dataset/4f025f38-0c70-45e2-8106-4c29a5e48637/resource/b9c368f4-f7de-4a44-9917-338f596a2b85/download/bp1.zip"
go run ./cmd/load-bp1 -budget 2026-27 -file data/raw/bp1-2026-27-table-6.3.csv -url "https://data.gov.au/data/dataset/5400a217-82f4-425c-b713-b7ba2ee78e5a/resource/e8615266-48a4-4258-9c3b-9283f4c593cd/download/budget-paper-no.1-tables.zip"
go run ./cmd/load-bp1 -outcome -budget 2025-26 -file data/raw/fbo-2025-26-table-a.1.csv -url "https://archive.budget.gov.au/2025-26/fbo/download/00_fbo_2025-26.pdf"
go run ./cmd/load-subfunctions -budget 2026-27 -file data/raw/bp1-2026-27-table-a.6.1.csv -table "Table A.6.1" -title "2026-27 Budget Paper No. 1 – Statement 6, Appendix A: Expense by function and subfunction" -url "https://budget.gov.au/content/bp1/download/bp1_bs-6.docx"
go run ./cmd/load-subfunctions -budget 2026-27 -file data/raw/bp3-2026-27-general-revenue.csv -table "Table 3.1" -doc-type bp3_general_revenue -title "2026-27 Budget Paper No. 3 – Federal Financial Relations" -url "https://budget.gov.au/content/bp3/download/bp3_2026-27.pdf"
go run ./cmd/load-descriptions -budget 2026-27
go run ./cmd/load-investments
go run ./cmd/load-projects
if ls .local/abr/public_split_*.zip >/dev/null 2>&1; then
  go run ./cmd/load-abr
fi
if ls .local/gleif/*lei2*.csv.zip >/dev/null 2>&1; then
  go run ./cmd/load-gleif
fi
if ls data/raw/transparency/*.csv >/dev/null 2>&1; then
  go run ./cmd/load-transparency
fi
if ls data/raw/grantconnect/*.xlsx >/dev/null 2>&1; then
  go run ./cmd/load-grants
fi
go run ./cmd/derive
