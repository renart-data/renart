package notebook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/query"
)

// runPython executes a Python cell. The cell's materialize() dataframe is
// staged as Parquet, then loaded directly into the open notebook session as
// cell_<id>. SDK queries execute through the task-scoped broker against this
// same in-process session, so neither inputs nor outputs require a throwaway
// DuckDB database.
func (r *Runner) runPython(ctx context.Context, session *Session, nb *Notebook, cell *Cell, result CellRunResult, startedAt time.Time) CellRunResult {
	if strings.TrimSpace(cell.Asset.ExecutableFile.Content) == "" {
		result.Error = "cell is empty"
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}

	if r.PythonMaterializer == nil {
		result.Error = "Python cell execution is not available in this environment"
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}

	tmpDir, err := os.MkdirTemp("", "renart-nbpy-")
	if err != nil {
		result.Error = "failed to allocate Python workspace: " + err.Error()
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	defer os.RemoveAll(tmpDir)
	parquetPath := filepath.Join(tmpDir, "materialize.parquet")

	mapping := make(map[string]string, len(nb.Cells)-1)
	for _, sibling := range nb.Cells {
		if sibling.ID != cell.ID {
			mapping[sibling.Asset.Name] = CellObjectName(sibling.ID)
		}
	}

	// A user can issue queries from multiple Python threads. DuckDB calls on the
	// shared session are serialized, just like the runner's ordinary cell work.
	var queryMu sync.Mutex
	runQuery := func(queryCtx context.Context, connection, sql string) (*query.QueryResult, error) {
		if connection != NotebookConnectionName {
			return nil, fmt.Errorf("connection %q is not available in a notebook cell", connection)
		}
		queryMu.Lock()
		defer queryMu.Unlock()

		rewritten := strings.TrimRight(strings.TrimSpace(sql), ";")
		if referencesAnyMappedName(sql, mapping) {
			if r.RenameTables == nil {
				return nil, fmt.Errorf("notebook query rewriting is not available")
			}
			renamed, err := r.RenameTables(sql, "duckdb", mapping)
			if err != nil {
				return nil, fmt.Errorf("could not resolve notebook cell references: %w", err)
			}
			rewritten = strings.TrimRight(strings.TrimSpace(renamed), ";")
		}
		return session.Query(queryCtx, rewritten)
	}

	materialized, materializeErr := r.PythonMaterializer(ctx, cell, parquetPath, runQuery, r.ParameterValues)
	result.Logs = materialized.Logs
	result.performance().TransferBytes = materialized.TransferBytes
	result.performance().PythonStartupMS = materialized.PythonStartupMS
	if materialized.EnvironmentFingerprint != "" {
		result.Fingerprint = pythonCellFingerprint(
			nb,
			cell,
			notebookParameterFingerprint(nb, r.ParameterValues),
			materialized.EnvironmentFingerprint,
		)
	}
	if materializeErr != nil {
		result.Error = normalizePythonError(materializeErr)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}

	object := quoteIdent(result.ObjectName)
	// Drop any existing object under this name (by its actual type) before
	// recreating it, mirroring the SQL path.
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

	load := fmt.Sprintf("create table %s as select * from read_parquet(%s)", object, sqlStringLiteral(parquetPath))
	if err := session.Exec(ctx, load); err != nil {
		result.Error = normalizeDuckDBError(err)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	result.Materialized = "table"

	previewStartedAt := time.Now()
	preview, err := session.Query(ctx, fmt.Sprintf("select * from %s limit %d", object, r.previewLimit()))
	result.observePreviewQuery(previewStartedAt)
	if err != nil {
		result.Error = normalizeDuckDBError(err)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	result.Columns = preview.Columns
	result.ColumnTypes = append([]string(nil), preview.ColumnTypes...)
	result.Rows = normalizeRows(preview.Rows)

	if count, countErr := session.Query(ctx, fmt.Sprintf("select count(*) from %s", object)); countErr == nil && len(count.Rows) == 1 && len(count.Rows[0]) == 1 {
		result.TotalRows = toInt64(count.Rows[0][0])
	}

	result.Status = CellRunOK
	result.DurationMS = time.Since(startedAt).Milliseconds()
	return result
}

func normalizePythonError(err error) string {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "python cell failed"
	}
	return message
}
