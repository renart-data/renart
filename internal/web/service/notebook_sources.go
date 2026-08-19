package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/jinja"
	"renart/internal/web/notebook"
)

type notebookSourceExecutor struct {
	transfer *slingNotebookTransferService
	renderer *jinja.Renderer
}

func (executor *notebookSourceExecutor) Analyze(_ context.Context, input notebook.AnalyzeBlockInput) (notebook.BlockAnalysis, error) {
	if input.Cell == nil || input.Cell.Source == nil {
		return notebook.BlockAnalysis{}, fmt.Errorf("notebook source definition is required")
	}
	if err := notebook.ValidateSourceDefinition(input.Cell.Source); err != nil {
		return notebook.BlockAnalysis{}, err
	}
	kind := input.Cell.Source.Kind
	if kind == notebook.SourceKindFile && strings.TrimSpace(input.Cell.Source.Connection) != "" {
		kind = "object"
	}
	return notebook.BlockAnalysis{
		Kind: kind, Connection: strings.TrimSpace(input.Cell.Source.Connection),
	}, nil
}

func (executor *notebookSourceExecutor) Execute(ctx context.Context, input notebook.ExecuteBlockInput) (notebook.BlockOutput, error) {
	analysis, err := executor.Analyze(ctx, notebook.AnalyzeBlockInput{
		Notebook: input.Notebook, Cell: input.Cell, Environment: input.Environment,
	})
	if err != nil {
		return notebook.BlockOutput{}, err
	}
	if executor.transfer == nil {
		return notebook.BlockOutput{}, fmt.Errorf("notebook source snapshot transfer is unavailable")
	}
	mode, rowLimit, err := notebook.SourceSnapshotPolicy(input.Cell)
	if err != nil {
		return notebook.BlockOutput{}, err
	}
	definitionFingerprint := notebook.CellFingerprintWithParameters(input.Notebook, input.Cell, input.ParameterValues)
	switch input.Cell.Source.Kind {
	case notebook.SourceKindFile:
		return executor.executeFile(ctx, input, analysis, mode, rowLimit, definitionFingerprint)
	case notebook.SourceKindHTTP:
		return executor.executeHTTP(ctx, input, mode, rowLimit, definitionFingerprint)
	default:
		return notebook.BlockOutput{}, fmt.Errorf("unsupported notebook source kind %q", input.Cell.Source.Kind)
	}
}

