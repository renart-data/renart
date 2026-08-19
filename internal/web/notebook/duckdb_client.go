package notebook

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/apache/arrow-adbc/go/adbc"
	"github.com/apache/arrow-adbc/go/adbc/drivermgr"
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/query"

	"renart/internal/web/adbcutil"
)

// notebookDuckDBClient is the notebook session's narrow DuckDB adapter. Bruin's
// ADBC connection deliberately buffers Arrow results, but the ADBC Go driver
// manager currently discards the context passed to ExecuteQuery. Notebooks need
// a stronger contract: Stop must invoke the thread-safe ADBC statement cancel
// operation and wait until execution has unwound.
type notebookDuckDBClient struct {
	workspaceRoot           string
	disableFilesystemAccess bool
	database                adbc.Database
	connection              adbc.Connection
	trustedConnection       adbc.Connection
	executionGate           chan struct{}
	closed                  bool
}

func newNotebookDuckDBClient(ctx context.Context, path, workspaceRoot string, disableFilesystemAccess bool) (*notebookDuckDBClient, error) {
	return newNotebookDuckDBClientWithAccess(ctx, path, workspaceRoot, disableFilesystemAccess, false)
}

func newNotebookDuckDBClientWithAccess(
	ctx context.Context,
	path, workspaceRoot string,
	disableFilesystemAccess, readOnly bool,
) (*notebookDuckDBClient, error) {
	if err := duck.EnsureADBCDriverInstalled(ctx); err != nil {
		return nil, err
	}
	var driver drivermgr.Driver
	options := map[string]string{"driver": "duckdb", "path": path}
	if readOnly {
		options["access_mode"] = "read_only"
	}
	database, err := driver.NewDatabase(options)
	if err != nil {
		return nil, err
	}
	connection, err := database.Open(ctx)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	client := &notebookDuckDBClient{
		workspaceRoot:           cleanNotebookWorkspaceRoot(workspaceRoot),
		disableFilesystemAccess: disableFilesystemAccess,
		database:                database,
		connection:              connection,
		executionGate:           make(chan struct{}, 1),
	}
	client.executionGate <- struct{}{}
	return client, nil
}

func (c *notebookDuckDBClient) close() {
	<-c.executionGate
	defer func() { c.executionGate <- struct{}{} }()
	if c.closed {
		return
	}
	c.closed = true
	if c.trustedConnection != nil {
		_ = c.trustedConnection.Close()
		c.trustedConnection = nil
	}
	if c.connection != nil {
		_ = c.connection.Close()
		c.connection = nil
	}
	if c.database != nil {
		_ = c.database.Close()
		c.database = nil
	}
}

func (c *notebookDuckDBClient) exec(ctx context.Context, sqlText string) error {
	_, err := c.execute(ctx, sqlText, false)
	return err
}

// trustedExec runs a statement generated entirely by the notebook backend
// without applying the user-facing filesystem policy. It exists for narrow
// server-owned operations such as exporting a relation into a private
// .renart staging directory; authored notebook SQL must always use exec.
func (c *notebookDuckDBClient) trustedExec(ctx context.Context, sqlText string) error {
	_, err := c.executeSQL(ctx, sqlText, false, true)
	return err
}

func (c *notebookDuckDBClient) query(ctx context.Context, sqlText string) (*query.QueryResult, error) {
	return c.execute(ctx, sqlText, true)
}

func (c *notebookDuckDBClient) execute(ctx context.Context, sqlText string, returnRows bool) (*query.QueryResult, error) {
	return c.executeSQL(ctx, c.withWorkspace(sqlText), returnRows, false)
}

