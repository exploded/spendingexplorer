-- Every figure traces back to a source_document, and every loader run is
-- logged in load_run so the site can publish a data changelog.

CREATE TABLE IF NOT EXISTS source_document (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  title         TEXT NOT NULL,
  publisher     TEXT NOT NULL,
  url           TEXT NOT NULL,
  file_name     TEXT NOT NULL,
  sha256        TEXT NOT NULL UNIQUE,
  doc_type      TEXT NOT NULL,           -- pbs_line_items | bp1_function
  budget_year   TEXT NOT NULL,           -- publication's budget year, e.g. 2025-26
  retrieved_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS load_run (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  command             TEXT NOT NULL,
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id),
  rows_read           INTEGER NOT NULL,
  rows_written        INTEGER NOT NULL,
  notes               TEXT NOT NULL DEFAULT '',
  finished_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS entity (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS entity_alias (
  alias      TEXT PRIMARY KEY,
  entity_id  INTEGER NOT NULL REFERENCES entity(id)
);

-- Programs are per budget year; program_lineage links them across years.
-- Portfolio lives here because machinery-of-government changes move entities.
CREATE TABLE IF NOT EXISTS program (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_id    INTEGER NOT NULL REFERENCES entity(id),
  budget_year  TEXT NOT NULL,
  portfolio    TEXT NOT NULL,
  outcome      TEXT NOT NULL,
  name         TEXT NOT NULL,
  UNIQUE (entity_id, budget_year, outcome, name)
);

CREATE TABLE IF NOT EXISTS program_lineage (
  from_program_id  INTEGER NOT NULL REFERENCES program(id),
  to_program_id    INTEGER NOT NULL REFERENCES program(id),
  relation         TEXT NOT NULL,        -- same | renamed | split | merged | moved
  method           TEXT NOT NULL,        -- rule | llm | manual
  confidence       REAL,
  PRIMARY KEY (from_program_id, to_program_id)
);

-- One row per PBS line item per fiscal year column.
-- treatment says whether the line counts towards itemised spending, and
-- treatment_rule names the rule in internal/pbs that decided it.
CREATE TABLE IF NOT EXISTS program_expense (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  program_id          INTEGER NOT NULL REFERENCES program(id),
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id),
  source_table        TEXT NOT NULL,
  source_row          INTEGER NOT NULL,  -- row number in the published file, header = 1
  fiscal_year         TEXT NOT NULL,
  estimate_type       TEXT NOT NULL CHECK (estimate_type IN ('estimated_actual','budget','forward_estimate','actual')),
  expense_kind        TEXT NOT NULL CHECK (expense_kind IN ('administered','departmental','corporate')),
  line_item           TEXT NOT NULL,
  amount              INTEGER,           -- whole dollars; NULL when not for publication
  not_published       INTEGER NOT NULL DEFAULT 0,
  treatment           TEXT NOT NULL CHECK (treatment IN ('expense','transfer','subtotal','other_economic_flow')),
  treatment_rule      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS program_expense_doc_year ON program_expense(source_document_id, fiscal_year);

CREATE TABLE IF NOT EXISTS wog_expense (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id),
  fiscal_year         TEXT NOT NULL,
  estimate_type       TEXT NOT NULL,
  lens                TEXT NOT NULL CHECK (lens IN ('function','type')),
  category            TEXT NOT NULL,     -- 'Total expenses' holds the published total
  amount              INTEGER NOT NULL,  -- whole dollars
  UNIQUE (source_document_id, fiscal_year, lens, category)
);

-- AusTender ---------------------------------------------------------------

-- Procuring entities as named in AusTender, matched to PBS entities.
CREATE TABLE IF NOT EXISTS agency (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT NOT NULL UNIQUE,
  abn           TEXT NOT NULL DEFAULT '',
  entity_id     INTEGER REFERENCES entity(id),
  match_method  TEXT NOT NULL DEFAULT ''   -- exact | normalised | manual | llm
);

-- Suppliers are keyed by ABN, or by normalised name when no ABN is given.
CREATE TABLE IF NOT EXISTS supplier (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  key       TEXT NOT NULL UNIQUE,
  abn       TEXT NOT NULL DEFAULT '',
  name      TEXT NOT NULL,
  country   TEXT NOT NULL DEFAULT '',
  postcode  TEXT NOT NULL DEFAULT ''
);

-- Latest state of each contract notice. Values are whole-of-life, in whole
-- dollars, as published; they are not annual spending.
CREATE TABLE IF NOT EXISTS contract (
  cn_id               TEXT PRIMARY KEY,
  ocid                TEXT NOT NULL,
  agency_id           INTEGER NOT NULL REFERENCES agency(id),
  supplier_id         INTEGER REFERENCES supplier(id),
  title               TEXT NOT NULL,
  description         TEXT NOT NULL,
  unspsc              TEXT NOT NULL,
  method              TEXT NOT NULL,       -- open | limited | selective
  method_detail       TEXT NOT NULL,
  limited_reason      TEXT NOT NULL,
  exemption           TEXT NOT NULL,
  start_date          TEXT NOT NULL,
  end_date            TEXT NOT NULL,
  published_date      TEXT NOT NULL,
  value               INTEGER NOT NULL,
  original_value      INTEGER,             -- NULL until amendment history is loaded
  amendment_count     INTEGER NOT NULL DEFAULT 0,
  status              TEXT NOT NULL,
  release_id          TEXT NOT NULL,
  release_date        TEXT NOT NULL,
  history_loaded      INTEGER NOT NULL DEFAULT 0,
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id)
);

