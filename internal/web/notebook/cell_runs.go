package notebook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// CellRunRecord is the restart-safe summary of one successful notebook cell
// publication. Preview rows remain in the session relation and are queried on
// demand instead of being duplicated in metadata.
type CellRunRecord struct {
	CellID            string          `json:"cell_id"`
	CellFingerprint   string          `json:"cell_fingerprint"`
	FinishedAt        string          `json:"finished_at"`
	Status            string          `json:"status"`
	MaterializedAs    string          `json:"materialized_as"`
	RowCount          int64           `json:"row_count"`
	Schema            []TabularColumn `json:"schema"`
	DurationMS        int64           `json:"duration_ms"`
	SourceSnapshotIDs []string        `json:"source_snapshot_ids,omitempty"`
	Sampled           bool            `json:"sampled,omitempty"`
}

func (s *Session) recordCellRun(ctx context.Context, nb *Notebook, cell *Cell, result CellRunResult, parameterValues map[string]any) error {
	schema := make([]TabularColumn, 0, len(result.Columns))
	for index, name := range result.Columns {
		columnType := "UNKNOWN"
		if index < len(result.ColumnTypes) && strings.TrimSpace(result.ColumnTypes[index]) != "" {
			columnType = result.ColumnTypes[index]
		}
		schema = append(schema, TabularColumn{Name: name, Type: columnType})
	}
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	snapshotIDs := make([]string, 0, len(result.Imports)+1)
	if result.Snapshot != nil {
		snapshotIDs = append(snapshotIDs, result.Snapshot.BlockID)
	}
	for _, imported := range result.Imports {
		snapshotIDs = append(snapshotIDs, imported.Ref)
	}
	snapshotJSON, err := json.Marshal(snapshotIDs)
	if err != nil {
		return err
	}
	return s.Exec(ctx, fmt.Sprintf(
		`insert or replace into %s (
cell_id, cell_fingerprint, finished_at, status, materialized_as,
row_count, schema_json, duration_ms, source_snapshot_ids, sampled
) values (%s, %s, now(), %s, %s, %d, %s, %d, %s, %t)`,
		cellRunManifestTable,
		sqlStringLiteral(cell.ID),
		sqlStringLiteral(CellFingerprintWithParameters(nb, cell, parameterValues)),
		sqlStringLiteral(result.Status),
		sqlStringLiteral(result.Materialized),
		result.TotalRows,
		sqlStringLiteral(string(schemaJSON)),
		result.DurationMS,
		sqlStringLiteral(string(snapshotJSON)),
		result.Sampled,
	))
}

func (s *Session) listCellRuns(ctx context.Context) (map[string]CellRunRecord, error) {
	result, err := s.Query(ctx, fmt.Sprintf(
		`select cell_id, cell_fingerprint, cast(finished_at as varchar), status,
materialized_as, row_count, schema_json, duration_ms, source_snapshot_ids, sampled
from %s`, cellRunManifestTable))
	if err != nil {
		return nil, err
	}
	records := make(map[string]CellRunRecord, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) < 10 {
			continue
		}
		record := CellRunRecord{
			CellID:          stringValue(row[0]),
			CellFingerprint: stringValue(row[1]),
			FinishedAt:      stringValue(row[2]),
			Status:          stringValue(row[3]),
			MaterializedAs:  stringValue(row[4]),
			RowCount:        toInt64(row[5]),
			DurationMS:      toInt64(row[7]),
		}
		if record.CellID == "" {
			continue
		}
		_ = json.Unmarshal([]byte(stringValue(row[6])), &record.Schema)
		_ = json.Unmarshal([]byte(stringValue(row[8])), &record.SourceSnapshotIDs)
		record.Sampled, _ = row[9].(bool)
		records[record.CellID] = record
	}
	return records, nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// RestoreCellRunResults reconstructs successful summaries from an existing
// notebook session. It verifies both the authored fingerprint and the physical
// object before trusting a record; changed cells and their descendants are
// returned stale instead of receiving an old preview.
func (store *SessionStore) RestoreCellRunResults(ctx context.Context, nb *Notebook, previewLimit int) (map[string]CellRunResult, map[string]bool, error) {
	results := map[string]CellRunResult{}
	stale := map[string]bool{}
	if nb == nil {
		return results, stale, nil
	}
	if _, err := os.Stat(store.DBPath(nb.UUID)); os.IsNotExist(err) {
		return results, stale, nil
	}
	session, err := store.Open(nb.UUID)
	if err != nil {
		return nil, nil, err
	}
	defer session.Close()
	records, err := session.listCellRuns(ctx)
	if err != nil {
		return nil, nil, err
	}
	if previewLimit <= 0 {
		previewLimit = defaultPreviewLimit
	}
	for _, cell := range nb.Cells {
		record, exists := records[cell.ID]
		if !exists || record.Status != CellRunOK {
			continue
		}
		if record.CellFingerprint != CellFingerprint(nb, cell) {
			stale[cell.ID] = true
			continue
		}
		objectName := CellObjectName(cell.ID)
		objectType, typeErr := session.objectType(ctx, objectName)
		if typeErr != nil || objectType == "" {
			stale[cell.ID] = true
			continue
		}
		preview, queryErr := session.Query(ctx, fmt.Sprintf(
			"select * from %s limit %d", quoteIdent(objectName), previewLimit))
		if queryErr != nil {
			stale[cell.ID] = true
			continue
		}
		columns, columnTypes, rows := stripNotebookBookkeeping(preview.Columns, preview.ColumnTypes, preview.Rows)
		result := CellRunResult{
			CellID: cell.ID, Name: cell.Asset.Name, ObjectName: objectName,
			Status: CellRunOK, Columns: columns, ColumnTypes: columnTypes,
			Rows: normalizeRows(rows), TotalRows: record.RowCount,
			Materialized: record.MaterializedAs, DurationMS: record.DurationMS,
			Sampled: record.Sampled,
		}
		if !IsPythonCell(cell) && !IsSourceCell(cell) && strings.TrimSpace(cell.Asset.Connection) == "" {
			result.Viz, result.VizDiagnostics = ParseViz(cell.Asset.ExecutableFile.Content)
			if result.Viz != nil {
				result.VizDiagnostics = append(result.VizDiagnostics, ValidateVizColumns(result.Viz, result.Columns, 0)...)
			}
		}
		if strings.TrimSpace(cell.Asset.Connection) != "" || IsSourceCell(cell) {
			result.Snapshot, _ = session.lookupSnapshot(ctx, cell.ID)
		}
		results[cell.ID] = result
	}
	for cellID := range stale {
		if cell := nb.CellByID(cellID); cell != nil {
			for _, descendant := range Descendants(nb, cell) {
				stale[descendant.ID] = true
				delete(results, descendant.ID)
			}
		}
	}
	return results, stale, nil
}
