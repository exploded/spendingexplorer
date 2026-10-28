# Federal Spending Explorer

A public, top-down visual explorer of Australian Commonwealth spending. Every number shown
sits inside a known whole-of-government total, and every box shows how much of it is itemised.

## Stack and conventions

- Go (stdlib `net/http` router, Go 1.22+ patterns), `html/template`, HTMX (self-hosted)
- SQLite with sqlc-generated query code
- Developed on Windows in VS Code; deployed to Debian (Linode) behind Caddy, run by systemd,
  CI/CD via GitHub Actions
- Data loaders are separate Go commands (e.g. `cmd/load-pbs`, `cmd/load-austender`) that write
  to SQLite; the web app only reads
- Australian spelling in UI copy

## Hierarchy (top down)

1. **Whole of government** - total expenses, by function (Budget Paper 1) and by expense type
   (Monthly Financial Statements / Final Budget Outcome). Employee expenses = all APS wages.
   2025-26 revised estimate ~$812bn; Final Budget Outcome 2025-26 (released 28 Sep 2026) has actuals.
2. **Portfolio > entity > outcome > program** - PBS "Program Expense Line Items" CSV, published
   on data.gov.au with each Budget (2025-26 dataset:
   https://data.gov.au/data/dataset/4f025f38-0c70-45e2-8106-4c29a5e48637; find the 2026-27 one).
   NOTE: line items do NOT sum to the BP1 total. Show the difference as an explicit, labelled
   "whole-of-government adjustments" slice; never hide it.
3. **Expense kind within each entity** - departmental (staff, suppliers, property) vs administered
   (pensions, grants, payments to states). Transparency Portal (transparency.gov.au) adds staff
   numbers by classification/salary level and actual consultancy/non-consultancy spend.
4. **Itemised leaves** - supplier spend > AusTender contract notices (OCDS API / data.gov.au
   exports, structured records, no PDFs); grants > GrantConnect; wages > staff counts only;
   personal benefits, Medicare and GST payments stay as totals.

## Rules

- Every node displays total, itemised amount, and itemised % (coverage is the point).
- Suppliers who are individuals (sole traders such as barristers) keep their name on contract
  and supplier pages, as AusTender publishes it, but are left out of site-wide rankings, signal
  indexes and "top supplier" lists (decided 2026-10-27). They still count in every total.
  Implemented in `supplier_individual`: ABR entity type IND decides for suppliers with an
  ABN; for the rest (mostly overseas), `austender.LooksLikePerson` checks the name against
  given names common in the ABR. An org-by-default name test was tried and rejected: it
  hid KPMG, Deloitte and Clayton Utz.
- "FMS ACCOUNT RESERVE BANK OF AUSTRALIA" is US Foreign Military Sales (Defence buying from the
  US Government), not a company. It stays in rankings, with a "US Foreign Military Sales" badge
  and an explanation (`austender.IsFMS`, `/methodology#fms`) (decided 2026-10-28).
- AusTender values are whole-of-life contract values at publication, not annual spend.
  Any annualised figure (value spread evenly start to end date) must be labelled an estimate.
- Machinery-of-government changes rename/move programs between years; track this in a
  program lineage table rather than overwriting.
- Budget estimates and actuals are different series; store the source document and estimate
  type on every amount.

## Proposed data model (SQLite)

- `entity` + `entity_alias` (AusTender agency names, former department names)
- `program` + `program_lineage` (restructures across budget years)
- `program_expense` (program, fiscal_year, estimate_type, source_document, departmental/
  administered, amount)
- `wog_expense` (fiscal_year, lens = function|type, category, amount, source_document)
- `contract` (AusTender; entity, supplier ABN, value, start, end, UNSPSC category,
  description, llm_class) + `supplier`
- `grant` (GrantConnect), later
- `person` + `office_holding` + `ministry` (Parliamentary Handbook, from May 2019; photos from
  Wikimedia Commons with licence and credit) + `minister_title` (title → portfolio, curated in
  `data/minister_titles.csv`; extend it when a new ministry brings new titles)

## LLM usage

- Uses Claude API via the Max plan's monthly API credits (Console org linked to the plan).
  Credits do not cover Claude Code and expire monthly; run backfills within one month.
- Use Haiku 5.5 on the Batch API for classification; group many descriptions per request.
- First LLM job: match programs across budget years (lineage). Second: reclassify AusTender
  contract descriptions (consultancy flag is unreliable); start with the "management and
  business professionals" category.
- On the PC the API key lives in `.local/anthropic.key` (gitignored, Default workspace,
  30-day expiry); `llm-classify` reads it and passes it to the client directly. Never set
  ANTHROPIC_API_KEY in the Windows environment (it would override the Claude Code subscription
  login). On the server it may sit in the service `.env` instead.

## First step

Load the 2025-26 and 2026-27 PBS line-item CSVs into SQLite, sum them, and compare against
the BP1 / Final Budget Outcome totals. If the gap is modest and explainable, the model holds;
if large, revisit before building views.

## Ruled out

Telco outage register aggregator: registers cover only unplanned regional/remote outages
(plus 100k+ service national outages); metro, planned and sub-threshold outages are missing,
and filling gaps needs scraping or scanning. Not pursued.
