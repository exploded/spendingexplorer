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

-- Contracts with the financial year they were published in (July to June).
CREATE VIEW IF NOT EXISTS contract_pub AS
SELECT contract.*,
  CASE WHEN CAST(substr(published_date, 6, 2) AS INTEGER) >= 7
       THEN substr(published_date, 1, 4) || '-' || printf('%02d', (CAST(substr(published_date, 3, 2) AS INTEGER) + 1) % 100)
       ELSE (CAST(substr(published_date, 1, 4) AS INTEGER) - 1) || '-' || substr(published_date, 3, 2)
  END AS published_fy
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
