package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
)

const maxSeedSchemaCacheEntries = 64

type seedSchemaDiscoveryCall struct {
	done    chan struct{}
	columns []WorkspaceColumn
	output  []byte
	err     error
}

// inferSeedColumnsFromSource asks Sling for the schema of a local seed file.
// Unlike warehouse-backed non-SQL assets, a seed has a complete definition-time
// source, so users can refresh its columns before the first materialization.
func (s *AssetService) inferSeedColumnsFromSource(
	ctx context.Context,
	parsedPipeline *pipeline.Pipeline,
	asset *pipeline.Asset,
) ([]WorkspaceColumn, *APIError) {
	seedPath, ok := asset.Parameters.GetString("path")
	if !ok || strings.TrimSpace(seedPath) == "" {
		return nil, badRequestError("missing_seed_path", "seed path is required to infer columns")
	}

	pipelineName := ""
	if parsedPipeline != nil {
		pipelineName = parsedPipeline.Name
	}
	baseRenderer := jinja.NewRendererWithYesterday(pipelineName, "web-seed-column-infer")
	var renderer jinja.RendererInterface = baseRenderer
	if parsedPipeline != nil {
		assetRenderer, cloneErr := baseRenderer.CloneForAsset(ctx, parsedPipeline, asset)
		if cloneErr != nil {
			return nil, badRequestError("seed_path_render_failed", cloneErr.Error())
		}
		renderer = assetRenderer
	}
	renderedPath, renderErr := renderer.Render(seedPath)
	if renderErr != nil {
		return nil, badRequestError("seed_path_render_failed", renderErr.Error())
	}
	seedPath = renderedPath

	definitionPath := strings.TrimSpace(asset.DefinitionFile.Path)
	if definitionPath == "" {
		definitionPath = strings.TrimSpace(asset.ExecutableFile.Path)
	}
	fileType, _ := asset.Parameters.GetString("file_type")
	sourceStream, _, err := resolveSlingSeedSource(seedPath, fileType, filepath.Dir(definitionPath))
	if err != nil {
		return nil, badRequestError("seed_source_resolve_failed", err.Error())
	}
	if !strings.HasPrefix(strings.ToLower(sourceStream), "file://") {
		return nil, badRequestError(
			"remote_seed_column_inference_unsupported",
			"URL seed columns can be imported from the materialized output after the seed runs",
		)
	}

	pattern := filepath.FromSlash(strings.TrimPrefix(sourceStream, "file://"))
	columns, output, runErr := s.discoverSeedColumns(ctx, pattern)
	if runErr != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = runErr.Error()
		}
		return nil, badRequestError("seed_column_inference_failed", message)
	}
	if len(columns) == 0 {
		return nil, badRequestError(
			"seed_column_inference_failed",
			fmt.Sprintf("Sling found no columns in %s", strings.TrimSpace(seedPath)),
		)
	}
	return columns, nil
}

// discoverSeedColumns deduplicates Sling discovery by the local input's
// content fingerprint. Pure typecheck/LSP paths never call this function; an
// explicit filesystem-enabled schema observation is the only entry point.
func (s *AssetService) discoverSeedColumns(ctx context.Context, pattern string) ([]WorkspaceColumn, []byte, error) {
	key, err := seedSchemaFingerprint(pattern)
	if err != nil {
		return nil, nil, err
	}

	s.seedSchemaMu.Lock()
	if s.seedSchemaCache == nil {
		s.seedSchemaCache = make(map[string][]WorkspaceColumn)
	}
	if s.seedSchemaInflight == nil {
		s.seedSchemaInflight = make(map[string]*seedSchemaDiscoveryCall)
	}
	if columns, ok := s.seedSchemaCache[key]; ok {
		result := append([]WorkspaceColumn(nil), columns...)
		s.seedSchemaMu.Unlock()
		return result, nil, nil
	}
	if call, ok := s.seedSchemaInflight[key]; ok {
		s.seedSchemaMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-call.done:
			return append([]WorkspaceColumn(nil), call.columns...), append([]byte(nil), call.output...), call.err
		}
	}
	call := &seedSchemaDiscoveryCall{done: make(chan struct{})}
	s.seedSchemaInflight[key] = call
	s.seedSchemaMu.Unlock()

	call.output, call.err = runSlingSeedColumnDiscovery(ctx, s.deps.WorkspaceRoot, pattern)
	if call.err == nil {
		call.columns = parseSlingSeedColumns(string(call.output))
	}

	s.seedSchemaMu.Lock()
	delete(s.seedSchemaInflight, key)
	if call.err == nil && len(call.columns) > 0 {
		if _, exists := s.seedSchemaCache[key]; !exists {
			if len(s.seedSchemaCacheOrder) >= maxSeedSchemaCacheEntries {
				oldest := s.seedSchemaCacheOrder[0]
				s.seedSchemaCacheOrder = s.seedSchemaCacheOrder[1:]
				delete(s.seedSchemaCache, oldest)
			}
			s.seedSchemaCacheOrder = append(s.seedSchemaCacheOrder, key)
		}
		s.seedSchemaCache[key] = append([]WorkspaceColumn(nil), call.columns...)
	}
	close(call.done)
	s.seedSchemaMu.Unlock()

	return append([]WorkspaceColumn(nil), call.columns...), append([]byte(nil), call.output...), call.err
}