CREATE INDEX IF NOT EXISTS contract_agency ON contract(agency_id);
CREATE INDEX IF NOT EXISTS contract_supplier ON contract(supplier_id);
CREATE INDEX IF NOT EXISTS contract_period ON contract(start_date, end_date);

-- Every release seen for a contract, including amendments.
CREATE TABLE IF NOT EXISTS contract_release (
  release_id          TEXT PRIMARY KEY,
  cn_id               TEXT NOT NULL,
  release_date        TEXT NOT NULL,
  amendment           INTEGER NOT NULL,
  value               INTEGER NOT NULL,
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id)
);

CREATE INDEX IF NOT EXISTS contract_release_cn ON contract_release(cn_id);

-- Derived: each contract's value spread evenly by day across financial years.
-- An estimate, rebuilt by cmd/derive; always labelled as such in the UI.
CREATE TABLE IF NOT EXISTS contract_fy (
  cn_id        TEXT NOT NULL,
  fiscal_year  TEXT NOT NULL,
  amount       INTEGER NOT NULL,
  PRIMARY KEY (cn_id, fiscal_year)
);

CREATE INDEX IF NOT EXISTS contract_fy_year ON contract_fy(fiscal_year);

-- What each program pays for, quoted from its PBS document (kind = reported),
-- or a labelled AI summary written from that quote (kind = ai_summary).
CREATE TABLE IF NOT EXISTS program_description (
  program_id          INTEGER NOT NULL REFERENCES program(id),
  kind                TEXT NOT NULL CHECK (kind IN ('reported', 'ai_summary')),
  text                TEXT NOT NULL,
  source_url          TEXT NOT NULL,
  page                INTEGER,
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id),
  model               TEXT NOT NULL DEFAULT '',
  prompt_version      TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (program_id, kind)
);

-- Australian Business Register details for supplier ABNs, from the ABN Bulk
-- Extract on data.gov.au. Only ABNs that appear in AusTender are kept, and no
-- names: an individual's name stays as AusTender published it.
CREATE TABLE IF NOT EXISTS abr_entity (
  abn                 TEXT PRIMARY KEY,
  entity_type         TEXT NOT NULL,   -- ABR code: IND = individual or sole trader, PRV = private company, ...
  entity_type_text    TEXT NOT NULL,
  status              TEXT NOT NULL,   -- ACT (active) | CAN (cancelled)
  status_from         TEXT NOT NULL,   -- YYYY-MM-DD
  record_updated      TEXT NOT NULL,   -- YYYY-MM-DD
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id)
);

-- Given names common among individuals registered in the ABR, with how many
-- registrations use them. Used to recognise people's names among suppliers
-- with no ABR record. No other part of an individual's record is kept.
CREATE TABLE IF NOT EXISTS given_name (
  name           TEXT PRIMARY KEY,   -- upper case
  registrations  INTEGER NOT NULL
);

