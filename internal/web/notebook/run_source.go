package notebook

import (
	"context"
	"fmt"
	"strings"
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
	input := ExecuteBlockInput{
		Notebook: nb, Cell: cell, Environment: r.Environment, Refresh: opts.RefreshImports,
		ParameterValues: r.ParameterValues,
	}
	if opts.ReuseSourceSnapshots {
		if cached, ok := r.reuseSourceSnapshot(ctx, session, cell, result, startedAt, r.SourceExecutor, input, cell.Raw); ok {
			return cached
		}
	}
	output, err := r.SourceExecutor.Execute(ctx, input)
	return r.publishSourceOutput(ctx, session, cell, result, startedAt, cell.Raw, output, err)
}

func (r *Runner) reuseSourceSnapshot(
	ctx context.Context,
	session *Session,
	cell *Cell,
	result CellRunResult,
	startedAt time.Time,
	executor NotebookBlockExecutor,
	input ExecuteBlockInput,
	rewritten string,
) (CellRunResult, bool) {
	fingerprinter, ok := executor.(SnapshotDefinitionFingerprinter)
	if !ok {
		return result, false
	}
	expectedFingerprint, err := fingerprinter.SnapshotDefinitionFingerprint(ctx, input)
	if err != nil || strings.TrimSpace(expectedFingerprint) == "" {
		return result, false
	}
	record, err := session.lookupSnapshot(ctx, cell.ID)
	if err != nil || record == nil {
		return result, false
	}
	object := CellObjectName(cell.ID)
	expectedConnection := strings.TrimSpace(cell.Asset.Connection)
	if IsSourceCell(cell) {
		expectedConnection = strings.TrimSpace(cell.Source.Connection)
	}
	if record.ObjectName != object ||
		record.DefinitionFingerprint != expectedFingerprint ||
		strings.TrimSpace(record.Environment) != strings.TrimSpace(input.Environment) ||
		strings.TrimSpace(record.Connection) != expectedConnection {
		return result, false
	}
	mode, _, err := SourceSnapshotPolicy(cell)
	if err != nil || record.Complete != (mode == SnapshotModeFull) || record.Sampled != (mode == SnapshotModeSample) {
		return result, false
	}
	if objectType, typeErr := session.objectType(ctx, object); typeErr != nil || objectType == "" {
		return result, false
	}

	previewStartedAt := time.Now()
	preview, err := session.Query(ctx, fmt.Sprintf("select * from %s limit %d", quoteIdent(object), r.previewLimit()))
	result.observePreviewQuery(previewStartedAt)
	if err != nil {
		return result, false
	}
	result.Columns, result.ColumnTypes, result.Rows = stripNotebookBookkeeping(preview.Columns, preview.ColumnTypes, preview.Rows)
	result.Rows = normalizeRows(result.Rows)
	result.TotalRows = record.RowCount
	result.Snapshot = record
	result.Materialized = "table"
	result.RewrittenSQL = rewritten
	result.Status = CellRunOK
	result.DurationMS = time.Since(startedAt).Milliseconds()
	return result, true
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

	previewStartedAt := time.Now()
	preview, err := session.Query(ctx, fmt.Sprintf("select * from %s limit %d", quoteIdent(object), r.previewLimit()))
	result.observePreviewQuery(previewStartedAt)
	if err != nil {
		result.Error = normalizeDuckDBError(err)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	result.Columns, result.ColumnTypes, result.Rows = stripNotebookBookkeeping(preview.Columns, preview.ColumnTypes, preview.Rows)
	result.Rows = normalizeRows(result.Rows)
	result.TotalRows = output.Artifact.RowCount
	result.performance().TransferBytes = record.ByteCount
	result.Status = CellRunOK
	result.DurationMS = time.Since(startedAt).Milliseconds()
	return result
}
