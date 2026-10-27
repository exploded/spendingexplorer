-- name: UpsertSourceDocument :one
INSERT INTO source_document (title, publisher, url, file_name, sha256, doc_type, budget_year)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (sha256) DO UPDATE SET
  title = excluded.title, url = excluded.url, file_name = excluded.file_name
RETURNING id;

-- name: DeleteProgramExpensesForDoc :exec
DELETE FROM program_expense WHERE source_document_id = ?;

-- name: DeleteWogExpensesForDoc :exec
DELETE FROM wog_expense WHERE source_document_id = ?;

-- name: UpsertEntity :one
INSERT INTO entity (name) VALUES (?)
ON CONFLICT (name) DO UPDATE SET name = excluded.name
RETURNING id;

-- name: UpsertProgram :one
INSERT INTO program (entity_id, budget_year, portfolio, outcome, name) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (entity_id, budget_year, outcome, name) DO UPDATE SET portfolio = excluded.portfolio
RETURNING id;

-- name: InsertProgramExpense :exec
INSERT INTO program_expense (
  program_id, source_document_id, source_table, source_row, fiscal_year, estimate_type,
  expense_kind, line_item, amount, not_published, treatment, treatment_rule
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertWogExpense :exec
INSERT INTO wog_expense (source_document_id, fiscal_year, estimate_type, lens, category, amount)
VALUES (?, ?, ?, ?, ?, ?);

-- name: InsertLoadRun :exec
INSERT INTO load_run (command, source_document_id, rows_read, rows_written, notes)
VALUES (?, ?, ?, ?, ?);

-- name: ListPBSDocuments :many
SELECT id, title, budget_year FROM source_document
WHERE doc_type = 'pbs_line_items'
ORDER BY budget_year;

-- name: SumByTreatment :many
SELECT pe.treatment, pe.treatment_rule,
       CAST(COALESCE(SUM(pe.amount), 0) AS INTEGER) AS total,
       COUNT(*) AS lines,
       CAST(SUM(pe.not_published) AS INTEGER) AS not_published
FROM program_expense pe
WHERE pe.source_document_id = ? AND pe.fiscal_year = ?
GROUP BY pe.treatment, pe.treatment_rule
ORDER BY pe.treatment, total DESC;

-- name: SumExpenseByPortfolio :many
SELECT p.portfolio, CAST(COALESCE(SUM(pe.amount), 0) AS INTEGER) AS total
FROM program_expense pe
JOIN program p ON p.id = pe.program_id
WHERE pe.source_document_id = ? AND pe.fiscal_year = ? AND pe.treatment = 'expense'
GROUP BY p.portfolio
ORDER BY total DESC;

-- name: WogTotalForYear :one
-- The published total expenses from the BP1 of the same budget year.
SELECT w.amount, w.estimate_type, sd.title
FROM wog_expense w
JOIN source_document sd ON sd.id = w.source_document_id
WHERE sd.budget_year = ? AND w.fiscal_year = ? AND w.lens = 'function' AND w.category = 'Total expenses';

-- name: LargestTransfers :many
SELECT e.name AS entity, pe.line_item, pe.treatment_rule, pe.amount
FROM program_expense pe
JOIN program p ON p.id = pe.program_id
JOIN entity e ON e.id = p.entity_id
WHERE pe.source_document_id = ? AND pe.fiscal_year = ? AND pe.treatment = 'transfer'
ORDER BY pe.amount DESC
LIMIT ?;

-- Web app (read-only) -------------------------------------------------------

-- name: LatestPBSDocument :one
SELECT id, title, url, budget_year FROM source_document
WHERE doc_type = 'pbs_line_items'
ORDER BY budget_year DESC
LIMIT 1;

-- name: GetSourceDocument :one
SELECT id, title, publisher, url, file_name, sha256, doc_type, budget_year, retrieved_at
FROM source_document WHERE id = ?;

-- name: ListSourceDocuments :many
SELECT id, title, publisher, url, file_name, sha256, doc_type, budget_year, retrieved_at
FROM source_document ORDER BY budget_year DESC, doc_type;

-- name: ListLoadRuns :many
SELECT lr.id, lr.command, lr.rows_read, lr.rows_written, lr.notes, lr.finished_at, sd.title
FROM load_run lr JOIN source_document sd ON sd.id = lr.source_document_id
ORDER BY lr.finished_at DESC, lr.id DESC;

-- name: WogDocument :one
SELECT id, title, url FROM source_document
WHERE doc_type = 'bp1_function' AND budget_year = ?;

-- name: FunctionTotals :many
-- BP1 expenses by function for the current and previous fiscal year.
SELECT category,
  CAST(COALESCE(SUM(CASE WHEN fiscal_year = sqlc.arg(fy) THEN amount END), 0) AS INTEGER) AS cur,
  CAST(COALESCE(SUM(CASE WHEN fiscal_year = sqlc.arg(prev_fy) THEN amount END), 0) AS INTEGER) AS prev
FROM wog_expense
WHERE source_document_id = sqlc.arg(doc_id) AND lens = 'function'
GROUP BY category
ORDER BY cur DESC;

-- name: TreatmentTotals :many
-- Every line in the publication by treatment, for the reconciliation.
SELECT treatment, treatment_rule,
  CAST(COALESCE(SUM(CASE WHEN fiscal_year = sqlc.arg(fy) THEN amount END), 0) AS INTEGER) AS cur,
  CAST(COALESCE(SUM(CASE WHEN fiscal_year = sqlc.arg(prev_fy) THEN amount END), 0) AS INTEGER) AS prev,
  CAST(COUNT(DISTINCT source_row) AS INTEGER) AS lines
FROM program_expense
WHERE source_document_id = sqlc.arg(doc_id)
GROUP BY treatment, treatment_rule
ORDER BY treatment, cur DESC;

-- name: PortfolioTotals :many
SELECT p.portfolio,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) AND pe.treatment = 'expense' THEN pe.amount END), 0) AS INTEGER) AS cur,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(prev_fy) AND pe.treatment = 'expense' THEN pe.amount END), 0) AS INTEGER) AS prev,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) AND pe.treatment IN ('transfer', 'other_economic_flow') THEN pe.amount END), 0) AS INTEGER) AS excluded,
  CAST(COUNT(DISTINCT p.entity_id) AS INTEGER) AS children