-- Derived: suppliers who are, or may be, individuals. They keep their names on
-- contract and supplier pages but are left out of rankings. Rebuilt by
-- cmd/derive. basis = 'abr' (registered as an individual) or 'name' (no ABR
-- record, and the name does not look like an organisation's).
CREATE TABLE IF NOT EXISTS supplier_individual (
  supplier_id  INTEGER PRIMARY KEY,
  basis        TEXT NOT NULL CHECK (basis IN ('abr', 'name'))
);

-- Legal Entity Identifier records from GLEIF's golden copy for supplier
-- companies (matched by ACN or ABN), and for the ultimate parents they report.
-- Only these are kept. abn is the supplier ABN a record matched, at most one
-- record per ABN; it is empty for records kept only as parents.
CREATE TABLE IF NOT EXISTS gleif_entity (
  lei                 TEXT PRIMARY KEY,
  legal_name          TEXT NOT NULL,
  country             TEXT NOT NULL,
  abn                 TEXT NOT NULL DEFAULT '',
  status              TEXT NOT NULL,              -- ACTIVE | INACTIVE
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id)
);
CREATE INDEX IF NOT EXISTS gleif_entity_abn ON gleif_entity(abn) WHERE abn <> '';

-- Each kept entity's ultimate accounting consolidating parent, as reported to
-- GLEIF (relationship IS_ULTIMATELY_CONSOLIDATED_BY, status active).
CREATE TABLE IF NOT EXISTS gleif_parent (
  lei                 TEXT PRIMARY KEY,
  parent_lei          TEXT NOT NULL,
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id)
);

-- LLM judgements of which suppliers sharing a leading name word belong to one
-- corporate group. One row per supplier sent; group_label is unique within
-- the cluster, and empty when the supplier stands alone. Kept per prompt
-- version, like llm_classification.
CREATE TABLE IF NOT EXISTS llm_supplier_group (
  supplier_id     INTEGER NOT NULL,
  prompt_version  TEXT NOT NULL,
  cluster_key     TEXT NOT NULL,
  input_hash      TEXT NOT NULL,   -- fingerprint of the whole cluster sent
  group_label     TEXT NOT NULL,
  group_name      TEXT NOT NULL,
  confidence      TEXT NOT NULL,
  model           TEXT NOT NULL,
  batch_id        TEXT NOT NULL,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (supplier_id, prompt_version)
);
CREATE INDEX IF NOT EXISTS llm_supplier_group_cluster ON llm_supplier_group(cluster_key, prompt_version);

-- The suppliers in each request of a supplier-grouping batch.
CREATE TABLE IF NOT EXISTS llm_group_batch_item (
  batch_id     TEXT NOT NULL,
  custom_id    TEXT NOT NULL,
  cluster_key  TEXT NOT NULL,
  input_hash   TEXT NOT NULL,
  supplier_id  INTEGER NOT NULL,
  PRIMARY KEY (batch_id, supplier_id)
);

-- Derived: corporate groups of two or more suppliers, joined by a GLEIF parent
-- or by the LLM's grouping. id is the lowest member supplier id, so a group's
-- URL survives rebuilds. Individuals are never members. Rebuilt by cmd/derive.
CREATE TABLE IF NOT EXISTS supplier_group (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  name_basis  TEXT NOT NULL CHECK (name_basis IN ('gleif', 'ai')),
  parent_lei  TEXT NOT NULL DEFAULT ''
);

-- basis: 'gleif' when the supplier reports the group's parent (or is it),
-- 'ai' when only the LLM's grouping joins it.
CREATE TABLE IF NOT EXISTS supplier_group_member (
  supplier_id  INTEGER PRIMARY KEY,
  group_id     INTEGER NOT NULL,
  basis        TEXT NOT NULL CHECK (basis IN ('gleif', 'ai'))
);
CREATE INDEX IF NOT EXISTS supplier_group_member_group ON supplier_group_member(group_id);

-- Agencies that are corporate Commonwealth entities, from Finance's List of
-- Commonwealth Entities and Companies (reviewed in data/corporate_agencies.csv).
-- Rebuilt by cmd/derive.
CREATE TABLE IF NOT EXISTS corporate_agency (
  agency_id  INTEGER PRIMARY KEY REFERENCES agency(id)
);

