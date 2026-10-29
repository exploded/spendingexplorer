# Finds supplier websites for the top suppliers, cheapest source first.
# Run from the repo root on the PC (it writes to spending.db):
#
#   .\scripts\find-websites.ps1            # top 2,000 suppliers
#   .\scripts\find-websites.ps1 -Top 500
#
# 1. Wikidata (free) and domains built from names (free) run first.
# 2. The rest go to Claude with web search, after you confirm the cost.
# 3. It waits for the batch (usually under an hour), then checks each answer.
# Then snapshot and upload the database as usual.
param([int]$Top = 2000)
$ErrorActionPreference = 'Stop'

go build -o .local/find-websites.exe ./cmd/find-websites
if ($LASTEXITCODE) { exit 1 }
$fw = '.local/find-websites.exe'

& $fw wikidata -fetch
if ($LASTEXITCODE) { exit 1 }
& $fw guess -top $Top
if ($LASTEXITCODE) { exit 1 }

& $fw submit -top $Top -n
if ($LASTEXITCODE) { exit 1 }
$answer = Read-Host 'Send these to Claude web search? (y/n)'
if ($answer -ne 'y') { Write-Host 'Stopped before the paid step.'; exit 0 }

& $fw submit -top $Top
if ($LASTEXITCODE) { exit 1 }
& $fw collect -wait
if ($LASTEXITCODE) { exit 1 }
