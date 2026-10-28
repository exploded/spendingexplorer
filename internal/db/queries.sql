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

-- Entity names ------------------------------------------------------------------
-- One entity keeps one row across budget years. data/entity_names.csv maps the
-- names it is published under (renames, misspellings) to that row.

-- name: EntityIDByName :one
SELECT id FROM entity WHERE name = ?;

-- name: MoveProgramsToEntity :exec
UPDATE program SET entity_id = sqlc.arg(to_id) WHERE entity_id = sqlc.arg(from_id);

-- name: MoveAgenciesToEntity :exec
UPDATE agency SET entity_id = sqlc.arg(to_id) WHERE entity_id = sqlc.arg(from_id);

-- name: DeleteEntity :exec
DELETE FROM entity WHERE id = ?;

-- name: UpsertEntityAlias :exec
INSERT INTO entity_alias (alias, entity_id) VALUES (?, ?)
ON CONFLICT (alias) DO UPDATE SET entity_id = excluded.entity_id;

-- name: ListEntityAliases :many
SELECT alias, entity_id FROM entity_alias ORDER BY alias;

-- name: EntityOtherNames :many
-- Names the entity is published under in the PBS or AusTender, including its own.
SELECT ea.alias AS name FROM entity_alias ea WHERE ea.entity_id = sqlc.arg(entity_id)
UNION
SELECT a.name FROM agency a WHERE a.entity_id = sqlc.arg(entity_id)
ORDER BY 1;

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
WHERE sd.doc_type = 'bp1_function' AND sd.budget_year = ? AND w.fiscal_year = ? AND w.lens = 'function' AND w.category = 'Total expenses';

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
-- Documents other than AusTender API pages, which are summarised separately.
SELECT id, title, publisher, url, file_name, sha256, doc_type, budget_year, retrieved_at
FROM source_document WHERE doc_type NOT LIKE 'austender%'
ORDER BY budget_year DESC, doc_type;

-- name: AusTenderSourceSummary :many
SELECT doc_type, CAST(COUNT(*) AS INTEGER) AS documents,
  CAST(MIN(retrieved_at) AS TEXT) AS first_retrieved, CAST(MAX(retrieved_at) AS TEXT) AS last_retrieved
FROM source_document WHERE doc_type LIKE 'austender%'
GROUP BY doc_type ORDER BY doc_type;

-- name: ListLoadRuns :many
SELECT lr.id, lr.command, lr.rows_read, lr.rows_written, lr.notes, lr.finished_at, sd.title
FROM load_run lr JOIN source_document sd ON sd.id = lr.source_document_id
ORDER BY lr.finished_at DESC, lr.id DESC
LIMIT 200;

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

-- AusTender loader ----------------------------------------------------------

-- name: UpsertAgency :one
INSERT INTO agency (name, abn) VALUES (?, ?)
ON CONFLICT (name) DO UPDATE SET abn = CASE WHEN excluded.abn <> '' THEN excluded.abn ELSE agency.abn END
RETURNING id;

-- name: UpsertSupplier :one
INSERT INTO supplier (key, abn, name, country, postcode) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET name = excluded.name, country = excluded.country, postcode = excluded.postcode
RETURNING id;