FROM program_expense pe
JOIN program p ON p.id = pe.program_id
WHERE pe.source_document_id = sqlc.arg(doc_id)
GROUP BY p.portfolio
ORDER BY cur DESC;

-- name: EntityTotals :many
SELECT e.id, e.name,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) AND pe.treatment = 'expense' THEN pe.amount END), 0) AS INTEGER) AS cur,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(prev_fy) AND pe.treatment = 'expense' THEN pe.amount END), 0) AS INTEGER) AS prev,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) AND pe.treatment IN ('transfer', 'other_economic_flow') THEN pe.amount END), 0) AS INTEGER) AS excluded,
  CAST(COUNT(DISTINCT p.id) AS INTEGER) AS children
FROM program_expense pe
JOIN program p ON p.id = pe.program_id
JOIN entity e ON e.id = p.entity_id
WHERE pe.source_document_id = sqlc.arg(doc_id) AND p.portfolio = sqlc.arg(portfolio)
GROUP BY e.id, e.name
ORDER BY cur DESC;

-- name: GetEntityInDoc :one
SELECT e.id, e.name, p.portfolio
FROM entity e
JOIN program p ON p.entity_id = e.id
JOIN program_expense pe ON pe.program_id = p.id
WHERE e.id = sqlc.arg(entity_id) AND pe.source_document_id = sqlc.arg(doc_id)
LIMIT 1;

-- name: ProgramTotals :many
SELECT p.id, p.name, p.outcome,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) AND pe.treatment = 'expense' THEN pe.amount END), 0) AS INTEGER) AS cur,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(prev_fy) AND pe.treatment = 'expense' THEN pe.amount END), 0) AS INTEGER) AS prev,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) AND pe.treatment IN ('transfer', 'other_economic_flow') THEN pe.amount END), 0) AS INTEGER) AS excluded,
  CAST(COUNT(DISTINCT pe.source_row) AS INTEGER) AS children
FROM program_expense pe
JOIN program p ON p.id = pe.program_id
WHERE pe.source_document_id = sqlc.arg(doc_id) AND p.entity_id = sqlc.arg(entity_id)
GROUP BY p.id, p.name, p.outcome
ORDER BY cur DESC;

-- name: KindTotals :many
-- Administered / departmental / corporate split for an entity.
SELECT pe.expense_kind,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) THEN pe.amount END), 0) AS INTEGER) AS cur
FROM program_expense pe
JOIN program p ON p.id = pe.program_id
WHERE pe.source_document_id = sqlc.arg(doc_id) AND p.entity_id = sqlc.arg(entity_id) AND pe.treatment = 'expense'
GROUP BY pe.expense_kind
ORDER BY cur DESC;

-- name: GetProgram :one
SELECT p.id, p.name, p.outcome, p.portfolio, p.budget_year, e.id AS entity_id, e.name AS entity_name
FROM program p JOIN entity e ON e.id = p.entity_id
WHERE p.id = ?;

-- name: ProgramLines :many
-- One row per published line, with the current and previous year side by side.
SELECT pe.source_row, pe.source_table, pe.line_item, pe.expense_kind, pe.treatment, pe.treatment_rule,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) THEN pe.amount END), 0) AS INTEGER) AS cur,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(prev_fy) THEN pe.amount END), 0) AS INTEGER) AS prev,
  CAST(MAX(pe.not_published) AS INTEGER) AS not_published
FROM program_expense pe
WHERE pe.source_document_id = sqlc.arg(doc_id) AND pe.program_id = sqlc.arg(program_id)
GROUP BY pe.source_row, pe.source_table, pe.line_item, pe.expense_kind, pe.treatment, pe.treatment_rule
ORDER BY cur DESC;