func seedSchemaFingerprint(pattern string) (string, error) {
	pattern = filepath.Clean(strings.TrimSpace(pattern))
	if pattern == "" || pattern == "." {
		return "", fmt.Errorf("seed path is empty")
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		matches = []string{pattern}
	}
	sort.Strings(matches)

	digest := sha256.New()
	_, _ = io.WriteString(digest, "renart-seed-schema-v1\x00")
	for _, match := range matches {
		info, statErr := os.Stat(match)
		if statErr != nil {
			return "", statErr
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("seed schema input %s is not a regular file", match)
		}
		_, _ = io.WriteString(digest, filepath.Clean(match))
		_, _ = io.WriteString(digest, "\x00")
		file, openErr := os.Open(match)
		if openErr != nil {
			return "", openErr
		}
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		_, _ = io.WriteString(digest, "\x00")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func runSlingSeedColumnDiscovery(ctx context.Context, workspaceRoot, pattern string) ([]byte, error) {
	args := []string{
		"conns", "discover", "LOCAL",
		"--pattern", pattern,
		"--columns",
		"-o", "json",
	}
	cmdName, cmdArgs, err := loadCommand(ctx, args, nil)
	if err != nil {
		return nil, err
	}
	cmd := newStreamingCommand(ctx, cmdName, cmdArgs, workspaceRoot, nil)
	return runSlingCombinedOutput(ctx, cmd)
}

// parseSlingSeedColumns reads `sling conns discover LOCAL --columns -o json`.
// Sling may prefix the JSON with log lines, so it shares the tolerant decoder
// used by Load connection discovery.
func parseSlingSeedColumns(output string) []WorkspaceColumn {
	payload, ok := decodeLoadDiscoverPayload(output)
	if !ok {
		return []WorkspaceColumn{}
	}

	nameIndex, generalTypeIndex, nativeTypeIndex := -1, -1, -1
	for index, field := range payload.Fields {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "column", "name":
			nameIndex = index
		case "general type", "general_type", "type":
			generalTypeIndex = index
		case "native type", "native_type":
			nativeTypeIndex = index
		}
	}
	if nameIndex < 0 {
		return []WorkspaceColumn{}
	}

	seen := make(map[string]struct{})
	columns := make([]WorkspaceColumn, 0, len(payload.Rows))
	for _, row := range payload.Rows {
		if nameIndex >= len(row) {
			continue
		}
		name := loadCellString(row[nameIndex])
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		columnType := ""
		if generalTypeIndex >= 0 && generalTypeIndex < len(row) {
			columnType = loadCellString(row[generalTypeIndex])
		}
		if (columnType == "" || columnType == "-") &&
			nativeTypeIndex >= 0 && nativeTypeIndex < len(row) {
			columnType = loadCellString(row[nativeTypeIndex])
		}
		if columnType == "-" {
			columnType = ""
		}
		columns = append(columns, WorkspaceColumn{Name: name, Type: columnType})
	}
	return columns
}
