package notebook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SnapshotRecord is durable runtime provenance for one data-producing source
// block in the notebook session database.
type SnapshotRecord struct {
	BlockID               string          `json:"block_id"`
	ObjectName            string          `json:"object_name"`
	SourceKind            string          `json:"source_kind"`
	Environment           string          `json:"environment,omitempty"`
	Connection            string          `json:"connection,omitempty"`
	DefinitionFingerprint string          `json:"definition_fingerprint"`
	SourceFingerprint     string          `json:"source_fingerprint,omitempty"`
	ImportedAt            string          `json:"imported_at"`
	RowCount              int64           `json:"row_count"`
	ByteCount             int64           `json:"byte_count"`
	Complete              bool            `json:"complete"`
	Sampled               bool            `json:"sampled"`
	Schema                []TabularColumn `json:"schema"`
	Warnings              []string        `json:"warnings,omitempty"`
}

// InspectParquetArtifact reads schema and row-count metadata through DuckDB,
// preserving Parquet physical types without observing or JSON-coercing values.
func InspectParquetArtifact(ctx context.Context, path string, provenance SnapshotProvenance, complete, sampled bool) (TabularArtifact, error) {
	info, err := os.Stat(path)
	if err != nil {
		return TabularArtifact{}, fmt.Errorf("inspect notebook snapshot: %w", err)
	}
	client, err := newNotebookDuckDBClient(ctx, ":memory:", "", false)
	if err != nil {
		return TabularArtifact{}, err
	}
	defer client.close()
	parquet := sqlStringLiteral(path)
	schemaResult, err := client.query(ctx, "select * from read_parquet("+parquet+") limit 0")
	if err != nil {
		return TabularArtifact{}, fmt.Errorf("inspect notebook snapshot schema: %w", err)
	}
	schema := make([]TabularColumn, len(schemaResult.Columns))
	for index, name := range schemaResult.Columns {
		columnType := ""
		if index < len(schemaResult.ColumnTypes) {
			columnType = strings.TrimSpace(schemaResult.ColumnTypes[index])
		}
		schema[index] = TabularColumn{Name: name, Type: columnType}
	}
	countResult, err := client.query(ctx, "select count(*) from read_parquet("+parquet+")")
	if err != nil {
		return TabularArtifact{}, fmt.Errorf("inspect notebook snapshot rows: %w", err)
	}
	rowCount := int64(0)
	if len(countResult.Rows) > 0 && len(countResult.Rows[0]) > 0 {
		rowCount = toInt64(countResult.Rows[0][0])
	}
	artifact := TabularArtifact{
		Path: path, Schema: schema, RowCount: rowCount, ByteCount: info.Size(),
		Complete: complete, Sampled: sampled, Provenance: provenance,
	}
	if err := artifact.ValidateForPublication(); err != nil {
		return TabularArtifact{}, err
	}
	return artifact, nil
}

// publishSnapshot atomically swaps a validated Parquet artifact into its
// durable relation name and records provenance in the same DuckDB transaction.
func (s *Session) publishSnapshot(ctx context.Context, blockID, object string, artifact TabularArtifact) (*SnapshotRecord, error) {
	if err := artifact.ValidateForPublication(); err != nil {
		return nil, err
	}
	existingType, err := s.objectType(ctx, object)
	if err != nil {
		return nil, err
	}
	incoming := "__renart_incoming_" + objectNameSanitizer.ReplaceAllString(strings.ToLower(object), "_")
	drop := ""
	if existingType != "" {
		dropKind := "view"
		if existingType == "BASE TABLE" || existingType == "LOCAL TEMPORARY" {
			dropKind = "table"
		}
		drop = fmt.Sprintf("drop %s if exists %s;", dropKind, quoteIdent(object))
	}
	schemaJSON, err := json.Marshal(artifact.Schema)
	if err != nil {
		return nil, err
	}
	warningsJSON, err := json.Marshal(artifact.Provenance.Warnings)
	if err != nil {
		return nil, err
	}
	statement := fmt.Sprintf(
		"begin transaction;\n"+
			"create or replace table %s as select * from read_parquet(%s);\n"+
			"%s\n"+
			"alter table %s rename to %s;\n"+
			"insert or replace into %s ("+
			"block_id, object_name, source_kind, environment, connection, "+
			"definition_fingerprint, source_fingerprint, imported_at, "+
			"row_count, byte_count, complete, sampled, schema_json, warnings_json"+
			") values (%s, %s, %s, %s, %s, %s, %s, now(), %d, %d, %t, %t, %s, %s);\n"+
			"commit;",
		quoteIdent(incoming), sqlStringLiteral(artifact.Path), drop,
		quoteIdent(incoming), quoteIdent(object), snapshotManifestTable,
		sqlStringLiteral(blockID), sqlStringLiteral(object),
		sqlStringLiteral(artifact.Provenance.SourceKind),
		sqlStringLiteral(artifact.Provenance.Environment),
		sqlStringLiteral(artifact.Provenance.Connection),
		sqlStringLiteral(artifact.Provenance.DefinitionFingerprint),
		sqlStringLiteral(artifact.Provenance.SourceFingerprint),
		artifact.RowCount, artifact.ByteCount, artifact.Complete, artifact.Sampled,
		sqlStringLiteral(string(schemaJSON)), sqlStringLiteral(string(warningsJSON)),
	)
	if err := s.Exec(ctx, statement); err != nil {
		return nil, err
	}
	return s.lookupSnapshot(ctx, blockID)
}

func (s *Session) lookupSnapshot(ctx context.Context, blockID string) (*SnapshotRecord, error) {
	result, err := s.Query(ctx, fmt.Sprintf(
		`select block_id, object_name, source_kind, environment, connection,
definition_fingerprint, source_fingerprint, cast(imported_at as varchar),
row_count, byte_count, complete, sampled, schema_json, warnings_json
from %s where block_id = %s`, snapshotManifestTable, sqlStringLiteral(blockID)))
	if err != nil || len(result.Rows) == 0 {
		return nil, err
	}
	row := result.Rows[0]
	if len(row) < 14 {
		return nil, fmt.Errorf("notebook snapshot manifest row is incomplete")
	}
	record := &SnapshotRecord{
		BlockID: stringValue(row[0]), ObjectName: stringValue(row[1]),
		SourceKind: stringValue(row[2]), Environment: stringValue(row[3]),
		Connection: stringValue(row[4]), DefinitionFingerprint: stringValue(row[5]),
		SourceFingerprint: stringValue(row[6]), ImportedAt: stringValue(row[7]),
		RowCount: toInt64(row[8]), ByteCount: toInt64(row[9]),
	}
	record.Complete, _ = row[10].(bool)
	record.Sampled, _ = row[11].(bool)
	_ = json.Unmarshal([]byte(stringValue(row[12])), &record.Schema)
	_ = json.Unmarshal([]byte(stringValue(row[13])), &record.Warnings)
	return record, nil
}
