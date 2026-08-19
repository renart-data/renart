package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"renart/internal/web/notebook"
)

const (
	defaultNotebookSnapshotMaxBytes = int64(2 << 30)
	defaultNotebookSnapshotTimeout  = 30 * time.Minute
)

type warehouseSQLSourceExecutor struct {
	transfer notebook.NotebookTransferService
	validate func(sqlText, assetType string) error
}

func (executor *warehouseSQLSourceExecutor) Analyze(_ context.Context, input notebook.AnalyzeBlockInput) (notebook.BlockAnalysis, error) {
	if input.Cell == nil || input.Cell.Asset == nil {
		return notebook.BlockAnalysis{}, fmt.Errorf("notebook source cell is required")
	}
	connection := strings.TrimSpace(input.Cell.Asset.Connection)
	if connection == "" {
		return notebook.BlockAnalysis{}, fmt.Errorf("warehouse source cell requires a source connection")
	}
	dialect, err := sqlparser.AssetTypeToDialect(input.Cell.Asset.Type)
	if err != nil {
		return notebook.BlockAnalysis{}, err
	}
	return notebook.BlockAnalysis{
		Kind: "warehouse_sql", Dialect: dialect, Connection: connection,
	}, nil
}

func (executor *warehouseSQLSourceExecutor) Execute(ctx context.Context, input notebook.ExecuteBlockInput) (notebook.BlockOutput, error) {
	analysis, err := executor.Analyze(ctx, notebook.AnalyzeBlockInput{
		Notebook: input.Notebook, Cell: input.Cell, Environment: input.Environment,
	})
	if err != nil {
		return notebook.BlockOutput{}, err
	}
	query := strings.TrimSpace(input.SQL)
	if query == "" {
		return notebook.BlockOutput{}, fmt.Errorf("warehouse source query is empty")
	}
	if executor.validate != nil {
		if err := executor.validate(query, string(input.Cell.Asset.Type)); err != nil {
			return notebook.BlockOutput{}, err
		}
	}
	if executor.transfer == nil {
		return notebook.BlockOutput{}, fmt.Errorf("notebook snapshot transfer is unavailable")
	}
	mode, rowLimit, err := notebook.SourceSnapshotPolicy(input.Cell)
	if err != nil {
		return notebook.BlockOutput{}, err
	}
	artifact, err := executor.transfer.Snapshot(ctx, notebook.SnapshotRequest{
		NotebookID: input.Notebook.UUID, BlockID: input.Cell.ID,
		Environment: input.Environment, Connection: analysis.Connection,
		Query: query, Mode: mode, RowLimit: rowLimit,
		DefinitionFingerprint: notebook.CellFingerprintWithParameters(input.Notebook, input.Cell, input.ParameterValues),
	})
	if err != nil {
		return notebook.BlockOutput{}, err
	}
	cleanup := artifact.Cleanup
	artifact.Cleanup = nil
	return notebook.BlockOutput{Artifact: &artifact, Cleanup: cleanup}, nil
}

type slingNotebookTransferService struct {
	workspaceRoot        string
	configPath           string
	newConnectionManager func(context.Context, string) (config.ConnectionAndDetailsGetter, error)
	maxBytes             int64
	timeout              time.Duration
}

func (service *slingNotebookTransferService) Snapshot(ctx context.Context, request notebook.SnapshotRequest) (notebook.TabularArtifact, error) {
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "sample" {
		return notebook.TabularArtifact{}, fmt.Errorf("notebook snapshot mode must be full or sample")
	}
	query := strings.TrimRight(strings.TrimSpace(request.Query), ";")
	if query == "" {
		return notebook.TabularArtifact{}, fmt.Errorf("notebook snapshot query is empty")
	}
	if mode == "sample" {
		if request.RowLimit <= 0 {
			return notebook.TabularArtifact{}, fmt.Errorf("sample snapshots require a positive row limit")
		}
		query = fmt.Sprintf("select * from (\n%s\n) as __renart_sample limit %d", query, request.RowLimit)
	}

	config, err := loadSelectedConfig(service.configPath, request.Environment)
	if err != nil {
		return notebook.TabularArtifact{}, fmt.Errorf("load notebook source environment: %w", err)
	}
	if service.newConnectionManager == nil {
		return notebook.TabularArtifact{}, fmt.Errorf("notebook source connection manager is unavailable")
	}
	manager, err := service.newConnectionManager(ctx, config.SelectedEnvironmentName)
	if err != nil {
		return notebook.TabularArtifact{}, fmt.Errorf("resolve notebook source environment: %w", err)
	}
	connectionURI, warning, err := loadConnectionURIWithWarning(manager, request.Connection)
	if err != nil {
		return notebook.TabularArtifact{}, err
	}

	warnings := make([]string, 0, 1)
	if warning != "" {
		warnings = append(warnings, warning)
	}
	return service.snapshotFromSling(ctx, mode, notebook.SnapshotProvenance{
		SourceKind: "warehouse_sql", Environment: config.SelectedEnvironmentName,
		Connection: request.Connection, DefinitionFingerprint: request.DefinitionFingerprint,
		CreatedAt: time.Now().UTC(), Warnings: warnings,
	}, func(_ context.Context, stagingDir string, _ io.Writer) (notebookSnapshotSource, error) {
		queryPath := filepath.Join(stagingDir, "query.sql")
		if err := os.WriteFile(queryPath, []byte(query+"\n"), 0o600); err != nil {
			return notebookSnapshotSource{}, err
		}
		return notebookSnapshotSource{SQL: &notebookSnapshotSQLSource{
			ConnectionURI: connectionURI,
			QueryPath:     queryPath,
		}}, nil
	})
}