func (c *notebookDuckDBClient) executeSQL(ctx context.Context, sqlText string, returnRows, trusted bool) (*query.QueryResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.executionGate:
	}
	defer func() { c.executionGate <- struct{}{} }()
	if c.closed || c.connection == nil {
		return nil, fmt.Errorf("notebook session is closed")
	}

	connection := c.connection
	if trusted {
		// Server-authored exports must not inherit the LocalFileSystem policy
		// applied to notebook SQL. Keep that narrow path on a second connection,
		// opened only when it is actually needed.
		if c.trustedConnection == nil {
			trustedConnection, err := c.database.Open(ctx)
			if err != nil {
				return nil, err
			}
			c.trustedConnection = trustedConnection
		}
		connection = c.trustedConnection
	}

	statement, err := connection.NewStatement()
	if err != nil {
		return nil, err
	}
	defer statement.Close()
	if err := statement.SetSqlQuery(sqlText); err != nil {
		return nil, err
	}

	stopWatching, err := adbcutil.WatchStatementCancellation(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer stopWatching()

	reader, affected, err := statement.ExecuteQuery(ctx)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		var rowsAffected *int64
		if affected >= 0 {
			rowsAffected = &affected
		}
		return &query.QueryResult{
			Columns:     []string{},
			Rows:        [][]any{},
			ColumnTypes: []string{},
			Execution:   query.NewExecutionSummaryFromStatement("duckdb", query.SQLStatementType(sqlText), rowsAffected),
		}, nil
	}
	defer reader.Release()

	if !returnRows {
		return &query.QueryResult{Columns: []string{}, Rows: [][]any{}, ColumnTypes: []string{}}, nil
	}
	return bufferNotebookArrowResult(reader)
}

func (c *notebookDuckDBClient) withWorkspace(sqlText string) string {
	settings := make([]string, 0, 2)
	if c.disableFilesystemAccess {
		settings = append(settings, "SET disabled_filesystems = 'LocalFileSystem';")
	}
	if c.workspaceRoot != "" {
		root := strings.ReplaceAll(c.workspaceRoot, "'", "''")
		settings = append(settings, "SET file_search_path = '"+root+"';")
	}
	if len(settings) == 0 {
		return sqlText
	}
	return strings.Join(settings, "\n") + "\n" + sqlText
}

func cleanNotebookWorkspaceRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	return filepath.Clean(root)
}

