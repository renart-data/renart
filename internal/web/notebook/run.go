package notebook

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/query"
)

// ErrUnknownSource signals that an external reference is not a known
// upstream (not a pipeline asset); the runner leaves such references
// untouched instead of failing the cell.
var ErrUnknownSource = errors.New("unknown source")

// SourceFetcher resolves an external reference (a pipeline asset or raw
// warehouse table) into data the session can use. The interface is the
// future cloud-gateway read path: asset → stream → local DuckDB; today the
// backend is the direct connection.
type SourceFetcher interface {
	// LocalDuckDBPath returns the path of a local DuckDB database that
	// already contains ref, enabling the zero-copy ATTACH import. ok is
	// false when the upstream lives elsewhere.
	LocalDuckDBPath(ctx context.Context, ref string) (path string, ok bool)
	// Fetch returns up to limit rows of the referenced upstream.
	Fetch(ctx context.Context, ref string, limit int) (columns []string, rows [][]any, err error)
}

// RenameTablesFunc rewrites table references in a SQL statement according
// to the mapping, preserving everything else (the real SQL parser — never
// a regex; see the plan's prototype warnings).
type RenameTablesFunc func(sql, dialect string, mapping map[string]string) (string, error)

// Runner executes notebook cells inside their session database.
type Runner struct {
	Store        *SessionStore
	RenameTables RenameTablesFunc
	// RenderSQL renders a cell's Jinja (date windows, variables, macros)
	// before reference rewriting and execution, so a run executes the same
	// SQL the editor previews. nil leaves the SQL untouched (tests).
	RenderSQL func(content string) (string, error)
	Fetcher   SourceFetcher
	// WarehouseExecutor runs connection-bound SQL on its source warehouse and
	// returns a typed Parquet snapshot for atomic local publication.
	WarehouseExecutor NotebookBlockExecutor
	// SourceExecutor runs Renart-owned file/object/HTTP source definitions and
	// returns the same typed artifact contract as warehouse SQL sources.
	SourceExecutor NotebookBlockExecutor
	Environment    string
	// PythonMaterializer runs a Python cell, writing its materialize() result
	// to a Parquet staging file. nil when Python execution is not wired (the
	// cell then reports a clear error instead of crashing).
	PythonMaterializer PythonMaterializer
	// ParameterValues is the validated runtime snapshot used consistently by
	// SQL rendering, Python context, source fingerprints, and run records.
	ParameterValues map[string]any
	// ImportRowCap is a strict safety limit for the legacy buffered importer.
	// Results above it fail before publication; a partial result must never
	// masquerade as an ordinary downstream relation.
	ImportRowCap int
	// PreviewLimit caps the rows returned to the UI per cell run.
	PreviewLimit int
}

// NotebookConnectionName is the broker connection exposed to Python notebook
// cells. It names the live, in-process notebook session rather than a database
// path; callers cannot use it to open the session outside the broker.
const NotebookConnectionName = "renart-notebook"

// PythonQueryFunc executes a broker query against the live notebook session.
// The broker performs authentication and read-only validation before invoking
// it; the runner rewrites logical sibling names to their durable session object
// names before querying DuckDB.
type PythonQueryFunc func(ctx context.Context, connection, sql string) (*query.QueryResult, error)

// PythonMaterializer runs cell's materialize() and writes the result to
// parquetPath. runQuery is the only data-access path provided to Python; no
// session database path or credentials cross the process boundary. It returns
// combined stdout/stderr even on failure so the traceback can be shown.
type PythonMaterializer func(ctx context.Context, cell *Cell, parquetPath string, runQuery PythonQueryFunc, parameterValues map[string]any) (logs string, err error)

const (
	defaultImportRowCap = 50000
	defaultPreviewLimit = 100
)

// CellStatus values for a cell run.
const (
	CellRunOK      = "ok"
	CellRunError   = "error"
	CellRunBlocked = "blocked"
)