-- Contracts with the financial year they were published in (July to June), and
-- the open-tender threshold that applied (CPR paragraph 9.7):
--   construction services: $7.5 million. AusTender has no construction flag, so
--     UNSPSC segment 72 or the agency's own "below the construction threshold"
--     reason stands in for it;
--   corporate entities: $400,000;
--   non-corporate entities: $80,000, raised to $125,000 for approaches to market
--     from 17 November 2025. AusTender gives no approach-to-market date, so
--     publication date stands in for it.
-- Dropped and recreated on every open so changes reach existing databases.
DROP VIEW IF EXISTS contract_pub;
CREATE VIEW contract_pub AS
SELECT contract.*,
  CASE WHEN CAST(substr(published_date, 6, 2) AS INTEGER) >= 7
       THEN substr(published_date, 1, 4) || '-' || printf('%02d', (CAST(substr(published_date, 3, 2) AS INTEGER) + 1) % 100)
       ELSE (CAST(substr(published_date, 1, 4) AS INTEGER) - 1) || '-' || substr(published_date, 3, 2)
  END AS published_fy,
  CASE WHEN unspsc LIKE '72%' OR limited_reason = 'Valued below the construction threshold.' THEN 7500000
       WHEN agency_id IN (SELECT agency_id FROM corporate_agency) THEN 400000
       WHEN published_date >= '2025-11-17' THEN 125000
       ELSE 80000
  END AS tender_threshold
FROM contract;

-- Derived: "worth a closer look" signals, one row per contract that matches a
-- rule in internal/signals. Rebuilt by cmd/derive.
CREATE TABLE IF NOT EXISTS signal (
  rule_id      TEXT NOT NULL,
  cn_id        TEXT NOT NULL,
  agency_id    INTEGER NOT NULL,
  supplier_id  INTEGER,
  fiscal_year  TEXT NOT NULL,      -- financial year the contract was published
  amount       INTEGER NOT NULL,   -- dollars the pattern involves
  measure      REAL NOT NULL,      -- rule-specific: per cent, days or count
  PRIMARY KEY (rule_id, cn_id)
);

CREATE INDEX IF NOT EXISTS signal_agency ON signal(agency_id, fiscal_year);

-- LLM classifications of contracts, kept per task and prompt version so a new
-- prompt never overwrites results made under an older one.
CREATE TABLE IF NOT EXISTS llm_classification (
  cn_id           TEXT NOT NULL,
  task            TEXT NOT NULL,
  prompt_version  TEXT NOT NULL,
  model           TEXT NOT NULL,
  category        TEXT NOT NULL,
  confidence      TEXT NOT NULL,
  input_hash      TEXT NOT NULL,
  batch_id        TEXT NOT NULL,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (cn_id, task, prompt_version)
);

-- Batches submitted to the Message Batches API, and the contracts in each
-- request, so results can be matched when they arrive.
CREATE TABLE IF NOT EXISTS llm_batch (
  batch_id        TEXT PRIMARY KEY,
  task            TEXT NOT NULL,
  prompt_version  TEXT NOT NULL,
  model           TEXT NOT NULL,
  requests        INTEGER NOT NULL,
  status          TEXT NOT NULL,        -- submitted | collected
  submitted_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS llm_batch_item (
  batch_id    TEXT NOT NULL,
  custom_id   TEXT NOT NULL,
  cn_id       TEXT NOT NULL,
  input_hash  TEXT NOT NULL,
  PRIMARY KEY (batch_id, custom_id, cn_id)
);
-- ContractsToClassify looks items up by contract.
CREATE INDEX IF NOT EXISTS llm_batch_item_cn ON llm_batch_item(cn_id);

-- Transparency Portal ---------------------------------------------------------

-- Contract spending each non-corporate entity reported in its annual report,
-- from the Transparency Portal's Data Explorer. Cash paid in the reporting
-- year, GST inclusive, whole dollars. check_note is empty when the figure
-- passes the checks in internal/transparency, otherwise the reason it looks
-- wrong. These figures are shown on entity pages only and never summed.
CREATE TABLE IF NOT EXISTS reported_spend (
  entity_name         TEXT NOT NULL,            -- as published
  entity_id           INTEGER REFERENCES entity(id),
  reporting_year      TEXT NOT NULL,
  kind                TEXT NOT NULL CHECK (kind IN ('consultancy', 'non_consultancy')),
  new_contracts       INTEGER NOT NULL,
  new_amount          INTEGER NOT NULL,
  ongoing_contracts   INTEGER NOT NULL,
  ongoing_amount      INTEGER NOT NULL,
  contracts           INTEGER NOT NULL,
  amount              INTEGER NOT NULL,
  check_note          TEXT NOT NULL DEFAULT '',
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id),
  PRIMARY KEY (entity_name, reporting_year, kind)
);

