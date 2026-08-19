package notebook

import (
	"context"
	"fmt"
	"time"
)

func (r *Runner) runSource(
	ctx context.Context,
	session *Session,
	nb *Notebook,
	cell *Cell,
	result CellRunResult,
	startedAt time.Time,
	opts RunOptions,
) CellRunResult {
	if len(cell.Asset.Upstreams) > 0 {
		result.Error = fmt.Sprintf("source %q cannot depend on another notebook cell", cell.Asset.Name)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	if r.SourceExecutor == nil {
		result.Error = "file and HTTP source execution is not available in this environment"
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	output, err := r.SourceExecutor.Execute(ctx, ExecuteBlockInput{
		Notebook: nb, Cell: cell, Environment: r.Environment, Refresh: opts.RefreshImports,
		ParameterValues: r.ParameterValues,
	})
	return r.publishSourceOutput(ctx, session, cell, result, startedAt, cell.Raw, output, err)
}

func (r *Runner) publishSourceOutput(
	ctx context.Context,
	session *Session,
	cell *Cell,
	result CellRunResult,
	startedAt time.Time,
	rewritten string,
	output BlockOutput,
	executeErr error,
) CellRunResult {
	if output.Cleanup != nil {
		defer output.Cleanup()
	}
	result.Logs = output.Logs
	if executeErr != nil {
		result.Error = executeErr.Error()
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	if output.Artifact == nil {
		result.Error = "source did not produce a tabular snapshot"
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	if err := output.Artifact.ValidateForPublication(); err != nil {
		result.Error = "source produced an invalid snapshot: " + err.Error()
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	object := CellObjectName(cell.ID)
	record, err := session.publishSnapshot(ctx, cell.ID, object, *output.Artifact)
	if err != nil {
		result.Error = normalizeDuckDBError(err)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	result.Snapshot = record
	result.Materialized = "table"
	result.RewrittenSQL = rewritten

	preview, err := session.Query(ctx, fmt.Sprintf("select * from %s limit %d", quoteIdent(object), r.previewLimit()))
	if err != nil {
		result.Error = normalizeDuckDBError(err)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	result.Columns, result.ColumnTypes, result.Rows = stripNotebookBookkeeping(preview.Columns, preview.ColumnTypes, preview.Rows)
	result.Rows = normalizeRows(result.Rows)
	result.TotalRows = output.Artifact.RowCount
	result.Status = CellRunOK
	result.DurationMS = time.Since(startedAt).Milliseconds()
	return result
}