// CellRunResult is the outcome of executing one cell.
type CellRunResult struct {
	CellID       string          `json:"cell_id"`
	Name         string          `json:"name"`
	ObjectName   string          `json:"object_name"`
	Status       string          `json:"status"`
	Error        string          `json:"error,omitempty"`
	Columns      []string        `json:"columns"`
	Rows         [][]any         `json:"rows"`
	TotalRows    int64           `json:"total_rows"`
	Materialized string          `json:"materialized"` // view | table
	Imports      []ImportRecord  `json:"imports,omitempty"`
	ColumnTypes  []string        `json:"column_types,omitempty"`
	Snapshot     *SnapshotRecord `json:"snapshot,omitempty"`
	// Sampled propagates through the notebook DAG: a local result derived from
	// an explicitly sampled source remains visibly sampled.
	Sampled      bool   `json:"sampled,omitempty"`
	RewrittenSQL string `json:"rewritten_sql,omitempty"`
	// Logs is the combined stdout/stderr a Python cell printed while running.
	Logs       string `json:"logs,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	// Viz is the parsed @viz directive (nil → render as a plain table).
	Viz *VizDirective `json:"viz,omitempty"`
	// VizDiagnostics are directive parse/validation findings (including
	// columns referenced that the result does not contain).
	VizDiagnostics []VizDiagnostic `json:"viz_diagnostics,omitempty"`
}

// RunOptions tunes a batch run.
type RunOptions struct {
	// RefreshImports forces re-fetching cached upstream imports.
	RefreshImports bool
}

// materializeDirective detects @materialize(table) in either comment form:
// a `--` line comment or a `/* ... */` block comment. The SQL formatter
// rewrites line comments into block comments, so both must be accepted or the
// pin would silently drop after a format.
var materializeDirective = regexp.MustCompile(`(?m)(^\s*--\s*@materialize\(\s*table\s*\)|/\*\s*@materialize\(\s*table\s*\)\s*\*/)`)

// RunCells executes the given cells in the order provided (callers use
// TopoOrder/Ancestors to build it) inside one session. A cell whose direct
// upstream failed or was blocked in this batch is reported as blocked, not
// executed — the prototype's behavior.
func (r *Runner) RunCells(ctx context.Context, nb *Notebook, cells []*Cell, opts RunOptions) ([]CellRunResult, error) {
	session, err := r.Store.Open(nb.UUID)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	failed := map[string]bool{}
	priorRuns, _ := session.listCellRuns(ctx)
	sampledByName := map[string]bool{}
	for _, candidate := range nb.Cells {
		if record, ok := priorRuns[candidate.ID]; ok && record.Sampled {
			sampledByName[strings.ToLower(candidate.Asset.Name)] = true
		}
	}
	results := make([]CellRunResult, 0, len(cells))
	for _, cell := range cells {
		// Stop promptly when the caller cancels (the client aborted the run):
		// don't start further cells, release the session lock, and let the
		// already-cancelled DuckDB statement unwind. The discarded response is
		// never read, so the partial results we return here don't matter.
		if err := ctx.Err(); err != nil {
			return results, err
		}
		blockedBy := ""
		for _, upstream := range cell.Asset.Upstreams {
			if failed[strings.ToLower(upstream.Value)] {
				blockedBy = upstream.Value
				break
			}
		}
		if blockedBy != "" {
			failed[strings.ToLower(cell.Asset.Name)] = true
			results = append(results, CellRunResult{
				CellID:     cell.ID,
				Name:       cell.Asset.Name,
				ObjectName: CellObjectName(cell.ID),
				Status:     CellRunBlocked,
				Error:      fmt.Sprintf("upstream cell %q did not run successfully", blockedBy),
				Columns:    []string{},
				Rows:       [][]any{},
			})
			continue
		}

		inheritedSample := false
		for _, upstream := range cell.Asset.Upstreams {
			if nb.CellByName(upstream.Value) != nil && sampledByName[strings.ToLower(upstream.Value)] {
				inheritedSample = true
				break
			}
		}
		result := r.runOne(ctx, session, nb, cell, opts)
		result.Sampled = result.Sampled || inheritedSample || (result.Snapshot != nil && result.Snapshot.Sampled)
		if result.Status == CellRunOK {
			if recordErr := session.recordCellRun(ctx, nb, cell, result, r.ParameterValues); recordErr != nil {
				result.Status = CellRunError
				result.Error = "could not persist notebook run metadata: " + recordErr.Error()
			}
		}
		if result.Status != CellRunOK {
			failed[strings.ToLower(cell.Asset.Name)] = true
		}
		sampledByName[strings.ToLower(cell.Asset.Name)] = result.Status == CellRunOK && result.Sampled
		results = append(results, result)
	}
	return results, nil
}

// referencesAnyMappedName reports whether content references any of the names
// the runner would rewrite, via a cheap whole-word scan (no SQL parser). When
// false, RenameTables is a no-op and can be skipped.
func referencesAnyMappedName(content string, mapping map[string]string) bool {
	for name := range mapping {
		if matchesWholeWord(content, name) {
			return true
		}
	}
	return false
}

func (r *Runner) runOne(ctx context.Context, session *Session, nb *Notebook, cell *Cell, opts RunOptions) CellRunResult {
	startedAt := time.Now()
	result := CellRunResult{
		CellID:     cell.ID,
		Name:       cell.Asset.Name,
		ObjectName: CellObjectName(cell.ID),
		Status:     CellRunError,
		Columns:    []string{},
		Rows:       [][]any{},
	}

	if IsSourceCell(cell) {
		return r.runSource(ctx, session, nb, cell, result, startedAt, opts)
	}
	if IsPythonCell(cell) {
		return r.runPython(ctx, session, nb, cell, result, startedAt)
	}
	if strings.TrimSpace(cell.Asset.Connection) != "" {
		return r.runWarehouseSQLSource(ctx, session, nb, cell, result, startedAt, opts)
	}

	content := strings.TrimSpace(cell.Asset.ExecutableFile.Content)
	if content == "" {
		result.Error = "cell is empty"
		return result
	}

	// Parse the presentation directive up front (it lives in a comment, so
	// it never affects the SQL or the fingerprint).
	viz, vizDiagnostics := ParseViz(cell.Asset.ExecutableFile.Content)
	result.Viz = viz
	result.VizDiagnostics = vizDiagnostics

	// Render Jinja before resolving references and executing. SQL comments
	// (@viz/@materialize) and sibling table names survive rendering, so the
	// directive parsing above and the reference rewriting below still apply.
	if r.RenderSQL != nil {
		rendered, renderErr := r.RenderSQL(content)
		if renderErr != nil {
			result.Error = "could not render Jinja: " + renderErr.Error()
			result.DurationMS = time.Since(startedAt).Milliseconds()
			return result
		}
		content = strings.TrimSpace(rendered)
		if content == "" {
			result.Error = "cell is empty after rendering Jinja"
			result.DurationMS = time.Since(startedAt).Milliseconds()
			return result
		}
	}

	// Resolve every reference: sibling cells map to their machine names,
	// external references are imported (cached) into the session DB.
	mapping := map[string]string{}
	for _, sibling := range nb.Cells {
		if sibling.ID == cell.ID {
			continue
		}
		mapping[sibling.Asset.Name] = CellObjectName(sibling.ID)
	}
	for _, ref := range cell.ExternalRefs {
		record, importErr := r.ensureImport(ctx, session, ref, opts.RefreshImports)
		if errors.Is(importErr, ErrUnknownSource) {
			// Not a known upstream (could be a DuckDB built-in or a typo);
			// leave the reference untouched — the session will produce a
			// clear missing-table error if it is wrong.
			continue
		}
		if importErr != nil {
			result.Error = fmt.Sprintf("failed to import upstream %q: %s", ref, importErr.Error())
			result.DurationMS = time.Since(startedAt).Milliseconds()
			return result
		}
		mapping[ref] = record.ObjectName
		result.Imports = append(result.Imports, *record)
	}

	// Only invoke the SQL parser when the cell actually references a name we
	// need to rewrite. Skipping it for cells that reference nothing — the common
	// case for source/leaf cells — avoids unnecessary parse and rewrite work.
	rewritten := strings.TrimRight(strings.TrimSpace(content), ";")
	if referencesAnyMappedName(content, mapping) {
		renamed, err := r.RenameTables(content, "duckdb", mapping)
		if err != nil {
			result.Error = "could not parse cell SQL: " + err.Error()
			result.DurationMS = time.Since(startedAt).Milliseconds()
			return result
		}
		rewritten = strings.TrimRight(strings.TrimSpace(renamed), ";")
	}
	result.RewrittenSQL = rewritten

	// Views by default; @materialize(table) pins expensive cells.
	kind := "view"
	if materializeDirective.MatchString(content) {
		kind = "table"
	}
	result.Materialized = kind

	object := quoteIdent(result.ObjectName)
	// Drop whatever object currently exists under this name, by its actual
	// type. DuckDB's `drop table if exists` errors when the name exists as a
	// view (and vice-versa), so a cell re-run — or a view↔table switch —
	// must drop the existing object precisely before recreating it.
	if existingType, typeErr := session.objectType(ctx, result.ObjectName); typeErr == nil && existingType != "" {
		dropKind := "view"
		if existingType == "BASE TABLE" || existingType == "LOCAL TEMPORARY" {
			dropKind = "table"
		}
		if err := session.Exec(ctx, fmt.Sprintf("drop %s if exists %s", dropKind, object)); err != nil {
			result.Error = normalizeDuckDBError(err)
			result.DurationMS = time.Since(startedAt).Milliseconds()
			return result
		}
	}
	if err := session.Exec(ctx, fmt.Sprintf("create or replace %s %s as (\n%s\n)", kind, object, rewritten)); err != nil {
		result.Error = normalizeDuckDBError(err)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}

	preview, err := session.Query(ctx, fmt.Sprintf("select * from %s limit %d", object, r.previewLimit()))
	if err != nil {
		result.Error = normalizeDuckDBError(err)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	result.Columns, result.ColumnTypes, result.Rows = stripNotebookBookkeeping(preview.Columns, preview.ColumnTypes, preview.Rows)
	result.Rows = normalizeRows(result.Rows)
	if viz != nil {
		result.VizDiagnostics = append(result.VizDiagnostics, ValidateVizColumns(viz, result.Columns, 0)...)
	}

	if count, countErr := session.Query(ctx, fmt.Sprintf("select count(*) from %s", object)); countErr == nil && len(count.Rows) == 1 && len(count.Rows[0]) == 1 {
		result.TotalRows = toInt64(count.Rows[0][0])
	}

	result.Status = CellRunOK
	result.DurationMS = time.Since(startedAt).Milliseconds()
	return result
}

// ensureImport makes sure the external reference is available as a table in
// the session DB, importing (or re-importing) it when needed.
func (r *Runner) ensureImport(ctx context.Context, session *Session, ref string, refresh bool) (*ImportRecord, error) {
	if existing, err := session.lookupImport(ctx, ref); err == nil && existing != nil && !refresh {
		if !existing.Complete {
			return nil, fmt.Errorf("cached upstream %q is incomplete; refresh it with a complete or explicitly sampled snapshot before running downstream cells", ref)
		}
		return existing, nil
	}

	object := ImportObjectName(ref)
	if r.Fetcher == nil {
		return nil, ErrUnknownSource
	}

	// Zero-copy path: the upstream already lives in a local DuckDB file.
	if sourcePath, ok := r.Fetcher.LocalDuckDBPath(ctx, ref); ok && sourcePath != session.Path {
		if err := r.importViaAttach(ctx, session, ref, object, sourcePath); err == nil {
			record, recordErr := r.finishImport(ctx, session, ref, object, true)
			if recordErr == nil {
				return record, nil
			}
		}
		// Attach can fail (locked file, missing table); fall through to the
		// generic fetch.
	}

	columns, rows, err := r.Fetcher.Fetch(ctx, ref, r.importRowCap()+1)
	if err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("upstream %q returned no columns", ref)
	}
	if len(rows) > r.importRowCap() {
		return nil, fmt.Errorf(
			"upstream %q returned more than the safe buffered import limit of %d rows; aggregate or filter it at the source, or use a typed snapshot adapter",
			ref, r.importRowCap(),
		)
	}

	if err := r.createImportTable(ctx, session, object, columns, rows); err != nil {
		return nil, err
	}

	record := ImportRecord{Ref: ref, ObjectName: object, RowCount: int64(len(rows)), Complete: true}
	if err := session.recordImport(ctx, record); err != nil {
		return nil, err
	}
	stored, err := session.lookupImport(ctx, ref)
	if err != nil || stored == nil {
		return &record, nil
	}
	return stored, nil
}

func (r *Runner) importViaAttach(ctx context.Context, session *Session, ref, object, sourcePath string) error {
	// One batched statement: the client may serve each call from a
	// different connection, and ATTACH visibility is per connection.
	batch := fmt.Sprintf(
		"attach %s as __renart_src (read_only); create or replace table %s as select * from %s; detach __renart_src;",
		sqlStringLiteral(sourcePath), quoteIdent(object), attachedQualifiedRef(ref))
	return session.Exec(ctx, batch)
}

func (r *Runner) finishImport(ctx context.Context, session *Session, ref, object string, complete bool) (*ImportRecord, error) {
	rowCount := int64(0)
	if count, err := session.Query(ctx, fmt.Sprintf("select count(*) from %s", quoteIdent(object))); err == nil && len(count.Rows) == 1 && len(count.Rows[0]) == 1 {
		rowCount = toInt64(count.Rows[0][0])
	}
	record := ImportRecord{Ref: ref, ObjectName: object, RowCount: rowCount, Complete: complete}
	if err := session.recordImport(ctx, record); err != nil {
		return nil, err
	}
	stored, err := session.lookupImport(ctx, ref)
	if err != nil || stored == nil {
		return &record, nil
	}
	return stored, nil
}

// attachedQualifiedRef qualifies ref inside the attached source database:
// "t" → __renart_src.main."t", "s.t" → __renart_src."s"."t", and a
// three-part "d.s.t" drops the original database name.
func attachedQualifiedRef(ref string) string {
	parts := strings.Split(ref, ".")
	for index, part := range parts {
		parts[index] = quoteIdent(strings.TrimSpace(part))
	}
	switch len(parts) {
	case 1:
		return "__renart_src.main." + parts[0]
	case 2:
		return "__renart_src." + parts[0] + "." + parts[1]
	default:
		return "__renart_src." + parts[len(parts)-2] + "." + parts[len(parts)-1]
	}
}

func (r *Runner) createImportTable(ctx context.Context, session *Session, object string, columns []string, rows [][]any) error {
	types := inferColumnTypes(columns, rows)
	defs := make([]string, 0, len(columns))
	for index, column := range columns {
		defs = append(defs, quoteIdent(column)+" "+types[index])
	}
	if err := session.Exec(ctx, fmt.Sprintf("create or replace table %s (%s)", quoteIdent(object), strings.Join(defs, ", "))); err != nil {
		return err
	}

	const batchSize = 500
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		values := make([]string, 0, end-start)
		for _, row := range rows[start:end] {
			cells := make([]string, len(columns))
			for index := range columns {
				var value any
				if index < len(row) {
					value = row[index]
				}
				cells[index] = formatSQLValue(value)
			}
			values = append(values, "("+strings.Join(cells, ", ")+")")
		}
		if err := session.Exec(ctx, fmt.Sprintf("insert into %s values %s", quoteIdent(object), strings.Join(values, ", "))); err != nil {
			return err
		}
	}
	return nil
}

// inferColumnTypes picks DuckDB column types from the fetched values:
// BOOLEAN, BIGINT (all-integral numbers), DOUBLE, else VARCHAR.
func inferColumnTypes(columns []string, rows [][]any) []string {
	types := make([]string, len(columns))
	for index := range columns {
		sawValue := false
		isBool := true
		isNumber := true
		isIntegral := true
		for _, row := range rows {
			if index >= len(row) || row[index] == nil {
				continue
			}
			sawValue = true
			switch v := row[index].(type) {
			case bool:
				isNumber = false
			case float64:
				isBool = false
				if v != math.Trunc(v) {
					isIntegral = false
				}
			case int, int32, int64:
				isBool = false
			default:
				isBool = false
				isNumber = false
			}
		}
		switch {
		case !sawValue:
			types[index] = "VARCHAR"
		case isBool:
			types[index] = "BOOLEAN"
		case isNumber && isIntegral:
			types[index] = "BIGINT"
		case isNumber:
			types[index] = "DOUBLE"
		default:
			types[index] = "VARCHAR"
		}
	}
	return types
}

func formatSQLValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case bool:
		if v {
			return "TRUE"
		}
		return "FALSE"
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1e15 {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'g', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case string:
		return sqlStringLiteral(v)
	default:
		return sqlStringLiteral(fmt.Sprintf("%v", v))
	}
}

// normalizeRows converts driver values into JSON-friendly ones.
func normalizeRows(rows [][]any) [][]any {
	normalized := make([][]any, len(rows))
	for rowIndex, row := range rows {
		out := make([]any, len(row))
		for colIndex, value := range row {
			switch v := value.(type) {
			case time.Time:
				out[colIndex] = v.Format(time.RFC3339)
			case []byte:
				out[colIndex] = string(v)
			default:
				out[colIndex] = v
			}
		}
		normalized[rowIndex] = out
	}
	return normalized
}

// Sling adds this bookkeeping column when it moves tabular data between
// connections. It is useful to the transport, but exposing it in notebook
// results makes imported data look as if the user selected an extra column and
// also pollutes runtime completion schemas.
const slingLoadedAtColumn = "_sling_loaded_at"

func stripNotebookBookkeepingColumns(columns []string, rows [][]any) ([]string, [][]any) {
	filteredColumns, _, filteredRows := stripNotebookBookkeeping(columns, nil, rows)
	return filteredColumns, filteredRows
}

func stripNotebookBookkeeping(columns, columnTypes []string, rows [][]any) ([]string, []string, [][]any) {
	keep := make([]int, 0, len(columns))
	filteredColumns := make([]string, 0, len(columns))
	filteredTypes := make([]string, 0, len(columns))
	for index, column := range columns {
		if strings.EqualFold(strings.TrimSpace(column), slingLoadedAtColumn) {
			continue
		}
		keep = append(keep, index)
		filteredColumns = append(filteredColumns, column)
		if index < len(columnTypes) {
			filteredTypes = append(filteredTypes, columnTypes[index])
		}
	}
	if len(keep) == len(columns) {
		return columns, columnTypes, rows
	}

	filteredRows := make([][]any, len(rows))
	for rowIndex, row := range rows {
		filteredRow := make([]any, 0, len(keep))
		for _, columnIndex := range keep {
			if columnIndex < len(row) {
				filteredRow = append(filteredRow, row[columnIndex])
			}
		}
		filteredRows[rowIndex] = filteredRow
	}
	return filteredColumns, filteredTypes, filteredRows
}

func toInt64(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int32:
		return int64(v)
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func (r *Runner) importRowCap() int {
	if r.ImportRowCap > 0 {
		return r.ImportRowCap
	}
	return defaultImportRowCap
}

func (r *Runner) previewLimit() int {
	if r.PreviewLimit > 0 {
		return r.PreviewLimit
	}
	return defaultPreviewLimit
}

func normalizeDuckDBError(err error) string {
	message := err.Error()
	// DuckDB prefixes errors with the failing statement context; keep the
	// message but trim the noisy "unable to run query ..." wrapper bruin adds.
	if index := strings.Index(message, ": "); index > 0 && strings.HasPrefix(message, "unable to run query") {
		if rest := message[index+2:]; rest != "" {
			return rest
		}
	}
	return message
}
