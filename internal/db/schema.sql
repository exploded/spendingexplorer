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