type notebookSnapshotSQLSource struct {
	ConnectionURI string
	QueryPath     string
}

type notebookSnapshotSource struct {
	Args []string
	Env  []string
	SQL  *notebookSnapshotSQLSource
}

type notebookSnapshotSourceBuilder func(ctx context.Context, stagingDir string, output io.Writer) (notebookSnapshotSource, error)

type notebookSlingReplication struct {
	Source   string                                    `json:"source"`
	Target   string                                    `json:"target"`
	Defaults notebookSlingReplicationDefaults          `json:"defaults"`
	Streams  map[string]notebookSlingReplicationStream `json:"streams"`
}

type notebookSlingReplicationDefaults struct {
	Mode string `json:"mode"`
}

type notebookSlingReplicationStream struct {
	SQL           string                             `json:"sql"`
	Object        string                             `json:"object"`
	TargetOptions notebookSlingReplicationTargetOpts `json:"target_options"`
}

type notebookSlingReplicationTargetOpts struct {
	Format string `json:"format"`
}

// snapshotFromSling is the single bounded source-to-Parquet path for warehouse
// SQL, file/object, and HTTP notebook sources. All callers inherit the same
// private staging directory, process limiter, credential-to-environment
// rewrite, cancellation, size budget, and typed artifact inspection.
func (service *slingNotebookTransferService) snapshotFromSling(
	ctx context.Context,
	mode string,
	provenance notebook.SnapshotProvenance,
	buildSource notebookSnapshotSourceBuilder,
) (notebook.TabularArtifact, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != notebook.SnapshotModeFull && mode != notebook.SnapshotModeSample {
		return notebook.TabularArtifact{}, fmt.Errorf("notebook snapshot mode must be full or sample")
	}
	stagingRoot := filepath.Join(service.workspaceRoot, ".renart", "notebook-transfers")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return notebook.TabularArtifact{}, err
	}
	stagingDir, err := os.MkdirTemp(stagingRoot, "snapshot-")
	if err != nil {
		return notebook.TabularArtifact{}, err
	}
	cleanup := func() error { return os.RemoveAll(stagingDir) }
	if err := os.Chmod(stagingDir, 0o700); err != nil {
		_ = cleanup()
		return notebook.TabularArtifact{}, err
	}
	parquetPath := filepath.Join(stagingDir, "snapshot.parquet")
	writer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil)}
	runTimeout := service.timeout
	if runTimeout <= 0 {
		runTimeout = defaultNotebookSnapshotTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	if buildSource == nil {
		_ = cleanup()
		return notebook.TabularArtifact{}, fmt.Errorf("notebook snapshot source builder is required")
	}
	source, err := buildSource(runCtx, stagingDir, writer)
	if err != nil {
		_ = cleanup()
		return notebook.TabularArtifact{}, err
	}
	args, connectionEnv, err := notebookSlingSnapshotArgs(stagingDir, parquetPath, source)
	if err != nil {
		_ = cleanup()
		return notebook.TabularArtifact{}, err
	}
	commandName, commandArgs, err := loadCommand(runCtx, args, writer)
	if err != nil {
		_ = cleanup()
		return notebook.TabularArtifact{}, err
	}
	command := newStreamingCommand(runCtx, commandName, commandArgs, service.workspaceRoot, writer)
	command.Env = append(command.Env, connectionEnv...)
	command.Env = append(command.Env, source.Env...)
	command.Env = append(command.Env, "SLING_ALLOW_EMPTY=true")

	maxBytes := service.maxBytes
	if maxBytes <= 0 {
		maxBytes = defaultNotebookSnapshotMaxBytes
	}
	var exceeded atomic.Bool
	monitorDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-monitorDone:
				return
			case <-ticker.C:
				if info, statErr := os.Stat(parquetPath); statErr == nil && info.Size() > maxBytes {
					exceeded.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	runErr := runStreamingCommand(runCtx, command, writer)
	close(monitorDone)
	if runErr != nil {
		output := strings.TrimSpace(writer.buffer.String())
		_ = cleanup()
		if exceeded.Load() {
			return notebook.TabularArtifact{}, fmt.Errorf("notebook snapshot exceeded the %d-byte transfer limit", maxBytes)
		}
		if runCtx.Err() != nil {
			return notebook.TabularArtifact{}, fmt.Errorf("notebook snapshot cancelled: %w", runCtx.Err())
		}
		if len(output) > 4096 {
			output = output[len(output)-4096:]
		}
		if output != "" {
			return notebook.TabularArtifact{}, fmt.Errorf("Sling notebook snapshot failed: %w: %s", runErr, output)
		}
		return notebook.TabularArtifact{}, fmt.Errorf("Sling notebook snapshot failed: %w", runErr)
	}
	if info, err := os.Stat(parquetPath); err != nil {
		_ = cleanup()
		return notebook.TabularArtifact{}, fmt.Errorf("Sling did not produce a notebook snapshot: %w", err)
	} else if info.Size() > maxBytes {
		_ = cleanup()
		return notebook.TabularArtifact{}, fmt.Errorf("notebook snapshot exceeded the %d-byte transfer limit", maxBytes)
	}
	artifact, err := notebook.InspectParquetArtifact(runCtx, parquetPath, provenance, mode == notebook.SnapshotModeFull, mode == notebook.SnapshotModeSample)
	if err != nil {
		_ = cleanup()
		return notebook.TabularArtifact{}, err
	}
	artifact.Cleanup = cleanup
	return artifact, nil
}

func notebookSlingSnapshotArgs(stagingDir, parquetPath string, source notebookSnapshotSource) ([]string, []string, error) {
	if source.SQL == nil {
		args := append([]string{"run"}, source.Args...)
		args = append(args,
			"--tgt-object", notebookSnapshotFileURI(parquetPath),
			"--tgt-options", `{"format":"parquet"}`,
		)
		args, connectionEnv := slingCommandConnectionEnv(args)
		return args, connectionEnv, nil
	}
	if len(source.Args) != 0 {
		return nil, nil, fmt.Errorf("notebook SQL snapshot cannot also define Sling source arguments")
	}
	connectionURI := strings.TrimSpace(source.SQL.ConnectionURI)
	queryPath := strings.TrimSpace(source.SQL.QueryPath)
	if connectionURI == "" || queryPath == "" {
		return nil, nil, fmt.Errorf("notebook SQL snapshot requires a connection and query file")
	}

	// Sling's flag form overloads --src-stream for both data files and SQL
	// files. In Sling 1.5.x that auto-detection is non-deterministic: the same
	// file://.../query.sql is sometimes opened as a data path. The replication
	// schema has an explicit `sql` field, which removes that ambiguity while
	// keeping both the query and credentials off the process command line.
	replication := notebookSlingReplication{
		Source:   slingSourceConnectionEnv,
		Target:   "LOCAL",
		Defaults: notebookSlingReplicationDefaults{Mode: "full-refresh"},
		Streams: map[string]notebookSlingReplicationStream{
			"renart_notebook_snapshot": {
				SQL:    notebookSnapshotFileURI(queryPath),
				Object: notebookSnapshotFileURI(parquetPath),
				TargetOptions: notebookSlingReplicationTargetOpts{
					Format: "parquet",
				},
			},
		},
	}
	payload, err := json.Marshal(replication)
	if err != nil {
		return nil, nil, fmt.Errorf("encode notebook Sling replication: %w", err)
	}
	replicationPath := filepath.Join(stagingDir, "replication.json")
	if err := os.WriteFile(replicationPath, payload, 0o600); err != nil {
		return nil, nil, fmt.Errorf("write notebook Sling replication: %w", err)
	}
	return []string{"run", "--replication", replicationPath}, []string{
		slingSourceConnectionEnv + "=" + connectionURI,
	}, nil
}

func notebookSnapshotFileURI(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
