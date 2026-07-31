package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/scheduler"
)

const seedTargetConnectionEnv = "RENART_SEED_TARGET"

var seedSizedTypePattern = regexp.MustCompile(`^([a-z ]+)\s*\(([^)]+)\)$`)

// slingSeedOperator materializes every seed through the same Sling boundary as
// Load, API, and non-DuckDB Python assets. Keeping this operator in the normal
// Bruin executor registry preserves column/custom checks and DuckDB lease
// coordination around the main task.
type slingSeedOperator struct {
	manager       config.ConnectionGetter
	renderer      jinja.RendererInterface
	workspaceRoot string
}

func newSlingSeedOperator(manager config.ConnectionGetter, renderer jinja.RendererInterface, workspaceRoot string) *slingSeedOperator {
	return &slingSeedOperator{manager: manager, renderer: renderer, workspaceRoot: workspaceRoot}
}

func (o *slingSeedOperator) Run(ctx context.Context, instance scheduler.TaskInstance) error {
	if instance == nil || instance.GetAsset() == nil {
		return errors.New("seed asset is required")
	}
	asset := instance.GetAsset()
	if instance.GetPipeline() == nil {
		return fmt.Errorf("pipeline is required to run seed %q", asset.Name)
	}

	seedPath, ok := asset.Parameters.GetString("path")
	if !ok || strings.TrimSpace(seedPath) == "" {
		return fmt.Errorf("%s requires a path parameter", asset.Type)
	}
	if o.renderer != nil {
		rendered, err := o.renderer.Render(seedPath)
		if err != nil {
			return fmt.Errorf("failed to render seed path: %w", err)
		}
		seedPath = rendered
	}

	fileType, _ := asset.Parameters.GetString("file_type")
	sourceStream, sourceOptions, err := resolveSlingSeedSource(seedPath, fileType, filepath.Dir(asset.ExecutableFile.Path))
	if err != nil {
		return err
	}
	connectionName, err := instance.GetPipeline().GetConnectionNameForAsset(asset)
	if err != nil {
		return err
	}

	var output io.Writer
	if printer, ok := ctx.Value(bruinexecutor.KeyPrinter).(io.Writer); ok {
		output = printer
	}
	writer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil), onChunk: func(chunk []byte) {
		if output != nil {
			_, _ = output.Write(chunk)
		}
	}}
	targetURI, connectionWarning, err := loadConnectionURIWithWarning(o.manager, connectionName)
	if err != nil {
		return err
	}
	writeSlingConnectionWarning(writer, connectionWarning)

	args := []string{
		"run",
		"--src-stream", sourceStream,
		"--src-options", sourceOptions,
		"--tgt-conn", seedTargetConnectionEnv,
		"--tgt-object", asset.Name,
	}
	materializationArgs, err := slingMaterializationArgs(ctx, asset)
	if err != nil {
		return err
	}
	args = append(args, materializationArgs...)
	targetOptions, err := slingTargetOptionsArgs(o.manager, connectionName, map[string]any{"column_casing": "snake"})
	if err != nil {
		return err
	}
	args = append(args, targetOptions...)
	columnArgs, err := slingSeedColumnArgs(asset)
	if err != nil {
		return err
	}
	args = append(args, columnArgs...)

	cmdName, cmdArgs, err := loadCommand(ctx, args, writer)
	if err != nil {
		return err
	}
	cmd := newStreamingCommand(ctx, cmdName, cmdArgs, o.workspaceRoot, writer)
	cmd.Env = append(cmd.Env, seedTargetConnectionEnv+"="+targetURI)
	if err := runStreamingCommand(cmd, writer); err != nil {
		return fmt.Errorf("failed to load seed %s with Sling: %w", asset.Name, err)
	}
	return nil
}

