package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"go.uber.org/zap"

	"renart/internal/web/notebook"
)

// notebookLogCap bounds captured Python stdout/stderr so a runaway print loop
// cannot balloon a run response.
const notebookLogCap = 256 * 1024

// boundedLogBuffer is a size-capped, concurrency-safe io.Writer. bruin streams
// a Python cell's stdout and stderr from two goroutines into the same writer,
// so writes must be serialized.
type boundedLogBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func (b *boundedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if remaining := notebookLogCap - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
			b.truncated = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

// notebookANSIPattern matches ANSI/VT100 control sequences (uv and rich-style
// tracebacks colorize their output) so they don't render as garbage in the UI.
var notebookANSIPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func (b *boundedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	cleaned := notebookANSIPattern.ReplaceAllString(b.buf.String(), "")
	// bruin prefixes every captured line with ">> "; strip it so the notebook
	// shows the program's actual output.
	lines := strings.Split(cleaned, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, ">> ")
	}
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if b.truncated {
		out += "\n… output truncated …"
	}
	return out
}

// materializePythonCell runs a Python cell's materialize() through the Renart
// Python operator, writing its result to parquetPath. SDK queries are delegated
// to runQuery, which targets the runner's already-open notebook session.
func (s *NotebookService) materializePythonCell(
	ctx context.Context,
	cell *notebook.Cell,
	parquetPath string,
	runQuery notebook.PythonQueryFunc,
	parameterValues map[string]any,
) (notebook.PythonMaterializationOutput, error) {
	notebookDir := filepath.Dir(cell.Path)

	cfg := &config.Config{
		SelectedEnvironmentName: "default",
		SelectedEnvironment:     &config.Environment{},
		Environments:            map[string]config.Environment{"default": {}},
	}

	// Clone the cell asset for the run: the destination table is the asset name
	// (bruin passes it to ingestr as --dest-table), materialized into the
	// session connection.
	runAsset := *cell.Asset
	runAsset.Name = cell.Asset.Name
	runAsset.Type = pipeline.AssetTypePython
	runAsset.Connection = notebook.NotebookConnectionName
	runAsset.Materialization = pipeline.Materialization{Type: pipeline.MaterializationTypeTable}
	runAsset.ExecutableFile = pipeline.ExecutableFile{
		Name:    filepath.Base(cell.Path),
		Path:    cell.Path,
		Content: cell.Asset.ExecutableFile.Content,
	}

	runPipeline := &pipeline.Pipeline{
		Name:      "renart-notebook",
		Assets:    []*pipeline.Asset{&runAsset},
		Variables: notebookPythonVariables(parameterValues),
	}

	// The Python operator reads the run window (start/end/execution date),
	// run id, and environment from the context, like the asset run path.
	now := time.Now().UTC()
	timeWindow, err := ResolveExecutionTimeWindow("", "", "", now)
	if err != nil {
		return notebook.PythonMaterializationOutput{}, fmt.Errorf("failed to resolve the run window: %w", err)
	}
	logs := &boundedLogBuffer{}
	runCtx := context.WithValue(ctx, pipeline.RunConfigFullRefresh, false)
	// Without interval modifiers, bruin preserves the environment we pass.
	runCtx = context.WithValue(runCtx, pipeline.RunConfigApplyIntervalModifiers, false)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigStartDate, timeWindow.Start)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigEndDate, timeWindow.End)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigExecutionDate, now)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigRunID, newRenartRunID())
	runCtx = context.WithValue(runCtx, config.EnvironmentContextKey, cfg.SelectedEnvironment)
	runCtx = context.WithValue(runCtx, config.EnvironmentNameContextKey, cfg.SelectedEnvironmentName)
	runCtx = context.WithValue(runCtx, bruinexecutor.ContextLogger, zap.NewNop().Sugar())
	// Capture the cell's stdout/stderr instead of letting bruin write it to the
	// server's os.Stdout, so it can be returned and shown next to the cell.
	runCtx = context.WithValue(runCtx, bruinexecutor.KeyPrinter, io.Writer(logs))

	readyPath := filepath.Join(filepath.Dir(parquetPath), "python-ready")
	envVariables := map[string]string{
		// Keep uv's project virtualenv out of the notebook folder (which is a
		// git-tracked folder of cells) by placing it under .renart.
		"UV_PROJECT_ENVIRONMENT":   notebookVenvDir(s.deps.WorkspaceRoot, notebookDir),
		"RENART_PYTHON_READY_FILE": readyPath,
	}
	operator := newRenartPythonOperator(nil, envVariables, renartPythonOperatorOptions{
		enableBroker:            true,
		brokerDefaultConnection: notebook.NotebookConnectionName,
		brokerRunQuery:          runQuery,
		brokerValidateSQL:       s.validateNotebookPythonQuery,
		brokerUsedTables: func(sql string) ([]string, error) {
			return s.usedTables(sql, notebook.DefaultCellType)
		},
		stagingOutputPath: parquetPath,
	})
	startedAt := time.Now()
	runErr := operator.RunTask(runCtx, runPipeline, &runAsset)
	output := notebook.PythonMaterializationOutput{Logs: logs.String()}
	if runErr == nil {
		environmentFingerprint, fingerprintErr := notebook.PythonEnvironmentFingerprint(
			afero.NewOsFs(), notebookDir, s.deps.WorkspaceRoot,
		)
		if fingerprintErr != nil {
			return output, fmt.Errorf("fingerprint the Python environment after execution: %w", fingerprintErr)
		}
		output.EnvironmentFingerprint = environmentFingerprint
	}
	if info, statErr := os.Stat(parquetPath); statErr == nil && !info.IsDir() {
		output.TransferBytes = info.Size()
	}
	if info, statErr := os.Stat(readyPath); statErr == nil {
		startup := info.ModTime().Sub(startedAt)
		if startup > 0 {
			output.PythonStartupMS = float64(startup) / float64(time.Millisecond)
		}
	}
	return output, runErr
}

func notebookPythonVariables(values map[string]any) pipeline.Variables {
	variables := make(pipeline.Variables, len(values))
	for name, value := range values {
		variableType := "string"
		switch value.(type) {
		case bool:
			variableType = "boolean"
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			variableType = "number"
		case []any, []string:
			variableType = "array"
		}
		variables[name] = map[string]any{"type": variableType, "default": cloneJSONValue(value)}
	}
	return variables
}