func bufferNotebookArrowResult(reader array.RecordReader) (*query.QueryResult, error) {
	schema := reader.Schema()
	fields := schema.Fields()
	result := &query.QueryResult{
		Columns:     make([]string, len(fields)),
		Rows:        [][]any{},
		ColumnTypes: make([]string, len(fields)),
	}
	for i, field := range fields {
		result.Columns[i] = field.Name
		result.ColumnTypes[i] = normalizeNotebookArrowType(field.Type.String())
	}

	for reader.Next() {
		record := reader.RecordBatch()
		for rowIndex := range int(record.NumRows()) {
			row := make([]any, int(record.NumCols()))
			for columnIndex := range int(record.NumCols()) {
				column := record.Column(columnIndex)
				if !column.IsNull(rowIndex) {
					row[columnIndex] = notebookArrowValue(column, rowIndex)
				}
			}
			result.Rows = append(result.Rows, row)
		}
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func notebookArrowValue(column arrow.Array, index int) any { //nolint:cyclop
	switch values := column.(type) {
	case *array.Boolean:
		return values.Value(index)
	case *array.Int8:
		return int64(values.Value(index))
	case *array.Int16:
		return int64(values.Value(index))
	case *array.Int32:
		return int64(values.Value(index))
	case *array.Int64:
		return values.Value(index)
	case *array.Uint8:
		return int64(values.Value(index))
	case *array.Uint16:
		return int64(values.Value(index))
	case *array.Uint32:
		return int64(values.Value(index))
	case *array.Uint64:
		return int64(values.Value(index)) //nolint:gosec // matches Bruin's DuckDB result contract
	case *array.Float32:
		return float64(values.Value(index))
	case *array.Float64:
		return values.Value(index)
	case *array.String:
		return strings.Clone(values.Value(index))
	case *array.LargeString:
		return strings.Clone(values.Value(index))
	case *array.Binary:
		return append([]byte(nil), values.Value(index)...)
	case *array.LargeBinary:
		return append([]byte(nil), values.Value(index)...)
	case *array.Date32:
		return values.Value(index).ToTime().Format(time.RFC3339)
	case *array.Date64:
		return values.Value(index).ToTime().Format(time.RFC3339)
	case *array.Time32:
		return values.Value(index).ToTime(values.DataType().(*arrow.Time32Type).Unit).Format(time.RFC3339Nano)
	case *array.Time64:
		return values.Value(index).ToTime(values.DataType().(*arrow.Time64Type).Unit).Format(time.RFC3339Nano)
	case *array.Timestamp:
		return values.Value(index).ToTime(values.DataType().(*arrow.TimestampType).Unit).Format(time.RFC3339Nano)
	case *array.Decimal128:
		typeInfo := values.DataType().(*arrow.Decimal128Type)
		return roundNotebookDecimal(values.Value(index), int32(typeInfo.Scale))
	case *array.List:
		start, end := values.ValueOffsets(index)
		items := values.ListValues()
		result := make([]any, int(end-start))
		for itemIndex := start; itemIndex < end; itemIndex++ {
			if !items.IsNull(int(itemIndex)) {
				result[itemIndex-start] = notebookArrowValue(items, int(itemIndex))
			}
		}
		return result
	case *array.Struct:
		result := make(map[string]any, values.NumField())
		typeInfo := values.DataType().(*arrow.StructType)
		for fieldIndex := range values.NumField() {
			field := values.Field(fieldIndex)
			if field.IsNull(index) {
				result[typeInfo.Field(fieldIndex).Name] = nil
			} else {
				result[typeInfo.Field(fieldIndex).Name] = notebookArrowValue(field, index)
			}
		}
		return result
	case *array.Map:
		keys := values.Keys()
		items := values.Items()
		start, end := values.ValueOffsets(index)
		result := make(map[string]any, int(end-start))
		for itemIndex := start; itemIndex < end; itemIndex++ {
			key := fmt.Sprintf("%v", notebookArrowValue(keys, int(itemIndex)))
			if items.IsNull(int(itemIndex)) {
				result[key] = nil
			} else {
				result[key] = notebookArrowValue(items, int(itemIndex))
			}
		}
		return result
	default:
		return strings.Clone(column.ValueStr(index))
	}
}

func roundNotebookDecimal(value decimal128.Num, scale int32) float64 {
	result := value.ToFloat64(scale)
	if scale <= 0 {
		return result
	}
	multiplier := math.Pow10(int(scale))
	return math.Round(result*multiplier) / multiplier
}

var notebookArrowTypeToDuckDBType = map[string]string{
	"utf8": "VARCHAR", "large_utf8": "VARCHAR", "int8": "TINYINT", "int16": "SMALLINT",
	"int32": "INTEGER", "int64": "BIGINT", "uint8": "UTINYINT", "uint16": "USMALLINT",
	"uint32": "UINTEGER", "uint64": "UBIGINT", "float16": "FLOAT", "float32": "FLOAT",
	"float64": "DOUBLE", "bool": "BOOLEAN", "date32": "DATE", "date64": "DATE",
	"binary": "BLOB", "large_binary": "BLOB", "null": "NULL",
}

func normalizeNotebookArrowType(typeName string) string {
	if normalized, ok := notebookArrowTypeToDuckDBType[typeName]; ok {
		return normalized
	}
	switch {
	case strings.HasPrefix(typeName, "timestamp["):
		return "TIMESTAMP"
	case strings.HasPrefix(typeName, "time32["), strings.HasPrefix(typeName, "time64["):
		return "TIME"
	case strings.HasPrefix(typeName, "decimal"):
		if paramsAt := strings.Index(typeName, "("); paramsAt >= 0 {
			return "DECIMAL" + strings.ReplaceAll(typeName[paramsAt:], " ", "")
		}
		return "DECIMAL"
	case strings.HasPrefix(typeName, "list<"):
		return "LIST"
	case strings.HasPrefix(typeName, "struct<"):
		return "STRUCT"
	case strings.HasPrefix(typeName, "map<"):
		return "MAP"
	case strings.HasPrefix(typeName, "fixed_size_binary"):
		return "BLOB"
	default:
		return typeName
	}
}