func resolveSlingSeedSource(seedPath, fileType, assetDir string) (string, string, error) {
	trimmedPath := strings.TrimSpace(seedPath)
	if trimmedPath == "" {
		return "", "", errors.New("seed path is required")
	}

	parsed, err := url.Parse(trimmedPath)
	if err != nil {
		return "", "", fmt.Errorf("invalid seed path: %w", err)
	}
	pathForType := trimmedPath
	sourceStream := trimmedPath
	if parsed.IsAbs() {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", "", fmt.Errorf("seed URLs must use http or https, got %q", parsed.Scheme)
		}
		if parsed.Host == "" {
			return "", "", errors.New("seed URL requires a host")
		}
		pathForType = parsed.Path
	} else {
		localPath := filepath.FromSlash(trimmedPath)
		if !filepath.IsAbs(localPath) {
			localPath = filepath.Join(assetDir, localPath)
		}
		localPath = filepath.Clean(localPath)
		if info, statErr := os.Stat(localPath); statErr != nil {
			return "", "", fmt.Errorf("seed file %s is unavailable: %w", localPath, statErr)
		} else if info.IsDir() {
			return "", "", fmt.Errorf("seed path %s is a directory", localPath)
		}
		sourceStream = "file://" + filepath.ToSlash(localPath)
		pathForType = localPath
	}

	format, err := slingSeedFormat(fileType, pathForType)
	if err != nil {
		return "", "", err
	}
	encoded, err := json.Marshal(map[string]string{"format": format})
	if err != nil {
		return "", "", err
	}
	return sourceStream, string(encoded), nil
}

func slingSeedFormat(fileType, path string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(fileType))
	if normalized == "" {
		normalized = strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		if normalized == "" {
			normalized = "csv"
		}
	}
	switch normalized {
	case "csv", "parquet", "json", "avro":
		return normalized, nil
	case "jsonl", "ndjson":
		return "jsonlines", nil
	default:
		return "", fmt.Errorf("unsupported seed file type %q (supported: csv, parquet, json, jsonl, ndjson, avro)", normalized)
	}
}

func slingSeedColumnArgs(asset *pipeline.Asset) ([]string, error) {
	if asset == nil || len(asset.Columns) == 0 {
		return nil, nil
	}
	enforceSchema := true
	if value, ok := asset.Parameters.GetString("enforce_schema"); ok {
		enforceSchema = strings.EqualFold(strings.TrimSpace(value), "true")
	}
	if !enforceSchema {
		return nil, nil
	}

	columns := make(map[string]string, len(asset.Columns))
	selected := make([]string, 0, len(asset.Columns))
	for _, column := range asset.Columns {
		name := strings.TrimSpace(column.Name)
		if name == "" {
			continue
		}
		source := strings.TrimSpace(column.SourceColumn)
		if source == "" {
			source = name
		}
		if source == name {
			selected = append(selected, source)
		} else {
			selected = append(selected, source+" as "+name)
		}
		if columnType := slingSeedColumnType(column.Type); columnType != "" {
			columns[name] = columnType
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	args := []string{"--select", strings.Join(selected, ",")}
	if len(columns) > 0 {
		encoded, err := json.Marshal(columns)
		if err != nil {
			return nil, err
		}
		args = append(args, "--columns", string(encoded))
	}
	if primaryKeys := asset.ColumnNamesWithPrimaryKey(); len(primaryKeys) > 0 {
		// Besides preserving the authored table key, this prevents Sling's
		// StarRocks fallback from inventing _sling_row_id after the explicit
		// schema projection has already been fixed.
		args = append(args, "--primary-key", strings.Join(primaryKeys, ","))
	}
	return args, nil
}

func slingSeedColumnType(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}
	base := normalized
	size := ""
	if match := seedSizedTypePattern.FindStringSubmatch(normalized); len(match) == 3 {
		base = strings.TrimSpace(match[1])
		size = strings.TrimSpace(match[2])
	}

	var result string
	switch base {
	case "tinyint", "smallint", "int", "int2", "int4", "integer", "mediumint", "serial", "smallserial":
		result = "integer"
	case "bigint", "int8", "int64", "bigserial", "long":
		result = "bigint"
	case "bool", "boolean":
		result = "bool"
	case "date", "datetime", "timestamp", "timestamp with time zone", "timestamp without time zone", "timestamptz":
		result = "datetime"
	case "decimal", "numeric", "number":
		result = "decimal"
	case "json", "jsonb", "object", "variant":
		result = "json"
	case "char", "character", "string", "text", "varchar", "character varying", "nvarchar", "nchar":
		result = "string"
	case "binary", "blob", "bytea", "varbinary":
		// Sling has no generic binary cast in its CLI column contract. Let
		// source inference preserve the native value instead.
		return ""
	default:
		return ""
	}
	if size != "" && (result == "string" || result == "decimal") {
		return result + "(" + size + ")"
	}
	return result
}