-- name: UpsertContract :exec
-- Keeps the newest release; an older release never overwrites a newer one.
INSERT INTO contract (
  cn_id, ocid, agency_id, supplier_id, title, description, unspsc, method, method_detail,
  limited_reason, exemption, start_date, end_date, published_date, value, original_value,
  amendment_count, status, release_id, release_date, source_document_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (cn_id) DO UPDATE SET
  agency_id = excluded.agency_id, supplier_id = excluded.supplier_id, title = excluded.title,
  description = excluded.description, unspsc = excluded.unspsc, method = excluded.method,
  method_detail = excluded.method_detail, limited_reason = excluded.limited_reason,
  exemption = excluded.exemption, start_date = excluded.start_date, end_date = excluded.end_date,
  published_date = excluded.published_date, value = excluded.value,
  original_value = COALESCE(contract.original_value, excluded.original_value),
  amendment_count = excluded.amendment_count, status = excluded.status,
  release_id = excluded.release_id, release_date = excluded.release_date,
  history_loaded = CASE WHEN excluded.amendment_count > contract.amendment_count THEN 0 ELSE contract.history_loaded END,
  source_document_id = excluded.source_document_id
WHERE excluded.release_date >= contract.release_date;

-- name: UpsertContractRelease :exec
INSERT INTO contract_release (release_id, cn_id, release_date, amendment, value, source_document_id)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (release_id) DO NOTHING;

-- name: ContractsNeedingHistory :many
SELECT cn_id FROM contract WHERE amendment_count > 0 AND history_loaded = 0 ORDER BY cn_id;

-- name: SetContractHistory :exec
UPDATE contract SET original_value = ?, history_loaded = 1 WHERE cn_id = ?;

-- name: UnmatchedAgencies :many
SELECT id, name FROM agency WHERE entity_id IS NULL ORDER BY name;

-- name: ListEntities :many
SELECT id, name FROM entity ORDER BY name;

-- name: SetAgencyEntity :exec
UPDATE agency SET entity_id = ?, match_method = ? WHERE id = ?;

-- Derivations -----------------------------------------------------------------

-- name: ClearContractFY :exec
DELETE FROM contract_fy;

-- name: ContractsForApportion :many
SELECT cn_id, value, start_date, end_date, published_date FROM contract WHERE value > 0;

-- name: InsertContractFY :exec
INSERT INTO contract_fy (cn_id, fiscal_year, amount) VALUES (?, ?, ?);

-- Contracts in the web app ------------------------------------------------------

-- name: ContractCoverageTotal :one
-- Annualised contract value in a year, for agencies matched to an entity.
SELECT CAST(COALESCE(SUM(cf.amount), 0) AS INTEGER) AS amount,
       CAST(COUNT(*) AS INTEGER) AS contracts
FROM contract_fy cf
JOIN contract c ON c.cn_id = cf.cn_id
JOIN agency a ON a.id = c.agency_id
WHERE cf.fiscal_year = ? AND a.entity_id IS NOT NULL;

-- name: PortfolioContractTotals :many
SELECT p.portfolio, CAST(COALESCE(SUM(cf.amount), 0) AS INTEGER) AS amount
FROM contract_fy cf
JOIN contract c ON c.cn_id = cf.cn_id
JOIN agency a ON a.id = c.agency_id
JOIN (SELECT DISTINCT entity_id, portfolio FROM program WHERE budget_year = sqlc.arg(budget_year)) p ON p.entity_id = a.entity_id
WHERE cf.fiscal_year = sqlc.arg(fy)
GROUP BY p.portfolio;

-- name: EntityContractTotals :many
SELECT a.entity_id, CAST(COALESCE(SUM(cf.amount), 0) AS INTEGER) AS amount
FROM contract_fy cf
JOIN contract c ON c.cn_id = cf.cn_id
JOIN agency a ON a.id = c.agency_id
WHERE cf.fiscal_year = ? AND a.entity_id IS NOT NULL
GROUP BY a.entity_id;

-- name: EntityContractStats :one
-- Contracts active in a year for one entity, with the procurement facts the
-- page reports: limited tenders and value added by amendments.
SELECT CAST(COUNT(*) AS INTEGER) AS contracts,
  CAST(COALESCE(SUM(cf.amount), 0) AS INTEGER) AS amount,
  CAST(COALESCE(SUM(CASE WHEN c.method = 'limited' THEN cf.amount END), 0) AS INTEGER) AS limited_amount,
  CAST(COALESCE(SUM(CASE WHEN c.method = 'limited' THEN 1 END), 0) AS INTEGER) AS limited_contracts,
  CAST(COALESCE(SUM(CASE WHEN c.amendment_count > 0 THEN 1 END), 0) AS INTEGER) AS amended_contracts,
  CAST(COALESCE(SUM(CASE WHEN c.original_value IS NOT NULL AND c.value > c.original_value THEN c.value - c.original_value END), 0) AS INTEGER) AS amended_increase,
  CAST(COUNT(DISTINCT c.supplier_id) AS INTEGER) AS suppliers
FROM contract_fy cf
JOIN contract c ON c.cn_id = cf.cn_id
JOIN agency a ON a.id = c.agency_id
WHERE cf.fiscal_year = sqlc.arg(fy) AND a.entity_id = sqlc.arg(entity_id);

-- name: EntityTopSuppliers :many
-- Rankings leave out suppliers who are, or may be, individuals.
SELECT s.id, s.name, s.abn,
  CAST(SUM(cf.amount) AS INTEGER) AS amount,
  CAST(COUNT(*) AS INTEGER) AS contracts,
  CAST(COALESCE(SUM(CASE WHEN c.method = 'limited' THEN cf.amount END), 0) AS INTEGER) AS limited_amount
FROM contract_fy cf
JOIN contract c ON c.cn_id = cf.cn_id
JOIN agency a ON a.id = c.agency_id
JOIN supplier s ON s.id = c.supplier_id
WHERE cf.fiscal_year = sqlc.arg(fy) AND a.entity_id = sqlc.arg(entity_id)
  AND s.id NOT IN (SELECT supplier_id FROM supplier_individual)
GROUP BY s.id, s.name, s.abn
ORDER BY amount DESC
LIMIT sqlc.arg(lim);

-- name: EntityTopContracts :many
-- Rankings leave out suppliers who are, or may be, individuals.
SELECT c.cn_id, c.description, c.method, c.start_date, c.end_date, c.value, c.original_value,
  c.amendment_count, s.id AS supplier_id, s.name AS supplier, cf.amount
FROM contract_fy cf
JOIN contract c ON c.cn_id = cf.cn_id
JOIN agency a ON a.id = c.agency_id
LEFT JOIN supplier s ON s.id = c.supplier_id
WHERE cf.fiscal_year = sqlc.arg(fy) AND a.entity_id = sqlc.arg(entity_id)
  AND (c.supplier_id IS NULL OR c.supplier_id NOT IN (SELECT supplier_id FROM supplier_individual))
ORDER BY cf.amount DESC
LIMIT sqlc.arg(lim);

-- name: GetSupplier :one
SELECT id, key, abn, name, country, postcode FROM supplier WHERE id = ?;

-- name: SupplierTotals :one
SELECT CAST(COUNT(*) AS INTEGER) AS contracts,
  CAST(COALESCE(SUM(value), 0) AS INTEGER) AS value,
  CAST(COALESCE(SUM(CASE WHEN method = 'limited' THEN value END), 0) AS INTEGER) AS limited_value,
  CAST(COALESCE(SUM(CASE WHEN method = 'limited' THEN 1 END), 0) AS INTEGER) AS limited_contracts,
  CAST(COALESCE(MIN(published_date), '') AS TEXT) AS first_published,
  CAST(COALESCE(MAX(published_date), '') AS TEXT) AS last_published
FROM contract WHERE supplier_id = ?;

-- name: SupplierAgencies :many
SELECT a.name AS agency, a.entity_id,
  CAST(COUNT(*) AS INTEGER) AS contracts,
  CAST(SUM(c.value) AS INTEGER) AS value,
  CAST(COALESCE(SUM(CASE WHEN c.method = 'limited' THEN c.value END), 0) AS INTEGER) AS limited_value
FROM contract c JOIN agency a ON a.id = c.agency_id
WHERE c.supplier_id = ?
GROUP BY a.name, a.entity_id
ORDER BY value DESC;

-- name: SupplierContracts :many
SELECT c.cn_id, c.description, c.method, c.start_date, c.end_date, c.published_date, c.value,
  c.original_value, c.amendment_count, a.name AS agency
FROM contract c JOIN agency a ON a.id = c.agency_id
WHERE c.supplier_id = ?
ORDER BY c.value DESC
LIMIT ?;

-- name: GetContract :one
SELECT c.cn_id, c.ocid, c.title, c.description, c.unspsc, c.method, c.method_detail,
  c.limited_reason, c.exemption, c.start_date, c.end_date, c.published_date, c.value,
  c.original_value, c.amendment_count, c.status, c.history_loaded,
  a.name AS agency, a.entity_id, s.id AS supplier_id, s.name AS supplier, s.abn AS supplier_abn
FROM contract c
JOIN agency a ON a.id = c.agency_id
LEFT JOIN supplier s ON s.id = c.supplier_id
WHERE c.cn_id = ?;

-- name: ContractReleases :many
SELECT release_id, release_date, amendment, value FROM contract_release
WHERE cn_id = ? ORDER BY release_date, amendment;

-- name: ContractFiscalYears :many
SELECT fiscal_year, amount FROM contract_fy WHERE cn_id = ? ORDER BY fiscal_year;

-- name: ContractLoadSummary :one
SELECT CAST(COUNT(*) AS INTEGER) AS contracts,
  CAST(COALESCE(MIN(published_date), '') AS TEXT) AS first_published,
  CAST(COALESCE(MAX(published_date), '') AS TEXT) AS last_published
FROM contract;

-- Program descriptions ---------------------------------------------------------

-- name: FindProgram :one
-- The entity may be given by a name it was published under.
SELECT p.id FROM program p JOIN entity e ON e.id = p.entity_id
WHERE p.budget_year = sqlc.arg(budget_year) AND p.name = sqlc.arg(program)
  AND (e.name = sqlc.arg(entity) OR e.id IN (SELECT entity_id FROM entity_alias WHERE alias = sqlc.arg(entity)));

-- name: UpsertProgramDescription :exec
INSERT INTO program_description (program_id, kind, text, source_url, page, source_document_id)
VALUES (?, 'reported', ?, ?, ?, ?)
ON CONFLICT (program_id, kind) DO UPDATE SET
  text = excluded.text, source_url = excluded.source_url, page = excluded.page,
  source_document_id = excluded.source_document_id;

-- name: GetProgramDescription :one
SELECT text, source_url, page FROM program_description
WHERE program_id = ? AND kind = 'reported';

-- name: EntityProgramDescriptions :many
SELECT pd.program_id, pd.text FROM program_description pd
JOIN program p ON p.id = pd.program_id
WHERE p.entity_id = ? AND p.budget_year = ? AND pd.kind = 'reported';

-- Signals (derived) ----------------------------------------------------------------

-- name: ClearSignals :exec
DELETE FROM signal;

-- name: DeriveValueIncrease :exec
INSERT INTO signal (rule_id, cn_id, agency_id, supplier_id, fiscal_year, amount, measure)
SELECT 'value-increase', cn_id, agency_id, supplier_id, published_fy,
  value - original_value, 100.0 * (value - original_value) / original_value
FROM contract_pub
WHERE original_value > 0 AND value - original_value >= 1000000 AND value >= 1.5 * original_value;

-- name: DeriveLatePublication :exec
-- AusTender's dateSigned equals the publication date, so the start date is the
-- only reference point; the 180-day margin allows for backdated start dates.
INSERT INTO signal (rule_id, cn_id, agency_id, supplier_id, fiscal_year, amount, measure)
SELECT 'late-publication', cn_id, agency_id, supplier_id, published_fy,
  value, julianday(published_date) - julianday(start_date)
FROM contract_pub
WHERE start_date <> '' AND published_date <> '' AND value >= 100000
  AND julianday(published_date) - julianday(start_date) > 180;

-- name: DeriveNewABN :exec
-- The ABN Bulk Extract gives only the date the current status took effect, so
-- an ABN reactivated after cancellation looks new; status dates after award are
-- left out for that reason. Award is the earlier of the start and publication
-- dates. Government bodies (ABR types CG*, SG*, LG*, TG*) are not counted.
-- Measure is the number of days the ABN had been active.
INSERT INTO signal (rule_id, cn_id, agency_id, supplier_id, fiscal_year, amount, measure)
SELECT 'new-abn', c.cn_id, c.agency_id, c.supplier_id, c.published_fy, c.value,
  julianday(c.award_date) - julianday(r.status_from)
FROM (SELECT *, CASE WHEN start_date <> '' AND start_date < published_date THEN start_date ELSE published_date END AS award_date
      FROM contract_pub) c
JOIN supplier s ON s.id = c.supplier_id
JOIN abr_entity r ON r.abn = s.abn
WHERE r.status = 'ACT' AND r.entity_type NOT GLOB '[CSLT]G?' AND c.value >= 100000
  AND julianday(c.award_date) - julianday(r.status_from) BETWEEN 0 AND 89;

-- name: DeriveThresholdCluster :exec
-- Measure is the number of near-threshold contracts in the agency-supplier-year group.
-- Only limited tenders count: an open or panel procurement can't be avoiding
-- open tender, and AusTender reports panel call-offs with the panel's method.
INSERT INTO signal (rule_id, cn_id, agency_id, supplier_id, fiscal_year, amount, measure)
SELECT 'threshold-cluster', c.cn_id, c.agency_id, c.supplier_id, c.published_fy, c.value, g.n
FROM contract_pub c
JOIN (SELECT agency_id, supplier_id, published_fy, tender_threshold, COUNT(*) AS n
      FROM contract_pub
      WHERE method = 'limited' AND exemption = ''
        AND value >= 0.9 * tender_threshold AND value < tender_threshold AND supplier_id IS NOT NULL
      GROUP BY agency_id, supplier_id, published_fy, tender_threshold
      HAVING COUNT(*) >= 3) g
  ON g.agency_id = c.agency_id AND g.supplier_id = c.supplier_id AND g.published_fy = c.published_fy
  AND g.tender_threshold = c.tender_threshold
WHERE c.method = 'limited' AND c.exemption = ''
  AND c.value >= 0.9 * c.tender_threshold AND c.value < c.tender_threshold;

-- name: DeriveRepeatLimited :exec
-- Measure is the number of limited tenders in the agency-supplier-year group.
-- Below the threshold, limited tender is routine and needs no stated reason, so
-- only contracts at or above it count.
INSERT INTO signal (rule_id, cn_id, agency_id, supplier_id, fiscal_year, amount, measure)
SELECT 'repeat-limited', c.cn_id, c.agency_id, c.supplier_id, c.published_fy, c.value, g.n
FROM contract_pub c
JOIN (SELECT agency_id, supplier_id, published_fy, COUNT(*) AS n
      FROM contract_pub
      WHERE method = 'limited' AND exemption = '' AND value >= tender_threshold AND supplier_id IS NOT NULL
      GROUP BY agency_id, supplier_id, published_fy
      HAVING COUNT(*) >= 5 AND SUM(value) >= 1000000) g
  ON g.agency_id = c.agency_id AND g.supplier_id = c.supplier_id AND g.published_fy = c.published_fy
WHERE c.method = 'limited' AND c.exemption = '' AND c.value >= c.tender_threshold;

-- name: DeriveCrossedThreshold :exec
-- Measure is the first published value. The 25% and 50% margins leave out
-- small overruns just past the threshold, which are usually minor variations.
INSERT INTO signal (rule_id, cn_id, agency_id, supplier_id, fiscal_year, amount, measure)
SELECT 'crossed-threshold', cn_id, agency_id, supplier_id, published_fy,
  value - original_value, original_value
FROM contract_pub
WHERE method = 'limited' AND exemption = '' AND original_value > 0
  AND original_value < tender_threshold
  AND value >= 1.25 * tender_threshold AND value >= 1.5 * original_value;

-- name: DeriveLateBatch :exec
-- Late-publication matches published in a batch (ten or more from one agency on
-- one day) are counted once per batch under late-batch. Measure is the batch size.
-- Run after DeriveLatePublication, then RemoveBatchedLate.
INSERT INTO signal (rule_id, cn_id, agency_id, supplier_id, fiscal_year, amount, measure)
SELECT 'late-batch', s.cn_id, s.agency_id, s.supplier_id, s.fiscal_year, s.amount, b.n
FROM signal s
JOIN contract c ON c.cn_id = s.cn_id
JOIN (SELECT s2.agency_id, c2.published_date, COUNT(*) AS n
      FROM signal s2 JOIN contract c2 ON c2.cn_id = s2.cn_id
      WHERE s2.rule_id = 'late-publication'
      GROUP BY s2.agency_id, c2.published_date
      HAVING COUNT(*) >= 10) b
  ON b.agency_id = s.agency_id AND b.published_date = c.published_date
WHERE s.rule_id = 'late-publication';

-- name: RemoveBatchedLate :exec
DELETE FROM signal WHERE rule_id = 'late-publication'
  AND cn_id IN (SELECT cn_id FROM signal WHERE rule_id = 'late-batch');

-- name: ClearCorporateAgencies :exec
DELETE FROM corporate_agency;

-- name: InsertCorporateAgency :execrows
INSERT INTO corporate_agency (agency_id) SELECT id FROM agency WHERE name = ?;

-- ABR and individuals -------------------------------------------------------------

-- name: SupplierABNs :many
SELECT DISTINCT abn FROM supplier WHERE abn <> '';

-- name: UpsertABREntity :exec
INSERT INTO abr_entity (abn, entity_type, entity_type_text, status, status_from, record_updated, source_document_id)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(abn) DO UPDATE SET
  entity_type = excluded.entity_type, entity_type_text = excluded.entity_type_text,
  status = excluded.status, status_from = excluded.status_from,
  record_updated = excluded.record_updated, source_document_id = excluded.source_document_id;

-- name: ClearGivenNames :exec
DELETE FROM given_name;

-- name: InsertGivenName :exec
INSERT INTO given_name (name, registrations) VALUES (?, ?);

-- name: ListGivenNames :many
SELECT name FROM given_name;

-- name: ClearSupplierIndividuals :exec
DELETE FROM supplier_individual;

-- name: DeriveABRIndividuals :exec
INSERT INTO supplier_individual (supplier_id, basis)
SELECT s.id, 'abr' FROM supplier s JOIN abr_entity a ON a.abn = s.abn
WHERE a.entity_type = 'IND';

-- name: SuppliersWithoutABR :many
-- Suppliers with no ABR record, whose names the name test checks.
SELECT s.id, s.name FROM supplier s
WHERE NOT EXISTS (SELECT 1 FROM abr_entity a WHERE a.abn = s.abn);

-- name: InsertSupplierIndividual :exec
INSERT OR IGNORE INTO supplier_individual (supplier_id, basis) VALUES (?, ?);

-- name: SupplierIndividualCounts :many
SELECT basis, CAST(COUNT(*) AS INTEGER) AS suppliers FROM supplier_individual GROUP BY basis;

-- name: SignalCounts :many
SELECT rule_id, CAST(COUNT(*) AS INTEGER) AS contracts, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS amount
FROM signal GROUP BY rule_id;

-- Signals in the web app ----------------------------------------------------------

-- name: EntitySignalSummary :many
SELECT s.rule_id, CAST(COUNT(*) AS INTEGER) AS contracts, CAST(COALESCE(SUM(s.amount), 0) AS INTEGER) AS amount
FROM signal s JOIN agency a ON a.id = s.agency_id
WHERE a.entity_id = sqlc.arg(entity_id) AND s.fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
GROUP BY s.rule_id;

-- name: ContractSignals :many
SELECT rule_id, fiscal_year, amount, measure FROM signal WHERE cn_id = ? ORDER BY rule_id;

-- name: SupplierSignalSummary :many
SELECT rule_id, CAST(COUNT(*) AS INTEGER) AS contracts, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS amount
FROM signal WHERE supplier_id = ? GROUP BY rule_id;

-- name: RuleTotals :many
SELECT rule_id, CAST(COUNT(*) AS INTEGER) AS contracts, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS amount,
  CAST(COUNT(DISTINCT agency_id) AS INTEGER) AS agencies
FROM signal WHERE fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
GROUP BY rule_id;

-- name: RuleTopAgencies :many
SELECT a.name AS agency, a.entity_id, CAST(COUNT(*) AS INTEGER) AS contracts, CAST(SUM(s.amount) AS INTEGER) AS amount
FROM signal s JOIN agency a ON a.id = s.agency_id
WHERE s.rule_id = sqlc.arg(rule_id) AND s.fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
GROUP BY a.name, a.entity_id
ORDER BY amount DESC
LIMIT 15;

-- name: RuleContracts :many
-- Contracts matching a rule, optionally for one entity (entity_id 0 = all).
-- Suppliers who are, or may be, individuals are counted in totals, not listed.
SELECT s.cn_id, s.amount, s.measure, s.fiscal_year, c.description, c.value, c.original_value,
  c.published_date, c.start_date, a.name AS agency, a.entity_id
FROM signal s
JOIN contract c ON c.cn_id = s.cn_id
JOIN agency a ON a.id = s.agency_id
WHERE s.rule_id = sqlc.arg(rule_id) AND s.fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
  AND (sqlc.arg(entity_id) = 0 OR a.entity_id = sqlc.arg(entity_id))
  AND (s.supplier_id IS NULL OR s.supplier_id NOT IN (SELECT supplier_id FROM supplier_individual))
ORDER BY s.amount DESC
LIMIT 100;

-- name: RuleExamples :many
-- The three largest contracts matching each rule, as examples. Grouped rules
-- take at most one contract per group, so one batch or supplier cannot fill
-- the list. Suppliers who are, or may be, individuals are not listed.
SELECT rule_id, cn_id, amount, measure, description, agency, supplier FROM (
  SELECT rule_id, cn_id, amount, measure, description, agency, supplier,
    ROW_NUMBER() OVER (PARTITION BY rule_id ORDER BY amount DESC, cn_id) AS rule_rank
  FROM (
    SELECT s.rule_id, s.cn_id, s.amount, s.measure, c.description, a.name AS agency,
      CAST(COALESCE(sp.name, '') AS TEXT) AS supplier,
      ROW_NUMBER() OVER (PARTITION BY s.rule_id, CASE
          WHEN s.rule_id IN ('repeat-limited', 'threshold-cluster') THEN s.agency_id || '|' || s.supplier_id || '|' || s.fiscal_year
          WHEN s.rule_id = 'late-batch' THEN s.agency_id || '|' || c.published_date
          ELSE s.cn_id END
        ORDER BY s.amount DESC, s.cn_id) AS group_rank
    FROM signal s
    JOIN contract c ON c.cn_id = s.cn_id
    JOIN agency a ON a.id = s.agency_id
    LEFT JOIN supplier sp ON sp.id = s.supplier_id
    WHERE s.fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
      AND (s.supplier_id IS NULL OR s.supplier_id NOT IN (SELECT supplier_id FROM supplier_individual))
  ) WHERE group_rank = 1
) WHERE rule_rank <= 3
ORDER BY rule_id, rule_rank;

-- name: RuleGroupCounts :many
-- Groups per grouped rule: agency, supplier and year for repeat-limited and
-- threshold-cluster; agency and publication day for late-batch.
SELECT rule_id, CAST(COUNT(*) AS INTEGER) AS group_count FROM (
  SELECT s.rule_id FROM signal s
  WHERE s.rule_id IN ('repeat-limited', 'threshold-cluster') AND s.fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
  GROUP BY s.rule_id, s.agency_id, s.supplier_id, s.fiscal_year
  UNION ALL
  SELECT s.rule_id FROM signal s JOIN contract c ON c.cn_id = s.cn_id
  WHERE s.rule_id = 'late-batch' AND s.fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
  GROUP BY s.rule_id, s.agency_id, c.published_date
) GROUP BY rule_id;

-- name: RuleSupplierGroups :many
-- Agency-supplier-year groups for a rule, optionally for one entity, with the
-- limited-tender reason given most often in the group. Suppliers who are, or
-- may be, individuals are counted in totals, not listed.
SELECT a.name AS agency, a.entity_id, sp.id AS supplier_id, sp.name AS supplier, s.fiscal_year,
  CAST(COUNT(*) AS INTEGER) AS contracts, CAST(SUM(s.amount) AS INTEGER) AS amount,
  CAST(COUNT(DISTINCT c.limited_reason) AS INTEGER) AS reasons,
  CAST(COALESCE((SELECT c2.limited_reason FROM signal s2 JOIN contract c2 ON c2.cn_id = s2.cn_id
    WHERE s2.rule_id = s.rule_id AND s2.agency_id = s.agency_id AND s2.supplier_id = s.supplier_id
      AND s2.fiscal_year = s.fiscal_year
    GROUP BY c2.limited_reason ORDER BY COUNT(*) DESC LIMIT 1), '') AS TEXT) AS top_reason
FROM signal s
JOIN contract c ON c.cn_id = s.cn_id
JOIN agency a ON a.id = s.agency_id
JOIN supplier sp ON sp.id = s.supplier_id
WHERE s.rule_id = sqlc.arg(rule_id) AND s.fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
  AND (sqlc.arg(entity_id) = 0 OR a.entity_id = sqlc.arg(entity_id))
  AND s.supplier_id NOT IN (SELECT supplier_id FROM supplier_individual)
GROUP BY s.rule_id, s.agency_id, s.supplier_id, s.fiscal_year, a.name, a.entity_id, sp.id, sp.name
ORDER BY contracts DESC, amount DESC
LIMIT 100;

-- name: RuleBatches :many
-- Late-publication batches: one row per agency and publication day.
SELECT a.name AS agency, a.entity_id, c.published_date,
  CAST(COUNT(*) AS INTEGER) AS notices, CAST(SUM(s.amount) AS INTEGER) AS amount,
  CAST(MIN(c.start_date) AS TEXT) AS earliest_start
FROM signal s
JOIN contract c ON c.cn_id = s.cn_id
JOIN agency a ON a.id = s.agency_id
WHERE s.rule_id = 'late-batch' AND s.fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
  AND (sqlc.arg(entity_id) = 0 OR a.entity_id = sqlc.arg(entity_id))
GROUP BY a.name, a.entity_id, c.published_date
ORDER BY notices DESC, amount DESC
LIMIT 100;

-- name: RuleReasons :many
-- The limited-tender reasons agencies gave for a rule's contracts.
SELECT c.limited_reason AS reason, CAST(COUNT(*) AS INTEGER) AS contracts, CAST(SUM(s.amount) AS INTEGER) AS amount
FROM signal s
JOIN contract c ON c.cn_id = s.cn_id
JOIN agency a ON a.id = s.agency_id
WHERE s.rule_id = sqlc.arg(rule_id) AND s.fiscal_year IN (sqlc.arg(fy), sqlc.arg(prev_fy))
  AND (sqlc.arg(entity_id) = 0 OR a.entity_id = sqlc.arg(entity_id))
GROUP BY c.limited_reason
ORDER BY contracts DESC;

-- LLM classification ------------------------------------------------------------

-- name: ContractsToClassify :many
-- Contracts in a UNSPSC segment with no classification for this task and
-- prompt version, and none waiting in an uncollected batch.
SELECT c.cn_id, c.description, a.name AS agency, COALESCE(s.name, '') AS supplier
FROM contract c
JOIN agency a ON a.id = c.agency_id
LEFT JOIN supplier s ON s.id = c.supplier_id
WHERE c.unspsc LIKE sqlc.arg(unspsc_prefix) || '%'
  AND NOT EXISTS (SELECT 1 FROM llm_classification l
                  WHERE l.cn_id = c.cn_id AND l.task = sqlc.arg(task) AND l.prompt_version = sqlc.arg(prompt_version))
  AND NOT EXISTS (SELECT 1 FROM llm_batch_item i JOIN llm_batch b ON b.batch_id = i.batch_id
                  WHERE i.cn_id = c.cn_id AND b.status = 'submitted' AND b.task = sqlc.arg(task))
ORDER BY c.value DESC
LIMIT sqlc.arg(lim);

-- name: InsertLLMBatch :exec
INSERT INTO llm_batch (batch_id, task, prompt_version, model, requests, status)
VALUES (?, ?, ?, ?, ?, 'submitted');

-- name: InsertLLMBatchItem :exec
INSERT INTO llm_batch_item (batch_id, custom_id, cn_id, input_hash) VALUES (?, ?, ?, ?);

-- name: OpenLLMBatches :many
SELECT batch_id, task, prompt_version, model FROM llm_batch WHERE status = 'submitted' AND task = ? ORDER BY submitted_at;

-- name: LLMBatchItems :many
SELECT custom_id, i.cn_id, i.input_hash, c.description, a.name AS agency, COALESCE(s.name, '') AS supplier
FROM llm_batch_item i
JOIN contract c ON c.cn_id = i.cn_id
JOIN agency a ON a.id = c.agency_id
LEFT JOIN supplier s ON s.id = c.supplier_id
WHERE i.batch_id = ?;

-- name: UpsertLLMClassification :exec
INSERT INTO llm_classification (cn_id, task, prompt_version, model, category, confidence, input_hash, batch_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (cn_id, task, prompt_version) DO UPDATE SET
  model = excluded.model, category = excluded.category, confidence = excluded.confidence,
  input_hash = excluded.input_hash, batch_id = excluded.batch_id, created_at = CURRENT_TIMESTAMP;

-- name: MarkLLMBatchCollected :exec
UPDATE llm_batch SET status = 'collected' WHERE batch_id = ?;

-- name: ContractClassification :one
SELECT category, confidence, model, prompt_version, created_at FROM llm_classification
WHERE cn_id = ? AND task = ? ORDER BY prompt_version DESC LIMIT 1;

-- GLEIF and corporate groups ------------------------------------------------------

-- name: ClearGLEIFEntities :exec
DELETE FROM gleif_entity;

-- name: ClearGLEIFParents :exec
DELETE FROM gleif_parent;

-- name: InsertGLEIFEntity :exec
INSERT INTO gleif_entity (lei, legal_name, country, abn, status, source_document_id) VALUES (?, ?, ?, ?, ?, ?);

-- name: InsertGLEIFParent :exec
INSERT INTO gleif_parent (lei, parent_lei, source_document_id) VALUES (?, ?, ?);

-- name: SuppliersForGrouping :many
-- Suppliers with contracts who are not individuals, with what the model is
-- told about each.
SELECT s.id, s.name, s.country,
  CAST(COALESCE(a.entity_type_text, '') AS TEXT) AS entity_type,
  CAST(COALESCE(pe.legal_name, '') AS TEXT) AS gleif_parent
FROM supplier s
LEFT JOIN abr_entity a ON a.abn = s.abn
LEFT JOIN gleif_entity ge ON ge.abn = s.abn AND s.abn <> ''
LEFT JOIN gleif_parent gp ON gp.lei = ge.lei
LEFT JOIN gleif_entity pe ON pe.lei = gp.parent_lei
WHERE s.id NOT IN (SELECT supplier_id FROM supplier_individual)
  AND EXISTS (SELECT 1 FROM contract c WHERE c.supplier_id = s.id);

-- name: GroupedClusterHashes :many
-- Clusters already answered under a prompt version, with the input they had.
SELECT DISTINCT cluster_key, input_hash FROM llm_supplier_group WHERE prompt_version = ?;

-- name: PendingGroupClusters :many
-- Clusters waiting in a batch under a prompt version that has not been collected.
SELECT DISTINCT i.cluster_key FROM llm_group_batch_item i
JOIN llm_batch b ON b.batch_id = i.batch_id
WHERE b.status = 'submitted' AND b.prompt_version = ?;

-- name: InsertLLMGroupBatchItem :exec
INSERT INTO llm_group_batch_item (batch_id, custom_id, cluster_key, input_hash, supplier_id) VALUES (?, ?, ?, ?, ?);

-- name: LLMGroupBatchItems :many
SELECT i.custom_id, i.cluster_key, i.input_hash, i.supplier_id, s.name
FROM llm_group_batch_item i JOIN supplier s ON s.id = i.supplier_id
WHERE i.batch_id = ?
ORDER BY i.custom_id, i.cluster_key, i.supplier_id;

-- name: DeleteLLMSupplierGroupCluster :exec
DELETE FROM llm_supplier_group WHERE cluster_key = ? AND prompt_version = ?;

-- name: UpsertLLMSupplierGroup :exec
INSERT INTO llm_supplier_group (supplier_id, prompt_version, cluster_key, input_hash, group_label, group_name, confidence, model, batch_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (supplier_id, prompt_version) DO UPDATE SET
  cluster_key = excluded.cluster_key, input_hash = excluded.input_hash, group_label = excluded.group_label,
  group_name = excluded.group_name, confidence = excluded.confidence, model = excluded.model,
  batch_id = excluded.batch_id, created_at = CURRENT_TIMESTAMP;

-- name: GLEIFSupplierLinks :many
-- Suppliers with an LEI, and the parent it reports.
SELECT s.id, s.name, ge.lei, ge.legal_name,
  CAST(COALESCE(gp.parent_lei, '') AS TEXT) AS parent_lei,
  CAST(COALESCE(pe.legal_name, '') AS TEXT) AS parent_name
FROM supplier s
JOIN gleif_entity ge ON ge.abn = s.abn AND s.abn <> ''
LEFT JOIN gleif_parent gp ON gp.lei = ge.lei
LEFT JOIN gleif_entity pe ON pe.lei = gp.parent_lei
WHERE s.id NOT IN (SELECT supplier_id FROM supplier_individual);

-- name: AISupplierGroups :many
-- Suppliers the model placed in a group with high or medium confidence.
SELECT l.supplier_id, s.name, l.cluster_key, l.group_label, l.group_name
FROM llm_supplier_group l JOIN supplier s ON s.id = l.supplier_id
WHERE l.prompt_version = ? AND l.group_label <> '' AND l.confidence IN ('high', 'medium')
  AND l.supplier_id NOT IN (SELECT supplier_id FROM supplier_individual);

-- name: ClearSupplierGroups :exec
DELETE FROM supplier_group;

-- name: ClearSupplierGroupMembers :exec
DELETE FROM supplier_group_member;

-- name: InsertSupplierGroup :exec
INSERT INTO supplier_group (id, name, name_basis, parent_lei) VALUES (?, ?, ?, ?);

-- name: InsertSupplierGroupMember :exec
INSERT INTO supplier_group_member (supplier_id, group_id, basis) VALUES (?, ?, ?);

-- Suppliers in the web app --------------------------------------------------------

-- name: TopSuppliers :many
-- Suppliers ranked by contract value falling in a year (annualised estimate),
-- for agencies matched to an entity. Rankings leave out suppliers who are, or
-- may be, individuals.
SELECT s.id, s.name,
  CAST(COUNT(DISTINCT c.cn_id) AS INTEGER) AS contracts,
  CAST(SUM(cf.amount) AS INTEGER) AS amount,
  CAST(COALESCE(SUM(CASE WHEN c.method = 'limited' THEN cf.amount END), 0) AS INTEGER) AS limited_amount,
  CAST(COUNT(DISTINCT a.entity_id) AS INTEGER) AS entities
FROM contract_fy cf
JOIN contract c ON c.cn_id = cf.cn_id
JOIN agency a ON a.id = c.agency_id
JOIN supplier s ON s.id = c.supplier_id
WHERE cf.fiscal_year = sqlc.arg(fy) AND a.entity_id IS NOT NULL
  AND s.id NOT IN (SELECT supplier_id FROM supplier_individual)
GROUP BY s.id, s.name
ORDER BY amount DESC
LIMIT sqlc.arg(lim);

-- name: TopSupplierGroups :many
-- As TopSuppliers, with the members of each corporate group combined into
-- one row. is_group says whether id is a group's or a supplier's. suppliers
-- counts every member of the group, as its page lists them, not only those
-- with contract value in the year.
SELECT CAST(COALESCE(m.group_id, s.id) AS INTEGER) AS id,
  CAST(m.group_id IS NOT NULL AS INTEGER) AS is_group,
  CAST(COALESCE(g.name, s.name) AS TEXT) AS name,
  CAST(COALESCE(g.name_basis, '') AS TEXT) AS name_basis,
  CAST(CASE WHEN m.group_id IS NULL THEN 1
    ELSE (SELECT COUNT(*) FROM supplier_group_member x WHERE x.group_id = m.group_id) END AS INTEGER) AS suppliers,
  CAST(COUNT(DISTINCT c.cn_id) AS INTEGER) AS contracts,
  CAST(SUM(cf.amount) AS INTEGER) AS amount,
  CAST(COALESCE(SUM(CASE WHEN c.method = 'limited' THEN cf.amount END), 0) AS INTEGER) AS limited_amount,
  CAST(COUNT(DISTINCT a.entity_id) AS INTEGER) AS entities
FROM contract_fy cf
JOIN contract c ON c.cn_id = cf.cn_id
JOIN agency a ON a.id = c.agency_id
JOIN supplier s ON s.id = c.supplier_id
LEFT JOIN supplier_group_member m ON m.supplier_id = s.id
LEFT JOIN supplier_group g ON g.id = m.group_id
WHERE cf.fiscal_year = sqlc.arg(fy) AND a.entity_id IS NOT NULL
  AND s.id NOT IN (SELECT supplier_id FROM supplier_individual)
GROUP BY COALESCE(m.group_id, -s.id)
ORDER BY amount DESC
LIMIT sqlc.arg(lim);

-- name: SupplierGroupStats :one
SELECT CAST(COUNT(*) AS INTEGER) AS group_count,
  CAST(COALESCE((SELECT COUNT(*) FROM supplier_group_member), 0) AS INTEGER) AS members
FROM supplier_group;

-- name: SearchSuppliers :many
-- Suppliers whose name contains the pattern or whose ABN matches, largest
-- first. Individuals are included: a search for a name is not a ranking.
SELECT s.id, s.name, s.abn,
  CAST(COUNT(*) AS INTEGER) AS contracts,
  CAST(SUM(c.value) AS INTEGER) AS value,
  CAST(COALESCE(m.group_id, 0) AS INTEGER) AS group_id,
  CAST(COALESCE(g.name, '') AS TEXT) AS group_name
FROM supplier s
JOIN contract c ON c.supplier_id = s.id
LEFT JOIN supplier_group_member m ON m.supplier_id = s.id
LEFT JOIN supplier_group g ON g.id = m.group_id
WHERE instr(upper(s.name), upper(sqlc.arg(fragment))) > 0 OR (CAST(sqlc.arg(abn) AS TEXT) <> '' AND s.abn = sqlc.arg(abn))
GROUP BY s.id, s.name, s.abn
ORDER BY value DESC
LIMIT sqlc.arg(lim);

-- name: SupplierGroupOf :one
SELECT g.id, g.name, g.name_basis, m.basis,
  CAST((SELECT COUNT(*) FROM supplier_group_member x WHERE x.group_id = g.id) AS INTEGER) AS members
FROM supplier_group_member m JOIN supplier_group g ON g.id = m.group_id
WHERE m.supplier_id = ?;

-- name: GetSupplierGroup :one
SELECT g.id, g.name, g.name_basis, g.parent_lei,
  CAST(COALESCE(pe.legal_name, '') AS TEXT) AS parent_name,
  CAST(COALESCE(pe.country, '') AS TEXT) AS parent_country
FROM supplier_group g LEFT JOIN gleif_entity pe ON pe.lei = g.parent_lei
WHERE g.id = ?;

-- name: SupplierGroupMembers :many
SELECT s.id, s.name, s.abn, s.country, m.basis,
  CAST(COALESCE(ge.lei, '') AS TEXT) AS lei,
  CAST(COUNT(c.cn_id) AS INTEGER) AS contracts,
  CAST(COALESCE(SUM(c.value), 0) AS INTEGER) AS value
FROM supplier_group_member m
JOIN supplier s ON s.id = m.supplier_id
LEFT JOIN gleif_entity ge ON ge.abn = s.abn AND s.abn <> ''
LEFT JOIN contract c ON c.supplier_id = s.id
WHERE m.group_id = ?
GROUP BY s.id, s.name, s.abn, s.country, m.basis, ge.lei
ORDER BY value DESC;

-- name: GroupTotals :one
SELECT CAST(COUNT(*) AS INTEGER) AS contracts,
  CAST(COALESCE(SUM(value), 0) AS INTEGER) AS value,
  CAST(COALESCE(SUM(CASE WHEN method = 'limited' THEN value END), 0) AS INTEGER) AS limited_value,
  CAST(COALESCE(SUM(CASE WHEN method = 'limited' THEN 1 END), 0) AS INTEGER) AS limited_contracts,
  CAST(COALESCE(MIN(published_date), '') AS TEXT) AS first_published,
  CAST(COALESCE(MAX(published_date), '') AS TEXT) AS last_published
FROM contract WHERE supplier_id IN (SELECT supplier_id FROM supplier_group_member WHERE group_id = ?);

-- name: GroupAgencies :many
SELECT a.name AS agency, a.entity_id,
  CAST(COUNT(*) AS INTEGER) AS contracts,
  CAST(SUM(c.value) AS INTEGER) AS value,
  CAST(COALESCE(SUM(CASE WHEN c.method = 'limited' THEN c.value END), 0) AS INTEGER) AS limited_value
FROM contract c JOIN agency a ON a.id = c.agency_id
WHERE c.supplier_id IN (SELECT supplier_id FROM supplier_group_member WHERE group_id = ?)
GROUP BY a.name, a.entity_id
ORDER BY value DESC;

-- name: GroupContracts :many
SELECT c.cn_id, c.description, c.method, c.start_date, c.end_date, c.published_date, c.value,
  c.original_value, c.amendment_count, a.name AS agency, s.id AS supplier_id, s.name AS supplier
FROM contract c
JOIN agency a ON a.id = c.agency_id
JOIN supplier s ON s.id = c.supplier_id
WHERE c.supplier_id IN (SELECT supplier_id FROM supplier_group_member WHERE group_id = sqlc.arg(group_id))
ORDER BY c.value DESC
LIMIT sqlc.arg(lim);

-- Transparency Portal -------------------------------------------------------------

-- name: ClearReportedSpend :exec
DELETE FROM reported_spend;

-- name: ClearReportedOrgs :exec
DELETE FROM reported_org;

-- name: InsertReportedSpend :exec
INSERT INTO reported_spend (entity_name, entity_id, reporting_year, kind, new_contracts, new_amount,
  ongoing_contracts, ongoing_amount, contracts, amount, check_note, source_document_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertReportedOrg :exec
INSERT INTO reported_org (entity_name, entity_id, reporting_year, kind, org_name, org_abn, amount,
  individual, check_note, source_document_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAgencyEntities :many
SELECT name, entity_id FROM agency WHERE entity_id IS NOT NULL;

-- name: EntityExpenseCeilings :many
-- Each entity's largest yearly total expenses in any PBS publication held.
SELECT y.entity_id, CAST(MAX(y.amount) AS INTEGER) AS expenses
FROM (SELECT p.entity_id, pe.source_document_id, pe.fiscal_year, SUM(pe.amount) AS amount
      FROM program_expense pe JOIN program p ON p.id = pe.program_id
      WHERE pe.treatment = 'expense'
      GROUP BY p.entity_id, pe.source_document_id, pe.fiscal_year) y
GROUP BY y.entity_id;

-- name: ABREntityTypes :many
SELECT abn, entity_type FROM abr_entity;

-- name: IndividualSupplierABNs :many
SELECT s.abn FROM supplier s JOIN supplier_individual i ON i.supplier_id = s.id WHERE s.abn <> '';

-- name: EntityReportedSpend :many
SELECT reporting_year, kind, contracts, amount, check_note
FROM reported_spend WHERE entity_id = ?
ORDER BY reporting_year DESC, kind;

-- name: EntityReportedOrgs :many
-- Organisations who are, or may be, individuals are left out of the list.
SELECT o.kind, o.org_name, o.org_abn, o.amount, o.check_note,
  CAST(COALESCE((SELECT s.id FROM supplier s WHERE s.abn = o.org_abn AND o.org_abn <> '' LIMIT 1), 0) AS INTEGER) AS supplier_id
FROM reported_org o
WHERE o.entity_id = ? AND o.reporting_year = ? AND o.individual = 0
ORDER BY o.kind, o.amount DESC;

-- name: UnlinkAbsorbedSpend :execrows
-- A body reporting separately before it was absorbed maps to its successor
-- too. For each entity, year and kind, keep the published entity with the
-- most contracts (the continuing one; counts are not affected by unit
-- errors) and unlink the others.
UPDATE reported_spend SET entity_id = NULL
WHERE entity_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM reported_spend d
  WHERE d.entity_id = reported_spend.entity_id AND d.reporting_year = reported_spend.reporting_year
    AND d.kind = reported_spend.kind AND d.entity_name <> reported_spend.entity_name
    AND (d.contracts > reported_spend.contracts
         OR (d.contracts = reported_spend.contracts AND d.entity_name < reported_spend.entity_name)));

-- name: UnlinkAbsorbedOrgs :execrows
UPDATE reported_org SET entity_id = NULL
WHERE entity_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM reported_spend s
  WHERE s.entity_name = reported_org.entity_name AND s.reporting_year = reported_org.reporting_year
    AND s.kind = reported_org.kind AND s.entity_id IS NULL);

-- name: DeletePolicyInvestmentsForDoc :exec
DELETE FROM policy_investment WHERE source_document_id = ?;

-- name: InsertPolicyInvestment :exec
INSERT INTO policy_investment (source_document_id, item, item_key, fiscal_year, estimate_type, amount, is_total)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: InvestmentDocument :one
-- The investments table from one publication: doc_type bp1_investments or
-- fbo_investments.
SELECT id, title, url FROM source_document
WHERE doc_type = ? AND budget_year = ?;

-- name: PolicyInvestments :many
SELECT item, item_key, fiscal_year, amount, is_total FROM policy_investment
WHERE source_document_id = ?;

-- name: EntityCategoryTotals :many
-- Annualised value in a year of an entity's AI-classified contracts, by
-- category, with low-confidence results kept apart.
SELECT l.category, CAST(l.confidence = 'low' AS INTEGER) AS low,
  CAST(COUNT(*) AS INTEGER) AS contracts, CAST(SUM(cf.amount) AS INTEGER) AS amount
FROM contract_fy cf
JOIN contract c ON c.cn_id = cf.cn_id
JOIN agency a ON a.id = c.agency_id
JOIN llm_classification l ON l.cn_id = c.cn_id AND l.task = sqlc.arg(task) AND l.prompt_version = sqlc.arg(prompt_version)
WHERE cf.fiscal_year = sqlc.arg(fy) AND a.entity_id = sqlc.arg(entity_id)
GROUP BY l.category, low
ORDER BY amount DESC;

-- GrantConnect ----------------------------------------------------------------------

-- name: ClearGrantFY :exec
DELETE FROM grant_fy;

-- name: ClearGrantAwards :exec
DELETE FROM grant_award;

-- name: InsertGrantAward :exec
INSERT INTO grant_award (ga_id, version_id, activity, agency_name, entity_id, publish_date, category,
  start_date, end_date, value, go_id, recipient, individual, unnamed, one_off, aggregate, source_document_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertGrantFY :exec
INSERT INTO grant_fy (ga_id, fiscal_year, amount) VALUES (?, ?, ?);

-- name: EntityGrantStats :one
-- Grants active in a year for one entity, annualised.
SELECT CAST(COUNT(*) AS INTEGER) AS grants,
  CAST(COALESCE(SUM(gf.amount), 0) AS INTEGER) AS amount,
  CAST(COUNT(DISTINCT g.recipient) AS INTEGER) AS recipients,
  CAST(COALESCE(SUM(CASE WHEN g.go_id = '' THEN gf.amount END), 0) AS INTEGER) AS adhoc_amount,
  CAST(COALESCE(SUM(CASE WHEN g.aggregate = 1 THEN gf.amount END), 0) AS INTEGER) AS aggregate_amount
FROM grant_fy gf JOIN grant_award g ON g.ga_id = gf.ga_id
WHERE gf.fiscal_year = sqlc.arg(fy) AND g.entity_id = sqlc.arg(entity_id);

-- name: EntityGrantCategories :many
SELECT g.category, CAST(COUNT(*) AS INTEGER) AS grants, CAST(SUM(gf.amount) AS INTEGER) AS amount
FROM grant_fy gf JOIN grant_award g ON g.ga_id = gf.ga_id
WHERE gf.fiscal_year = sqlc.arg(fy) AND g.entity_id = sqlc.arg(entity_id)
GROUP BY g.category
ORDER BY amount DESC
LIMIT sqlc.arg(lim);

-- name: EntityGrantRecipients :many
-- Rankings leave out recipients whose names read as a person's, and awards
-- with no named recipient.
SELECT g.recipient, CAST(COUNT(*) AS INTEGER) AS grants, CAST(SUM(gf.amount) AS INTEGER) AS amount
FROM grant_fy gf JOIN grant_award g ON g.ga_id = gf.ga_id
WHERE gf.fiscal_year = sqlc.arg(fy) AND g.entity_id = sqlc.arg(entity_id) AND g.individual = 0 AND g.unnamed = 0
GROUP BY g.recipient
ORDER BY amount DESC
LIMIT sqlc.arg(lim);

-- name: PortfolioGrantTotals :many
SELECT p.portfolio, CAST(COALESCE(SUM(gf.amount), 0) AS INTEGER) AS amount
FROM grant_fy gf
JOIN grant_award g ON g.ga_id = gf.ga_id
JOIN (SELECT DISTINCT entity_id, portfolio FROM program WHERE budget_year = sqlc.arg(budget_year)) p ON p.entity_id = g.entity_id
WHERE gf.fiscal_year = sqlc.arg(fy)
GROUP BY p.portfolio;

-- name: EntityGrantTotals :many
-- Annualised grant value in a year, by entity.
SELECT g.entity_id, CAST(COALESCE(SUM(gf.amount), 0) AS INTEGER) AS amount
FROM grant_fy gf JOIN grant_award g ON g.ga_id = gf.ga_id
WHERE gf.fiscal_year = ? AND g.entity_id IS NOT NULL
GROUP BY g.entity_id;

-- name: ClearProjects :exec
DELETE FROM project;

-- name: ClearProjectEstimates :exec
DELETE FROM project_estimate;

-- name: InsertProject :exec
INSERT INTO project (key, name, owner, summary, item_keys, funding_note, sort)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: InsertProjectEstimate :exec
INSERT INTO project_estimate (project_key, published, amount, amount_high, scope, scope_changed, completion,
  source_title, source_publisher, source_url, quote, secondary, video_url, video_title, source_document_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListProjects :many
SELECT key, name, owner, summary, item_keys, funding_note FROM project ORDER BY sort;

-- name: ListProjectEstimates :many
SELECT project_key, published, amount, amount_high, scope, scope_changed, completion,
  source_title, source_publisher, source_url, quote, secondary, video_url, video_title
FROM project_estimate ORDER BY project_key, published, amount;

-- name: ClearProjectLinks :exec
DELETE FROM project_link;

-- name: InsertProjectLink :exec
INSERT INTO project_link (project_key, entity_id, supplier_id) VALUES (?, ?, ?);

-- name: SupplierIDsByABN :many
SELECT id FROM supplier WHERE abn = ? AND abn <> '';

-- name: ProjectLinks :many
SELECT l.project_key,
  CAST(COALESCE(l.entity_id, 0) AS INTEGER) AS entity_id, COALESCE(e.name, '') AS entity_name,
  CAST(COALESCE(l.supplier_id, 0) AS INTEGER) AS supplier_id, COALESCE(s.name, '') AS supplier_name
FROM project_link l
LEFT JOIN entity e ON e.id = l.entity_id
LEFT JOIN supplier s ON s.id = l.supplier_id
ORDER BY l.project_key, e.name, s.name;

-- Site-wide search ---------------------------------------------------------

-- name: SearchEntities :many
-- Entities in the publication whose name, a former name or an AusTender agency
-- name contains the fragment, largest first. matched_name is the other name
-- that matched, or '' when the entity's own name did.
SELECT e.id, e.name, CAST(MIN(p.portfolio) AS TEXT) AS portfolio,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) AND pe.treatment = 'expense' THEN pe.amount END), 0) AS INTEGER) AS cur,
  CAST(CASE WHEN instr(upper(e.name), upper(sqlc.arg(fragment))) > 0 THEN '' ELSE COALESCE(
    (SELECT ea.alias FROM entity_alias ea WHERE ea.entity_id = e.id AND instr(upper(ea.alias), upper(sqlc.arg(fragment))) > 0 LIMIT 1),
    (SELECT a.name FROM agency a WHERE a.entity_id = e.id AND instr(upper(a.name), upper(sqlc.arg(fragment))) > 0 LIMIT 1), '') END AS TEXT) AS matched_name
FROM program_expense pe
JOIN program p ON p.id = pe.program_id
JOIN entity e ON e.id = p.entity_id
WHERE pe.source_document_id = sqlc.arg(doc_id) AND e.id IN (
  SELECT x.id FROM entity x WHERE instr(upper(x.name), upper(sqlc.arg(fragment))) > 0
  UNION SELECT ea.entity_id FROM entity_alias ea WHERE instr(upper(ea.alias), upper(sqlc.arg(fragment))) > 0
  UNION SELECT a.entity_id FROM agency a WHERE a.entity_id IS NOT NULL AND instr(upper(a.name), upper(sqlc.arg(fragment))) > 0)
GROUP BY e.id, e.name
ORDER BY cur DESC
LIMIT sqlc.arg(lim);

-- name: SearchPortfolios :many
SELECT DISTINCT portfolio FROM program
WHERE budget_year = sqlc.arg(budget_year) AND instr(upper(portfolio), upper(sqlc.arg(fragment))) > 0
ORDER BY portfolio;

-- name: SearchPrograms :many
-- Programs in the publication whose name contains the fragment, largest first.
SELECT p.id, p.name, e.id AS entity_id, e.name AS entity_name,
  CAST(COALESCE(SUM(CASE WHEN pe.fiscal_year = sqlc.arg(fy) AND pe.treatment = 'expense' THEN pe.amount END), 0) AS INTEGER) AS cur
FROM program_expense pe
JOIN program p ON p.id = pe.program_id
JOIN entity e ON e.id = p.entity_id
WHERE pe.source_document_id = sqlc.arg(doc_id) AND instr(upper(p.name), upper(sqlc.arg(fragment))) > 0
GROUP BY p.id, p.name, e.id, e.name
ORDER BY cur DESC
LIMIT sqlc.arg(lim);

-- name: SearchSupplierGroups :many
-- Corporate groups whose name contains the fragment, by the whole-of-life
-- value of their members' contracts.
SELECT g.id, g.name, CAST(COUNT(DISTINCT m.supplier_id) AS INTEGER) AS members,
  CAST(COALESCE(SUM(c.value), 0) AS INTEGER) AS value
FROM supplier_group g
JOIN supplier_group_member m ON m.group_id = g.id
LEFT JOIN contract c ON c.supplier_id = m.supplier_id
WHERE instr(upper(g.name), upper(sqlc.arg(fragment))) > 0
GROUP BY g.id, g.name
ORDER BY value DESC
LIMIT sqlc.arg(lim);

-- name: SearchContracts :many
-- The contract with this notice ID, or contracts whose description contains the
-- fragment, largest first. matches counts every match before the limit.
SELECT c.cn_id, c.description, c.value, c.published_date, a.name AS agency, a.entity_id,
  CAST(COALESCE(s.id, 0) AS INTEGER) AS supplier_id, CAST(COALESCE(s.name, '') AS TEXT) AS supplier,
  CAST(COUNT(*) OVER () AS INTEGER) AS matches
FROM contract c
JOIN agency a ON a.id = c.agency_id
LEFT JOIN supplier s ON s.id = c.supplier_id
WHERE c.cn_id = sqlc.arg(cn_id) OR instr(upper(c.description), upper(sqlc.arg(fragment))) > 0
ORDER BY c.value DESC
LIMIT sqlc.arg(lim);

-- name: SearchGrantRecipients :many
-- Grant recipients whose name contains the fragment, one row per recipient and
-- granting agency, largest first. As in the rankings, recipients whose names
-- read as a person's and awards with no named recipient are left out.
SELECT g.recipient, g.agency_name, CAST(COALESCE(g.entity_id, 0) AS INTEGER) AS entity_id,
  CAST(COUNT(*) AS INTEGER) AS grants, CAST(SUM(g.value) AS INTEGER) AS value
FROM grant_award g
WHERE g.individual = 0 AND g.unnamed = 0 AND instr(upper(g.recipient), upper(sqlc.arg(fragment))) > 0
GROUP BY g.recipient, g.agency_name, g.entity_id
ORDER BY value DESC
LIMIT sqlc.arg(lim);

-- Budget against outcome -------------------------------------------------------

-- name: LatestOutcomeYear :one
-- The latest fiscal year with whole-of-government actuals (Final Budget Outcome).
SELECT CAST(COALESCE(MAX(fiscal_year), '') AS TEXT) AS fiscal_year
FROM wog_expense WHERE estimate_type = 'actual' AND lens = 'function';

-- name: FunctionOutcomes :many
-- Expenses by function for one fiscal year in each series: the year's own
-- Budget, the revised estimate in the next Budget, and the Final Budget Outcome.
-- has_* tell a missing series apart from a zero amount.
SELECT w.category,
  CAST(COALESCE(SUM(CASE WHEN w.estimate_type = 'budget' THEN w.amount END), 0) AS INTEGER) AS budget,
  CAST(COALESCE(SUM(CASE WHEN w.estimate_type = 'estimated_actual' THEN w.amount END), 0) AS INTEGER) AS revised,
  CAST(COALESCE(SUM(CASE WHEN w.estimate_type = 'actual' THEN w.amount END), 0) AS INTEGER) AS actual,
  CAST(COUNT(CASE WHEN w.estimate_type = 'budget' THEN 1 END) AS INTEGER) AS has_budget,
  CAST(COUNT(CASE WHEN w.estimate_type = 'estimated_actual' THEN 1 END) AS INTEGER) AS has_revised,
  CAST(COUNT(CASE WHEN w.estimate_type = 'actual' THEN 1 END) AS INTEGER) AS has_actual
FROM wog_expense w
JOIN source_document sd ON sd.id = w.source_document_id
WHERE w.fiscal_year = sqlc.arg(fy) AND w.lens = 'function' AND sd.doc_type IN ('bp1_function', 'fbo_function')
GROUP BY w.category
ORDER BY actual DESC;

-- name: OutcomeDocuments :many
-- The publications behind FunctionOutcomes, one per series.
SELECT DISTINCT w.estimate_type, sd.title, sd.url
FROM wog_expense w
JOIN source_document sd ON sd.id = w.source_document_id
WHERE w.fiscal_year = sqlc.arg(fy) AND w.lens = 'function' AND sd.doc_type IN ('bp1_function', 'fbo_function');

-- Budget sub-functions ----------------------------------------------------------

-- name: DeleteWogSubfunctionsForDocType :exec
-- Clears earlier versions of the same document too, since a corrected file
-- registers as a new source_document.
DELETE FROM wog_subfunction WHERE source_document_id IN (
  SELECT id FROM source_document WHERE doc_type = ? AND budget_year = ?);

-- name: InsertWogSubfunction :exec
INSERT INTO wog_subfunction (source_document_id, source_table, fiscal_year, estimate_type,
  function_name, parent, name, position, amount, note)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FunctionAmounts :many
-- BP1 expenses by function from the expenses-by-function table of a budget
-- year, for checking that sub-functions add up.
SELECT w.fiscal_year, w.category, w.amount
FROM wog_expense w
JOIN source_document sd ON sd.id = w.source_document_id
WHERE sd.budget_year = ? AND sd.doc_type = 'bp1_function' AND w.lens = 'function';

-- name: SubfunctionAmounts :many
-- Every sub-function row loaded for a budget year, for checking that a row's
-- parent exists and that its children add up.
SELECT w.source_document_id, w.fiscal_year, w.function_name, w.parent, w.name, w.amount
FROM wog_subfunction w
JOIN source_document sd ON sd.id = w.source_document_id
WHERE sd.budget_year = ?;

-- name: SubfunctionTotals :many
-- Sub-functions and their itemised rows for the current and previous fiscal
-- year, with the document each comes from.
SELECT w.function_name, w.parent, w.name, w.note, w.source_table,
  sd.title AS source_title, sd.url AS source_url,
  CAST(COALESCE(SUM(CASE WHEN w.fiscal_year = sqlc.arg(fy) THEN w.amount END), 0) AS INTEGER) AS cur,
  CAST(COALESCE(SUM(CASE WHEN w.fiscal_year = sqlc.arg(prev_fy) THEN w.amount END), 0) AS INTEGER) AS prev
FROM wog_subfunction w
JOIN source_document sd ON sd.id = w.source_document_id
WHERE sd.budget_year = sqlc.arg(budget_year)
GROUP BY w.source_document_id, w.function_name, w.parent, w.name
ORDER BY cur DESC;
