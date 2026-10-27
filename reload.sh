#!/bin/sh
# Rebuilds spending.db from the raw files in data/raw.
set -e
rm -f spending.db spending.db-wal spending.db-shm
go run ./cmd/load-pbs -budget 2025-26 -file data/raw/pbs-2025-26.csv -url "https://data.gov.au/data/dataset/4f025f38-0c70-45e2-8106-4c29a5e48637/resource/7cc8640f-b5d4-41de-a665-3197739b965f/download/2025-26-pbs-program-expense-line-items-final-2025-03-26.csv"
go run ./cmd/load-pbs -budget 2026-27 -file data/raw/pbs-2026-27.xlsx -url "https://data.gov.au/data/dataset/5400a217-82f4-425c-b713-b7ba2ee78e5a/resource/689b0a92-3c46-4f8f-b9cf-a86dd7e384f0/download/2026-27-pbs-4.updated-2026-06-18-w.-corrigenda.xlsx"
go run ./cmd/load-bp1 -budget 2025-26 -file data/raw/bp1-2025-26-table-5.3.csv -url "https://data.gov.au/data/dataset/4f025f38-0c70-45e2-8106-4c29a5e48637/resource/b9c368f4-f7de-4a44-9917-338f596a2b85/download/bp1.zip"
go run ./cmd/load-bp1 -budget 2026-27 -file data/raw/bp1-2026-27-table-6.3.csv -url "https://data.gov.au/data/dataset/5400a217-82f4-425c-b713-b7ba2ee78e5a/resource/e8615266-48a4-4258-9c3b-9283f4c593cd/download/budget-paper-no.1-tables.zip"