func (executor *notebookSourceExecutor) executeFile(
	ctx context.Context,
	input notebook.ExecuteBlockInput,
	analysis notebook.BlockAnalysis,
	mode string,
	rowLimit int64,
	definitionFingerprint string,
) (notebook.BlockOutput, error) {
	definition := input.Cell.Source
	renderedURI := definition.URI
	if executor.renderer != nil {
		var renderErr error
		renderedURI, renderErr = executor.renderer.Render(definition.URI)
		if renderErr != nil {
			return notebook.BlockOutput{}, fmt.Errorf("render notebook source uri: %w", renderErr)
		}
	}
	formatPath := renderedURI
	if parsed, err := url.Parse(renderedURI); err == nil && parsed.Path != "" {
		formatPath = parsed.Path
	}
	format, err := slingSeedFormat(definition.Format, formatPath)
	if err != nil {
		return notebook.BlockOutput{}, err
	}
	encodedOptions, err := json.Marshal(map[string]string{"format": format})
	if err != nil {
		return notebook.BlockOutput{}, err
	}
	environment := strings.TrimSpace(input.Environment)
	warnings := make([]string, 0, 1)
	var sourceArgs []string
	if analysis.Connection == "" {
		path, err := resolveNotebookLocalSourcePath(executor.transfer.workspaceRoot, renderedURI)
		if err != nil {
			return notebook.BlockOutput{}, err
		}
		sourceArgs = []string{"--src-stream", notebookSnapshotFileURI(path)}
	} else {
		selected, err := loadSelectedConfig(executor.transfer.configPath, input.Environment)
		if err != nil {
			return notebook.BlockOutput{}, fmt.Errorf("load notebook source environment: %w", err)
		}
		environment = selected.SelectedEnvironmentName
		if executor.transfer.newConnectionManager == nil {
			return notebook.BlockOutput{}, fmt.Errorf("notebook source connection manager is unavailable")
		}
		manager, err := executor.transfer.newConnectionManager(ctx, environment)
		if err != nil {
			return notebook.BlockOutput{}, fmt.Errorf("resolve notebook source environment: %w", err)
		}
		if details, ok := manager.(interface{ GetConnectionType(string) string }); ok {
			if category := loadConnectionCategory(details.GetConnectionType(analysis.Connection)); category != LoadCategoryStorage {
				return notebook.BlockOutput{}, fmt.Errorf("connection %q is not an object-storage connection", analysis.Connection)
			}
		}
		connectionURI, warning, err := loadConnectionURIWithWarning(manager, analysis.Connection)
		if err != nil {
			return notebook.BlockOutput{}, err
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
		sourceArgs = []string{"--src-conn", connectionURI, "--src-stream", renderedURI}
	}
	sourceArgs = append(sourceArgs, "--src-options", string(encodedOptions))
	if mode == notebook.SnapshotModeSample {
		sourceArgs = append(sourceArgs, "--limit", strconv.FormatInt(rowLimit, 10))
	}
	artifact, err := executor.transfer.snapshotFromSling(ctx, mode, notebook.SnapshotProvenance{
		SourceKind: analysis.Kind, Environment: environment, Connection: analysis.Connection,
		DefinitionFingerprint: definitionFingerprint, CreatedAt: time.Now().UTC(), Warnings: warnings,
	}, func(_ context.Context, _ string, _ io.Writer) (notebookSnapshotSource, error) {
		return notebookSnapshotSource{Args: sourceArgs}, nil
	})
	if err != nil {
		return notebook.BlockOutput{}, err
	}
	cleanup := artifact.Cleanup
	artifact.Cleanup = nil
	return notebook.BlockOutput{Artifact: &artifact, Cleanup: cleanup}, nil
}

func (executor *notebookSourceExecutor) executeHTTP(
	ctx context.Context,
	input notebook.ExecuteBlockInput,
	mode string,
	rowLimit int64,
	definitionFingerprint string,
) (notebook.BlockOutput, error) {
	if executor.renderer == nil {
		return notebook.BlockOutput{}, fmt.Errorf("notebook HTTP source renderer is unavailable")
	}
	spec, _, err := parseNativeAPIAssetSpec(input.Cell.Raw, input.Cell.Asset, nil)
	if err != nil {
		return notebook.BlockOutput{}, fmt.Errorf("parse notebook HTTP source: %w", err)
	}
	if strings.TrimSpace(spec.Request.URL) == "" {
		return notebook.BlockOutput{}, fmt.Errorf("HTTP source request.url is required")
	}
	spec.Request.Body = resolveNotebookSourceBodyParameters(spec.Request.Body, input.ParameterValues)
	environment := strings.TrimSpace(input.Environment)
	if selected, selectErr := loadSelectedConfig(executor.transfer.configPath, input.Environment); selectErr == nil {
		environment = selected.SelectedEnvironmentName
	}
	artifact, err := executor.transfer.snapshotFromSling(ctx, mode, notebook.SnapshotProvenance{
		SourceKind: "http", Environment: environment,
		DefinitionFingerprint: definitionFingerprint, CreatedAt: time.Now().UTC(),
	}, func(runCtx context.Context, stagingDir string, output io.Writer) (notebookSnapshotSource, error) {
		jsonlPath := filepath.Join(stagingDir, "response.jsonl")
		limit := int64(0)
		if mode == notebook.SnapshotModeSample {
			limit = rowLimit
		}
		count, err := writeAPIAssetJSONLLimited(runCtx, executor.renderer, spec, jsonlPath, output, limit)
		if err != nil {
			return notebookSnapshotSource{}, err
		}
		if output != nil {
			_, _ = fmt.Fprintf(output, "Fetched %d notebook source records.\n", count)
		}
		args := []string{
			"--src-stream", notebookSnapshotFileURI(jsonlPath),
			"--src-options", apiJSONLSourceOptions,
		}
		if mode == notebook.SnapshotModeSample {
			args = append(args, "--limit", strconv.FormatInt(rowLimit, 10))
		}
		return notebookSnapshotSource{Args: args}, nil
	})
	if err != nil {
		return notebook.BlockOutput{}, err
	}
	cleanup := artifact.Cleanup
	artifact.Cleanup = nil
	return notebook.BlockOutput{Artifact: &artifact, Cleanup: cleanup}, nil
}

var notebookSourceParameterExpression = regexp.MustCompile(`^\s*\{\{\s*parameters\.([a-z][a-z0-9_]*)\s*\}\}\s*$`)

// resolveNotebookSourceBodyParameters preserves the JSON type of an exact
// `{{ parameters.id }}` body value. Embedded expressions still use ordinary
// Jinja string rendering, while a boolean/number/list placeholder remains a
// boolean/number/list in the outgoing JSON request.
func resolveNotebookSourceBodyParameters(value any, parameters map[string]any) any {
	switch typed := value.(type) {
	case string:
		match := notebookSourceParameterExpression.FindStringSubmatch(typed)
		if len(match) == 2 {
			if parameter, ok := parameters[match[1]]; ok {
				return cloneJSONValue(parameter)
			}
		}
		return typed
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = resolveNotebookSourceBodyParameters(item, parameters)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = resolveNotebookSourceBodyParameters(item, parameters)
		}
		return result
	case map[any]any:
		result := make(map[any]any, len(typed))
		for key, item := range typed {
			result[key] = resolveNotebookSourceBodyParameters(item, parameters)
		}
		return result
	default:
		return value
	}
}

func resolveNotebookLocalSourcePath(workspaceRoot, rawPath string) (string, error) {
	value := strings.TrimSpace(rawPath)
	if value == "" {
		return "", fmt.Errorf("file source uri is required")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid file source uri: %w", err)
	}
	if parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, "file") || (parsed.Host != "" && parsed.Host != "localhost") {
			return "", fmt.Errorf("local notebook sources must use a workspace path or file URL")
		}
		value = parsed.Path
	}
	path := filepath.FromSlash(value)
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspaceRoot, path)
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("source file %s is unavailable: %w", value, err)
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("local notebook source must stay inside the workspace")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("source path %s is a directory", value)
	}
	return resolved, nil
}