-- Organisations each entity reported as receiving the largest shares (usually
-- five) of that spending. individual = 1 when the organisation is, or may be,
-- a person; such rows are not listed.
CREATE TABLE IF NOT EXISTS reported_org (
  entity_name         TEXT NOT NULL,
  entity_id           INTEGER REFERENCES entity(id),
  reporting_year      TEXT NOT NULL,
  kind                TEXT NOT NULL CHECK (kind IN ('consultancy', 'non_consultancy')),
  org_name            TEXT NOT NULL,
  org_abn             TEXT NOT NULL,
  amount              INTEGER NOT NULL,
  individual          INTEGER NOT NULL DEFAULT 0,
  check_note          TEXT NOT NULL DEFAULT '',
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id)
);
CREATE INDEX IF NOT EXISTS reported_org_entity ON reported_org(entity_id, reporting_year);

-- Loans and investments -----------------------------------------------------

-- Net cash flows from investments in financial assets for policy purposes:
-- loans and equity the government provides, which are not expenses. From the
-- table reconciling the underlying to the headline cash balance in Budget
-- Paper No. 1 and the Final Budget Outcome. Amounts are as published:
-- negative is net cash paid out. The row with is_total = 1 holds the
-- published total.
CREATE TABLE IF NOT EXISTS policy_investment (
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id),
  item                TEXT NOT NULL,      -- as published
  item_key            TEXT NOT NULL,      -- normalised, to match items across publications
  fiscal_year         TEXT NOT NULL,
  estimate_type       TEXT NOT NULL,
  amount              INTEGER NOT NULL,   -- whole dollars
  is_total            INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (source_document_id, item_key, fiscal_year)
);

-- GrantConnect -----------------------------------------------------------------

-- Grant awards from GrantConnect exports, one row per award (its latest
-- version). value is the award's total over its whole term, in whole dollars,
-- as published. Rebuilt in full by cmd/load-grants.
CREATE TABLE IF NOT EXISTS grant_award (
  ga_id               TEXT PRIMARY KEY,     -- without the version, e.g. GA270901
  version_id          TEXT NOT NULL,        -- as published, e.g. GA270901-V1
  activity            TEXT NOT NULL,
  agency_name         TEXT NOT NULL,
  entity_id           INTEGER REFERENCES entity(id),
  publish_date        TEXT NOT NULL,
  category            TEXT NOT NULL,
  start_date          TEXT NOT NULL,
  end_date            TEXT NOT NULL,
  value               INTEGER NOT NULL,
  go_id               TEXT NOT NULL,        -- grant opportunity; '' for ad hoc grants
  recipient           TEXT NOT NULL,
  individual          INTEGER NOT NULL DEFAULT 0,  -- recipient's name reads as a person's
  unnamed             INTEGER NOT NULL DEFAULT 0,  -- aggregated ("Multiple") or withheld ("n/a") recipient
  one_off             INTEGER NOT NULL,
  aggregate           INTEGER NOT NULL,
  source_document_id  INTEGER NOT NULL REFERENCES source_document(id)
);
CREATE INDEX IF NOT EXISTS grant_award_entity ON grant_award(entity_id);

-- Each award's value spread evenly over its term by financial year (an
-- estimate, like contract_fy). Rebuilt by cmd/load-grants, not by derive.
CREATE TABLE IF NOT EXISTS grant_fy (
  ga_id        TEXT NOT NULL REFERENCES grant_award(ga_id),
  fiscal_year  TEXT NOT NULL,
  amount       INTEGER NOT NULL,
  PRIMARY KEY (ga_id, fiscal_year)
);
CREATE INDEX IF NOT EXISTS grant_fy_year ON grant_fy(fiscal_year);
