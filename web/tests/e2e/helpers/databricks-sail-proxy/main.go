// databricks-sail-proxy is a deliberately small, test-only compatibility
// server. It accepts the HTTPS Thrift protocol used by Databricks SQL clients
// and executes statements against a Sail Flight SQL server.
//
// It is not a Databricks emulator. It covers the base TCLIService calls needed
// by Renart's multi-warehouse E2E test and intentionally omits Databricks-only
// features such as OAuth, Unity Catalog authorization, Cloud Fetch, and direct
// Arrow results.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/apache/arrow-adbc/go/adbc/sqldriver/flightsql"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/decimal256"
	"github.com/apache/thrift/lib/go/thrift"
	"github.com/databricks/databricks-sql-go/internal/cli_service"
	"github.com/databricks/databricks-sql-go/internal/client"
)

type queryOperation struct {
	handle  *cli_service.TOperationHandle
	schema  *cli_service.TTableSchema
	columns []*cli_service.TColumn
	fetched bool
}

type proxyServer struct {
	db *sql.DB

	mu         sync.Mutex
	operations map[string]*queryOperation
	relations  map[string]string
}

type columnKind int

const (
	columnString columnKind = iota
	columnBool
	columnI8
	columnI16
	columnI32
	columnI64
	columnDouble
	columnBinary
)

type resultColumn struct {
	name             string
	kind             columnKind
	typeID           cli_service.TTypeId
	decimalPrecision int32
	decimalScale     int32
	values           []any
}

type describedColumn struct {
	name     string
	dataType string
}

var informationSchemaColumnsPattern = regexp.MustCompile(
	`(?is)table_schema\s*=\s*(?:lower\()?['"]([^'"]+)['"]\)?\s+and\s+table_name\s*=\s*(?:lower\()?['"]([^'"]+)['"]\)?`,
)
var createRelationPattern = regexp.MustCompile(
	"(?is)^\\s*create\\s+(?:or\\s+replace\\s+)?(table|view)\\s+(?:if\\s+not\\s+exists\\s+)?([A-Za-z0-9_.`]+)",
)
var dropRelationPattern = regexp.MustCompile(
	"(?is)^\\s*drop\\s+(?:table|view)\\s+(?:if\\s+exists\\s+)?([A-Za-z0-9_.`]+)",
)
var usingDeltaPattern = regexp.MustCompile(`(?i)\busing\s+delta\b`)
var renameTablePattern = regexp.MustCompile(
	"(?is)^\\s*alter\\s+table\\s+([A-Za-z0-9_.`]+)\\s+rename\\s+to\\s+([A-Za-z0-9_.`]+)\\s*;?\\s*$",
)
var truncateTablePattern = regexp.MustCompile(
	"(?is)^\\s*truncate\\s+table\\s+([A-Za-z0-9_.`]+)\\s*;?\\s*$",
)
var deleteRowsPattern = regexp.MustCompile(
	"(?is)^\\s*delete\\s+from\\s+([A-Za-z0-9_.`]+)\\s+where\\s+(.+?)\\s*;?\\s*$",
)
var numericParameterPattern = regexp.MustCompile(`^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$`)
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func main() {
	listenAddress := flag.String("listen", "127.0.0.1:0", "HTTPS listen address")
	flightURI := flag.String("flight-uri", "grpc://127.0.0.1:32010", "Sail Flight SQL URI")
	tlsDirectory := flag.String("tls-dir", "", "directory for the generated test CA and server certificate")
	schemaName := flag.String("schema", "analytics", "schema to create before accepting connections")
	flag.Parse()

	if *tlsDirectory == "" {
		log.Fatal("--tls-dir is required")
	}

	db, err := sql.Open("flightsql", "uri="+*flightURI)
	if err != nil {
		log.Fatalf("open Sail Flight SQL connection: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var ready int
	if err := db.QueryRowContext(ctx, "select 1").Scan(&ready); err != nil {
		log.Fatalf("query Sail Flight SQL server: %v", err)
	}
	if ready != 1 {
		log.Fatalf("unexpected Sail readiness result: %d", ready)
	}
	if !identifierPattern.MatchString(*schemaName) {
		log.Fatalf("invalid --schema value %q", *schemaName)
	}
	rows, err := db.QueryContext(ctx, "create schema if not exists "+*schemaName)
	if err != nil {
		log.Fatalf("create Sail test schema %q: %v", *schemaName, err)
	}
	if err := rows.Close(); err != nil {
		log.Fatalf("close Sail schema result: %v", err)
	}

	certPath, keyPath, err := writeTestCertificates(*tlsDirectory)
	if err != nil {
		log.Fatalf("generate test TLS certificate: %v", err)
	}

	proxy := &proxyServer{
		db:         db,
		operations: make(map[string]*queryOperation),
		relations:  make(map[string]string),
	}
	service := &client.TestClient{
		FnOpenSession:          proxy.openSession,
		FnCloseSession:         proxy.closeSession,
		FnExecuteStatement:     proxy.executeStatement,
		FnGetOperationStatus:   proxy.getOperationStatus,
		FnGetResultSetMetadata: proxy.getResultSetMetadata,
		FnFetchResults:         proxy.fetchResults,
		FnCancelOperation:      proxy.cancelOperation,
		FnCloseOperation:       proxy.closeOperation,
	}
	processor := cli_service.NewTCLIServiceProcessor(service)
	protocolFactory := thrift.NewTBinaryProtocolFactoryConf(&thrift.TConfiguration{})
	thriftHandler := thrift.NewThriftHandlerFunc(processor, protocolFactory, protocolFactory)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", http.HandlerFunc(thriftHandler))

	httpServer := &http.Server{
		Addr:              *listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("Databricks compatibility endpoint listening on https://%s", *listenAddress)
		serverErrors <- httpServer.ListenAndServeTLS(certPath, keyPath)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-signals:
		log.Printf("received %s; shutting down", sig)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTPS server failed: %v", err)
		}
	}
}

func (s *proxyServer) openSession(_ context.Context, _ *cli_service.TOpenSessionReq) (*cli_service.TOpenSessionResp, error) {
	return &cli_service.TOpenSessionResp{
		Status:                successStatus(),
		ServerProtocolVersion: cli_service.TProtocolVersion_SPARK_CLI_SERVICE_PROTOCOL_V8,
		SessionHandle: &cli_service.TSessionHandle{
			SessionId: newHandleIdentifier(),
		},
		Configuration: map[string]string{},
	}, nil
}

func (s *proxyServer) closeSession(_ context.Context, _ *cli_service.TCloseSessionReq) (*cli_service.TCloseSessionResp, error) {
	return &cli_service.TCloseSessionResp{Status: successStatus()}, nil
}

func (s *proxyServer) executeStatement(ctx context.Context, req *cli_service.TExecuteStatementReq) (*cli_service.TExecuteStatementResp, error) {
	operation, err := s.runQuery(ctx, req.Statement, req.Parameters)
	if err != nil {
		log.Printf("statement failed: %s: %v", compactSQL(req.Statement), err)
		return &cli_service.TExecuteStatementResp{Status: errorStatus(err)}, nil
	}

	key := handleKey(operation.handle.OperationId)
	s.mu.Lock()
	s.operations[key] = operation
	s.mu.Unlock()

	rowCount := 0
	if len(operation.columns) > 0 {
		rowCount = thriftColumnLength(operation.columns[0])
	}
	log.Printf("statement completed (%d columns, %d rows): %s", len(operation.schema.Columns), rowCount, compactSQL(req.Statement))
	return &cli_service.TExecuteStatementResp{
		Status:          successStatus(),
		OperationHandle: operation.handle,
	}, nil
}

func (s *proxyServer) getOperationStatus(_ context.Context, req *cli_service.TGetOperationStatusReq) (*cli_service.TGetOperationStatusResp, error) {
	log.Printf("GetOperationStatus %s", handleKey(req.OperationHandle.OperationId))
	operation, ok := s.operation(req.OperationHandle)
	if !ok {
		return &cli_service.TGetOperationStatusResp{Status: invalidHandleStatus()}, nil
	}
	state := cli_service.TOperationState_FINISHED_STATE
	hasResultSet := operation.handle.HasResultSet
	return &cli_service.TGetOperationStatusResp{
		Status:         successStatus(),
		OperationState: &state,
		HasResultSet:   &hasResultSet,
	}, nil
}

func (s *proxyServer) getResultSetMetadata(_ context.Context, req *cli_service.TGetResultSetMetadataReq) (*cli_service.TGetResultSetMetadataResp, error) {
	log.Printf("GetResultSetMetadata %s", handleKey(req.OperationHandle.OperationId))
	operation, ok := s.operation(req.OperationHandle)
	if !ok {
		return &cli_service.TGetResultSetMetadataResp{Status: invalidHandleStatus()}, nil
	}
	return &cli_service.TGetResultSetMetadataResp{
		Status: successStatus(),
		Schema: operation.schema,
	}, nil
}

func (s *proxyServer) fetchResults(_ context.Context, req *cli_service.TFetchResultsReq) (*cli_service.TFetchResultsResp, error) {
	log.Printf("FetchResults %s", handleKey(req.OperationHandle.OperationId))
	key := handleKey(req.OperationHandle.OperationId)
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.operations[key]
	if !ok {
		return &cli_service.TFetchResultsResp{Status: invalidHandleStatus()}, nil
	}

	columns := operation.columns
	if operation.fetched {
		columns = emptyColumnsLike(operation.columns)
	}
	operation.fetched = true
	hasMoreRows := false
	return &cli_service.TFetchResultsResp{
		Status:      successStatus(),
		HasMoreRows: &hasMoreRows,
		Results: &cli_service.TRowSet{
			StartRowOffset: 0,
			Rows:           []*cli_service.TRow{},
			Columns:        columns,
		},
	}, nil
}

func (s *proxyServer) cancelOperation(_ context.Context, req *cli_service.TCancelOperationReq) (*cli_service.TCancelOperationResp, error) {
	if _, ok := s.operation(req.OperationHandle); !ok {
		return &cli_service.TCancelOperationResp{Status: invalidHandleStatus()}, nil
	}
	return &cli_service.TCancelOperationResp{Status: successStatus()}, nil
}

func (s *proxyServer) closeOperation(_ context.Context, req *cli_service.TCloseOperationReq) (*cli_service.TCloseOperationResp, error) {
	log.Printf("CloseOperation %s", handleKey(req.OperationHandle.OperationId))
	key := handleKey(req.OperationHandle.OperationId)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.operations[key]; !ok {
		return &cli_service.TCloseOperationResp{Status: invalidHandleStatus()}, nil
	}
	delete(s.operations, key)
	return &cli_service.TCloseOperationResp{Status: successStatus()}, nil
}

func (s *proxyServer) operation(handle *cli_service.TOperationHandle) (*queryOperation, bool) {
	if handle == nil || handle.OperationId == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.operations[handleKey(handle.OperationId)]
	return operation, ok
}

func (s *proxyServer) runQuery(ctx context.Context, statement string, parameters []*cli_service.TSparkParameter) (*queryOperation, error) {
	normalized := strings.ToLower(strings.TrimSpace(statement))
	if strings.HasPrefix(normalized, "use ") {
		// Sling sends the Databricks shorthand `USE <schema>`. Sail accepts
		// `USE SCHEMA <schema>`, but Flight SQL sessions may be pooled, and all
		// matrix statements are explicitly qualified anyway. Treat it as a
		// successful connection hint instead of mutating one pooled session.
		return buildQueryOperation(nil)
	}
	if strings.Contains(normalized, "from information_schema.columns") {
		return s.runInformationSchemaColumns(ctx, statement)
	}
	if strings.Contains(normalized, "information_schema.tables") {
		return s.runInformationSchemaTables(ctx, statement)
	}
	if matches := renameTablePattern.FindStringSubmatch(statement); len(matches) == 3 {
		return s.replaceTable(ctx, matches[1], matches[2])
	}
	if matches := truncateTablePattern.FindStringSubmatch(statement); len(matches) == 2 {
		return s.rewriteTable(ctx, matches[1], "false")
	}
	if matches := deleteRowsPattern.FindStringSubmatch(statement); len(matches) == 3 {
		return s.rewriteTable(ctx, matches[1], "not coalesce(("+matches[2]+"), false)")
	}
	// Sail parses Delta table DDL, but its Flight SQL service currently uses a
	// relative default warehouse path that delta-rs rejects. Storage format is
	// outside this protocol/lifecycle test, so retain the Spark SQL table
	// semantics with Sail's Parquet provider.
	statement = usingDeltaPattern.ReplaceAllString(statement, "USING PARQUET")

	statement, err := renderStatementParameters(statement, parameters)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	columns := make([]resultColumn, len(names))
	for i, name := range names {
		kind, typeID := classifyColumn(types[i])
		columns[i] = resultColumn{name: name, kind: kind, typeID: typeID}
		if precision, scale, ok := types[i].DecimalSize(); ok {
			columns[i].decimalPrecision = int32(precision) //nolint:gosec -- Arrow decimal bounds fit int32.
			columns[i].decimalScale = int32(scale)         //nolint:gosec -- Arrow decimal bounds fit int32.
		}
	}

	for rows.Next() {
		values := make([]any, len(names))
		destinations := make([]any, len(names))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		for i, value := range values {
			columns[i].values = append(columns[i].values, value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.recordRelationMutation(statement)

	return buildQueryOperation(columns)
}

func (s *proxyServer) replaceTable(ctx context.Context, source, target string) (*queryOperation, error) {
	// Databricks' full-refresh materializer atomically renames its staging table.
	// Sail/DataFusion cannot rename tables yet, so reproduce the resulting table
	// state with a CTAS followed by dropping the staging table.
	for _, statement := range []string{
		"create table " + target + " as select * from " + source,
		"drop table " + source,
	} {
		rows, err := s.db.QueryContext(ctx, statement)
		if err != nil {
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	s.replaceRelation(source, target, "MANAGED")
	return buildQueryOperation(nil)
}

func (s *proxyServer) rewriteTable(ctx context.Context, target, keepPredicate string) (*queryOperation, error) {
	// Sail's Parquet provider cannot mutate rows. Rebuild the table with the
	// rows a Databricks TRUNCATE or DELETE would retain so the incremental
	// lifecycle can still cross the real Databricks client boundary.
	temporary, err := temporaryRelationName(target, "__renart_rewrite")
	if err != nil {
		return nil, err
	}
	for _, statement := range []string{
		"drop table if exists " + temporary,
		"create table " + temporary + " as select * from " + target + " where " + keepPredicate,
		"drop table " + target,
		"create table " + target + " as select * from " + temporary,
		"drop table " + temporary,
	} {
		rows, err := s.db.QueryContext(ctx, statement)
		if err != nil {
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	s.setRelationKind(target, "MANAGED")
	s.deleteRelation(temporary)
	return buildQueryOperation(nil)
}

func temporaryRelationName(relation, suffix string) (string, error) {
	if relation == "" || !renameTablePattern.MatchString("alter table "+relation+" rename to "+relation) {
		return "", fmt.Errorf("unsupported relation name %q", relation)
	}
	if strings.HasSuffix(relation, "`") {
		open := strings.LastIndex(relation[:len(relation)-1], "`")
		if open < 0 {
			return "", fmt.Errorf("unsupported quoted relation name %q", relation)
		}
		return relation[:len(relation)-1] + suffix + "`", nil
	}
	return relation + suffix, nil
}

func renderStatementParameters(statement string, parameters []*cli_service.TSparkParameter) (string, error) {
	if len(parameters) == 0 {
		return statement, nil
	}

	var output strings.Builder
	output.Grow(len(statement) + len(parameters)*8)
	parameterIndex := 0
	var singleQuoted, doubleQuoted, backtickQuoted, lineComment, blockComment bool
	for i := 0; i < len(statement); i++ {
		current := statement[i]
		var next byte
		if i+1 < len(statement) {
			next = statement[i+1]
		}

		if lineComment {
			output.WriteByte(current)
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			output.WriteByte(current)
			if current == '*' && next == '/' {
				output.WriteByte(next)
				i++
				blockComment = false
			}
			continue
		}
		if !singleQuoted && !doubleQuoted && !backtickQuoted {
			if current == '-' && next == '-' {
				output.WriteString("--")
				i++
				lineComment = true
				continue
			}
			if current == '/' && next == '*' {
				output.WriteString("/*")
				i++
				blockComment = true
				continue
			}
		}

		switch current {
		case '\'':
			if !doubleQuoted && !backtickQuoted {
				if singleQuoted && next == '\'' {
					output.WriteString("''")
					i++
					continue
				}
				singleQuoted = !singleQuoted
			}
		case '"':
			if !singleQuoted && !backtickQuoted {
				doubleQuoted = !doubleQuoted
			}
		case '`':
			if !singleQuoted && !doubleQuoted {
				backtickQuoted = !backtickQuoted
			}
		case '?':
			if !singleQuoted && !doubleQuoted && !backtickQuoted {
				if parameterIndex >= len(parameters) {
					return "", fmt.Errorf("statement contains more placeholders than parameters")
				}
				literal, err := sparkParameterLiteral(parameterIndex, parameters[parameterIndex])
				if err != nil {
					return "", err
				}
				output.WriteString(literal)
				parameterIndex++
				continue
			}
		}
		output.WriteByte(current)
	}
	if parameterIndex != len(parameters) {
		return "", fmt.Errorf("statement contains %d placeholders for %d parameters", parameterIndex, len(parameters))
	}
	return output.String(), nil
}

func sparkParameterLiteral(index int, parameter *cli_service.TSparkParameter) (string, error) {
	if parameter == nil || parameter.Value == nil {
		return "NULL", nil
	}
	value := parameter.Value
	switch {
	case value.BooleanValue != nil:
		return strings.ToUpper(strconv.FormatBool(*value.BooleanValue)), nil
	case value.DoubleValue != nil:
		return strconv.FormatFloat(*value.DoubleValue, 'g', -1, 64), nil
	case value.StringValue == nil:
		return "", fmt.Errorf("parameter %d has no supported value", index)
	}

	raw := *value.StringValue
	typeName := strings.ToUpper(parameter.GetType())
	switch {
	case strings.Contains(typeName, "INT"):
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return "", fmt.Errorf("parse parameter %d as %s: %w", index, typeName, err)
		}
		return strconv.FormatInt(parsed, 10), nil
	case strings.Contains(typeName, "FLOAT"), strings.Contains(typeName, "DOUBLE"), strings.Contains(typeName, "DECIMAL"):
		if !numericParameterPattern.MatchString(raw) {
			return "", fmt.Errorf("parse parameter %d as %s: invalid numeric literal %q", index, typeName, raw)
		}
		return raw, nil
	case strings.Contains(typeName, "BOOL"):
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return "", fmt.Errorf("parse parameter %d as %s: %w", index, typeName, err)
		}
		return strings.ToUpper(strconv.FormatBool(parsed)), nil
	default:
		return "'" + strings.ReplaceAll(raw, "'", "''") + "'", nil
	}
}

func (s *proxyServer) runInformationSchemaTables(ctx context.Context, statement string) (*queryOperation, error) {
	matches := informationSchemaColumnsPattern.FindStringSubmatch(statement)
	if len(matches) != 3 {
		return nil, fmt.Errorf("unsupported information_schema.tables query: %s", compactSQL(statement))
	}
	relation := matches[1] + "." + matches[2]
	rows, err := s.db.QueryContext(ctx, "describe table "+relation)
	if err != nil {
		if isMissingRelationError(err) {
			return buildInformationSchemaTables("")
		}
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	kind := s.relationKind(relation)
	if kind == "" {
		// A relation created outside the compatibility endpoint is still positive
		// evidence. Sail cannot expose its kind through information_schema, so the
		// faithful default for the matrix's CTAS targets is a managed table.
		kind = "MANAGED"
	}
	return buildInformationSchemaTables(kind)
}

func buildInformationSchemaTables(kind string) (*queryOperation, error) {
	column := resultColumn{
		name: "table_type", kind: columnString, typeID: cli_service.TTypeId_STRING_TYPE,
	}
	if kind != "" {
		column.values = append(column.values, kind)
	}
	return buildQueryOperation([]resultColumn{column})
}

func (s *proxyServer) runInformationSchemaColumns(ctx context.Context, statement string) (*queryOperation, error) {
	matches := informationSchemaColumnsPattern.FindStringSubmatch(statement)
	if len(matches) != 3 {
		return nil, fmt.Errorf("unsupported information_schema.columns query: %s", compactSQL(statement))
	}
	tableName := matches[1] + "." + matches[2]
	rows, err := s.db.QueryContext(ctx, "describe table "+tableName)
	if err != nil {
		if isMissingRelationError(err) {
			return buildInformationSchemaColumns(nil)
		}
		return nil, err
	}
	defer rows.Close()

	var described []describedColumn
	for rows.Next() {
		var name, dataType string
		var comment any
		if err := rows.Scan(&name, &dataType, &comment); err != nil {
			return nil, err
		}
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		described = append(described, describedColumn{name: name, dataType: dataType})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildInformationSchemaColumns(described)
}

func isMissingRelationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "table not found") ||
		strings.Contains(message, "table does not exist") ||
		strings.Contains(message, "table_or_view_not_found")
}

func (s *proxyServer) recordRelationMutation(statement string) {
	if matches := createRelationPattern.FindStringSubmatch(statement); len(matches) == 3 {
		kind := "MANAGED"
		if strings.EqualFold(matches[1], "view") {
			kind = "VIEW"
		}
		s.setRelationKind(matches[2], kind)
		return
	}
	if matches := dropRelationPattern.FindStringSubmatch(statement); len(matches) == 2 {
		s.deleteRelation(matches[1])
	}
}

func (s *proxyServer) relationKind(relation string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.relations[relationKey(relation)]
}

func (s *proxyServer) setRelationKind(relation, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.relations[relationKey(relation)] = kind
}

func (s *proxyServer) deleteRelation(relation string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.relations, relationKey(relation))
}

func (s *proxyServer) replaceRelation(source, target, fallbackKind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sourceKey := relationKey(source)
	kind := s.relations[sourceKey]
	if kind == "" {
		kind = fallbackKind
	}
	delete(s.relations, sourceKey)
	s.relations[relationKey(target)] = kind
}

func relationKey(relation string) string {
	parts := strings.Split(strings.TrimSpace(relation), ".")
	for index := range parts {
		parts[index] = strings.ToLower(strings.TrimSpace(strings.Trim(parts[index], "`\"[]")))
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, ".")
}

func buildInformationSchemaColumns(described []describedColumn) (*queryOperation, error) {
	columns := []resultColumn{
		{name: "column_name", kind: columnString, typeID: cli_service.TTypeId_STRING_TYPE},
		{name: "data_type", kind: columnString, typeID: cli_service.TTypeId_STRING_TYPE},
		{name: "maximum_length", kind: columnI64, typeID: cli_service.TTypeId_BIGINT_TYPE},
		{name: "precision", kind: columnI64, typeID: cli_service.TTypeId_BIGINT_TYPE},
		{name: "scale", kind: columnI64, typeID: cli_service.TTypeId_BIGINT_TYPE},
	}
	for _, column := range described {
		columns[0].values = append(columns[0].values, column.name)
		columns[1].values = append(columns[1].values, column.dataType)
		columns[2].values = append(columns[2].values, nil)
		columns[3].values = append(columns[3].values, nil)
		columns[4].values = append(columns[4].values, nil)
	}
	return buildQueryOperation(columns)
}

func buildQueryOperation(columns []resultColumn) (*queryOperation, error) {
	schema := &cli_service.TTableSchema{Columns: make([]*cli_service.TColumnDesc, len(columns))}
	resultColumns := make([]*cli_service.TColumn, len(columns))
	for i := range columns {
		primitiveEntry := &cli_service.TPrimitiveTypeEntry{Type: columns[i].typeID}
		if columns[i].typeID == cli_service.TTypeId_DECIMAL_TYPE {
			precision := columns[i].decimalPrecision
			scale := columns[i].decimalScale
			primitiveEntry.TypeQualifiers = &cli_service.TTypeQualifiers{Qualifiers: map[string]*cli_service.TTypeQualifierValue{
				"precision": {I32Value: &precision},
				"scale":     {I32Value: &scale},
			}}
		}
		schema.Columns[i] = &cli_service.TColumnDesc{
			ColumnName: columns[i].name,
			TypeDesc: &cli_service.TTypeDesc{Types: []*cli_service.TTypeEntry{{
				PrimitiveEntry: primitiveEntry,
			}}},
			Position: int32(i), //nolint:gosec -- E2E result sets are intentionally tiny.
		}
		encoded, err := columns[i].thriftColumn()
		if err != nil {
			return nil, fmt.Errorf("encode result column %q: %w", columns[i].name, err)
		}
		resultColumns[i] = encoded
	}

	handle := &cli_service.TOperationHandle{
		OperationId:   newHandleIdentifier(),
		OperationType: cli_service.TOperationType_EXECUTE_STATEMENT,
		HasResultSet:  len(columns) > 0,
	}
	return &queryOperation{handle: handle, schema: schema, columns: resultColumns}, nil
}

func classifyColumn(columnType *sql.ColumnType) (columnKind, cli_service.TTypeId) {
	typeName := strings.ToUpper(columnType.DatabaseTypeName())
	switch {
	case strings.Contains(typeName, "BOOL"):
		return columnBool, cli_service.TTypeId_BOOLEAN_TYPE
	case strings.Contains(typeName, "TINYINT"):
		return columnI8, cli_service.TTypeId_TINYINT_TYPE
	case strings.Contains(typeName, "SMALLINT"):
		return columnI16, cli_service.TTypeId_SMALLINT_TYPE
	case strings.Contains(typeName, "BIGINT"), strings.Contains(typeName, "INT64"), typeName == "LONG":
		return columnI64, cli_service.TTypeId_BIGINT_TYPE
	case strings.Contains(typeName, "INT"):
		return columnI32, cli_service.TTypeId_INT_TYPE
	case strings.Contains(typeName, "DOUBLE"), strings.Contains(typeName, "FLOAT64"):
		return columnDouble, cli_service.TTypeId_DOUBLE_TYPE
	case strings.Contains(typeName, "FLOAT"), strings.Contains(typeName, "REAL"):
		return columnDouble, cli_service.TTypeId_FLOAT_TYPE
	case strings.Contains(typeName, "DECIMAL"), strings.Contains(typeName, "NUMERIC"):
		return columnString, cli_service.TTypeId_DECIMAL_TYPE
	case strings.Contains(typeName, "TIMESTAMP"):
		return columnString, cli_service.TTypeId_TIMESTAMP_TYPE
	case strings.Contains(typeName, "DATE"):
		return columnString, cli_service.TTypeId_DATE_TYPE
	case strings.Contains(typeName, "BINARY"):
		return columnBinary, cli_service.TTypeId_BINARY_TYPE
	default:
		return columnString, cli_service.TTypeId_STRING_TYPE
	}
}

func (c resultColumn) thriftColumn() (*cli_service.TColumn, error) {
	nulls := make([]byte, (len(c.values)+7)/8)
	markNull := func(index int) {
		nulls[index/8] |= 1 << uint(index%8)
	}

	switch c.kind {
	case columnBool:
		values := make([]bool, len(c.values))
		for i, value := range c.values {
			if value == nil {
				markNull(i)
				continue
			}
			parsed, err := strconv.ParseBool(fmt.Sprint(value))
			if err != nil {
				return nil, err
			}
			values[i] = parsed
		}
		return &cli_service.TColumn{BoolVal: &cli_service.TBoolColumn{Values: values, Nulls: nulls}}, nil
	case columnI8:
		values := make([]int8, len(c.values))
		for i, value := range c.values {
			if value == nil {
				markNull(i)
				continue
			}
			parsed, err := strconv.ParseInt(fmt.Sprint(value), 10, 8)
			if err != nil {
				return nil, err
			}
			values[i] = int8(parsed)
		}
		return &cli_service.TColumn{ByteVal: &cli_service.TByteColumn{Values: values, Nulls: nulls}}, nil
	case columnI16:
		values := make([]int16, len(c.values))
		for i, value := range c.values {
			if value == nil {
				markNull(i)
				continue
			}
			parsed, err := strconv.ParseInt(fmt.Sprint(value), 10, 16)
			if err != nil {
				return nil, err
			}
			values[i] = int16(parsed)
		}
		return &cli_service.TColumn{I16Val: &cli_service.TI16Column{Values: values, Nulls: nulls}}, nil
	case columnI32:
		values := make([]int32, len(c.values))
		for i, value := range c.values {
			if value == nil {
				markNull(i)
				continue
			}
			parsed, err := strconv.ParseInt(fmt.Sprint(value), 10, 32)
			if err != nil {
				return nil, err
			}
			values[i] = int32(parsed)
		}
		return &cli_service.TColumn{I32Val: &cli_service.TI32Column{Values: values, Nulls: nulls}}, nil
	case columnI64:
		values := make([]int64, len(c.values))
		for i, value := range c.values {
			if value == nil {
				markNull(i)
				continue
			}
			parsed, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
			if err != nil {
				return nil, err
			}
			values[i] = parsed
		}
		return &cli_service.TColumn{I64Val: &cli_service.TI64Column{Values: values, Nulls: nulls}}, nil
	case columnDouble:
		values := make([]float64, len(c.values))
		for i, value := range c.values {
			if value == nil {
				markNull(i)
				continue
			}
			parsed, err := strconv.ParseFloat(fmt.Sprint(value), 64)
			if err != nil {
				return nil, err
			}
			values[i] = parsed
		}
		return &cli_service.TColumn{DoubleVal: &cli_service.TDoubleColumn{Values: values, Nulls: nulls}}, nil
	case columnBinary:
		values := make([][]byte, len(c.values))
		for i, value := range c.values {
			if value == nil {
				markNull(i)
				continue
			}
			switch typed := value.(type) {
			case []byte:
				values[i] = typed
			default:
				values[i] = []byte(fmt.Sprint(value))
			}
		}
		return &cli_service.TColumn{BinaryVal: &cli_service.TBinaryColumn{Values: values, Nulls: nulls}}, nil
	default:
		values := make([]string, len(c.values))
		for i, value := range c.values {
			if value == nil {
				markNull(i)
				continue
			}
			switch typed := value.(type) {
			case time.Time:
				if c.typeID == cli_service.TTypeId_DATE_TYPE {
					values[i] = typed.Format("2006-01-02")
				} else {
					values[i] = typed.Format("2006-01-02 15:04:05.999999999")
				}
			case []byte:
				values[i] = string(typed)
			case decimal128.Num:
				values[i] = typed.ToString(c.decimalScale)
			case decimal256.Num:
				values[i] = typed.ToString(c.decimalScale)
			default:
				values[i] = fmt.Sprint(value)
			}
		}
		return &cli_service.TColumn{StringVal: &cli_service.TStringColumn{Values: values, Nulls: nulls}}, nil
	}
}

func emptyColumnsLike(columns []*cli_service.TColumn) []*cli_service.TColumn {
	empty := make([]*cli_service.TColumn, len(columns))
	for i, column := range columns {
		switch {
		case column.BoolVal != nil:
			empty[i] = &cli_service.TColumn{BoolVal: &cli_service.TBoolColumn{Values: []bool{}, Nulls: []byte{}}}
		case column.ByteVal != nil:
			empty[i] = &cli_service.TColumn{ByteVal: &cli_service.TByteColumn{Values: []int8{}, Nulls: []byte{}}}
		case column.I16Val != nil:
			empty[i] = &cli_service.TColumn{I16Val: &cli_service.TI16Column{Values: []int16{}, Nulls: []byte{}}}
		case column.I32Val != nil:
			empty[i] = &cli_service.TColumn{I32Val: &cli_service.TI32Column{Values: []int32{}, Nulls: []byte{}}}
		case column.I64Val != nil:
			empty[i] = &cli_service.TColumn{I64Val: &cli_service.TI64Column{Values: []int64{}, Nulls: []byte{}}}
		case column.DoubleVal != nil:
			empty[i] = &cli_service.TColumn{DoubleVal: &cli_service.TDoubleColumn{Values: []float64{}, Nulls: []byte{}}}
		case column.BinaryVal != nil:
			empty[i] = &cli_service.TColumn{BinaryVal: &cli_service.TBinaryColumn{Values: [][]byte{}, Nulls: []byte{}}}
		default:
			empty[i] = &cli_service.TColumn{StringVal: &cli_service.TStringColumn{Values: []string{}, Nulls: []byte{}}}
		}
	}
	return empty
}

func thriftColumnLength(column *cli_service.TColumn) int {
	switch {
	case column == nil:
		return 0
	case column.BoolVal != nil:
		return len(column.BoolVal.Values)
	case column.ByteVal != nil:
		return len(column.ByteVal.Values)
	case column.I16Val != nil:
		return len(column.I16Val.Values)
	case column.I32Val != nil:
		return len(column.I32Val.Values)
	case column.I64Val != nil:
		return len(column.I64Val.Values)
	case column.DoubleVal != nil:
		return len(column.DoubleVal.Values)
	case column.BinaryVal != nil:
		return len(column.BinaryVal.Values)
	case column.StringVal != nil:
		return len(column.StringVal.Values)
	default:
		return 0
	}
}

func successStatus() *cli_service.TStatus {
	return &cli_service.TStatus{StatusCode: cli_service.TStatusCode_SUCCESS_STATUS}
}

func errorStatus(err error) *cli_service.TStatus {
	message := err.Error()
	return &cli_service.TStatus{
		StatusCode:   cli_service.TStatusCode_ERROR_STATUS,
		ErrorMessage: &message,
	}
}

func invalidHandleStatus() *cli_service.TStatus {
	message := "unknown operation handle"
	return &cli_service.TStatus{
		StatusCode:   cli_service.TStatusCode_INVALID_HANDLE_STATUS,
		ErrorMessage: &message,
	}
}

func newHandleIdentifier() *cli_service.THandleIdentifier {
	guid := make([]byte, 16)
	secret := make([]byte, 16)
	if _, err := rand.Read(guid); err != nil {
		panic(err)
	}
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	return &cli_service.THandleIdentifier{GUID: guid, Secret: secret}
}

func handleKey(identifier *cli_service.THandleIdentifier) string {
	if identifier == nil {
		return ""
	}
	return hex.EncodeToString(identifier.GUID)
}

func compactSQL(statement string) string {
	return strings.Join(strings.Fields(statement), " ")
}

func writeTestCertificates(directory string) (string, string, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", "", err
	}

	now := time.Now()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Renart Databricks E2E CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		return "", "", err
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCertificate, &serverKey.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}

	caPath := filepath.Join(directory, "ca.pem")
	certPath := filepath.Join(directory, "server.pem")
	keyPath := filepath.Join(directory, "server-key.pem")
	if err := writePEM(caPath, 0o644, "CERTIFICATE", caDER); err != nil {
		return "", "", err
	}
	if err := writePEM(certPath, 0o644, "CERTIFICATE", serverDER); err != nil {
		return "", "", err
	}
	if err := writePEM(keyPath, 0o600, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey)); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func writePEM(path string, mode os.FileMode, blockType string, data []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: data}), mode)
}
