package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
)

// PromoteCellRequest selects a reviewed notebook-to-pipeline promotion. A
// caller that first requested PlanPromoteCell sends BaseRevision back on apply
// so the exact notebook snapshot it reviewed cannot be replaced underneath it.
type PromoteCellRequest struct {
	PipelineID        string `json:"pipeline_id"`
	TargetName        string `json:"target_name"`
	IncludeUpstream   bool   `json:"include_upstream,omitempty"`
	IncludeDownstream bool   `json:"include_downstream,omitempty"`
	BaseRevision      string `json:"base_revision,omitempty"`
}

// PromoteCellAssetPreview is the user-facing consequence of promoting one
// notebook block. It intentionally contains no request headers, credentials,
// or source bytes.
type PromoteCellAssetPreview struct {
	CellID           string `json:"cell_id"`
	CellName         string `json:"cell_name"`
	TargetName       string `json:"target_name"`
	Path             string `json:"path"`
	AssetType        string `json:"asset_type"`
	Connection       string `json:"connection,omitempty"`
	SourceConnection string `json:"source_connection,omitempty"`
	Materialization  string `json:"materialization"`
}

type PromoteCellFilePreview struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// PromoteCellPlan is a credential-free review of the exact block roles,
// connections, materialization behavior, and file set an apply will produce.
type PromoteCellPlan struct {
	Status       string                    `json:"status"`
	BaseRevision string                    `json:"base_revision"`
	Assets       []PromoteCellAssetPreview `json:"assets"`
	Files        []PromoteCellFilePreview  `json:"files"`
	Warnings     []string                  `json:"warnings,omitempty"`
	CanApply     bool                      `json:"can_apply"`
}

// PromoteCellResult reports the outcome of an atomic promotion.
type PromoteCellResult struct {
	Status         string         `json:"status"`
	AssetPath      string         `json:"asset_path"`
	AssetPaths     []string       `json:"asset_paths,omitempty"`
	PromotedCount  int            `json:"promoted_count"`
	DialectWarning string         `json:"dialect_warning,omitempty"`
	Notebook       model.Notebook `json:"notebook"`
}

type promotionDestination struct {
	Pipeline     *model.Pipeline
	Parsed       *pipeline.Pipeline
	Dir          string
	SQLAssetType string
	Dialect      string
	Connection   string
}

type preparedCellPromotion struct {
	Notebook    *notebook.Notebook
	Plan        *notebook.PromotePlan
	Preview     PromoteCellPlan
	Before      map[string][]byte
	After       map[string][]byte
	PromotedIDs map[string]bool
	AssetPaths  []string
}

// PlanPromoteCell resolves promotion semantics without changing authored
// files. Sampled source blocks are rejected here, before a production-looking
// Seed/API/Load asset can be created from partial data.
func (s *NotebookService) PlanPromoteCell(notebookID, cellID string, req PromoteCellRequest) (PromoteCellPlan, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	prepared, apiErr := s.prepareCellPromotion(notebookID, cellID, req)
	if apiErr != nil {
		return PromoteCellPlan{}, apiErr
	}
	return prepared.Preview, nil
}

// PromoteCell moves one or more notebook blocks into a pipeline through one
// recoverable workspace transaction. A crash or write failure restores both
// the notebook snapshot and every destination pipeline file.
func (s *NotebookService) PromoteCell(notebookID, cellID string, req PromoteCellRequest) (PromoteCellResult, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	prepared, apiErr := s.prepareCellPromotion(notebookID, cellID, req)
	if apiErr != nil {
		return PromoteCellResult{}, apiErr
	}
	if err := applyWorkspaceFileTransaction(s.deps.WorkspaceRoot, prepared.Before, prepared.After, nil); err != nil {
		status := http.StatusInternalServerError
		code := "promote_failed"
		if strings.Contains(err.Error(), "changed before transaction commit") || strings.Contains(err.Error(), "appeared before transaction commit") {
			status = http.StatusConflict
			code = "notebook_edit_conflict"
		}
		return PromoteCellResult{}, &APIError{Status: status, Code: code, Message: err.Error()}
	}
	for blockID := range prepared.PromotedIDs {
		_ = s.store.DropCellObjects(prepared.Notebook.UUID, blockID)
	}

	if len(prepared.AssetPaths) > 0 {
		s.pushUpdate(prepared.AssetPaths[0])
	}
	updated, apiErr := s.Get(notebookID)
	if apiErr != nil {
		return PromoteCellResult{}, apiErr
	}
	primaryPath := ""
	if len(prepared.AssetPaths) > 0 {
		primaryPath = prepared.AssetPaths[0]
	}
	return PromoteCellResult{
		Status:         "ok",
		AssetPath:      primaryPath,
		AssetPaths:     prepared.AssetPaths,
		PromotedCount:  len(prepared.Plan.Assets),
		DialectWarning: prepared.Plan.DialectWarning,
		Notebook:       updated,
	}, nil
}

func (s *NotebookService) prepareCellPromotion(notebookID, cellID string, req PromoteCellRequest) (*preparedCellPromotion, *APIError) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return nil, apiErr
	}
	if baseRevision := strings.TrimSpace(req.BaseRevision); baseRevision != "" && baseRevision != nb.Revision {
		return nil, &APIError{
			Status: http.StatusConflict, Code: "notebook_edit_conflict",
			Message: "This notebook changed after the promotion was reviewed. Review the promotion again.",
		}
	}
	cell := nb.CellByID(cellID)
	if cell == nil {
		return nil, &APIError{Status: http.StatusNotFound, Code: "cell_not_found", Message: "cell not found"}
	}

	destination, apiErr := s.resolvePromotionTarget(req.PipelineID)
	if apiErr != nil {
		return nil, apiErr
	}
	targets, apiErr := s.promotionTargets(nb, cell, req, destination)
	if apiErr != nil {
		return nil, apiErr
	}
	if collision := promotionNameCollision(destination.Pipeline, targets); collision != nil {
		return nil, collision
	}

	plan, err := notebook.PlanPromoteCells(
		nb, targets, filepath.Join(destination.Dir, "assets"), destination.SQLAssetType, destination.Dialect,
	)
	if err != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: err.Error()}
	}

	previewAssets := make([]PromoteCellAssetPreview, 0, len(targets))
	warnings := make([]string, 0, 2)
	if plan.DialectWarning != "" {
		warnings = append(warnings, plan.DialectWarning)
	}
	for index, target := range targets {
		candidate := nb.CellByID(target.CellID)
		if candidate == nil {
			return nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: fmt.Sprintf("cell %q not found", target.CellID)}
		}
		asset := plan.Assets[index]
		assetType := strings.TrimSpace(target.AssetType)
		connection := strings.TrimSpace(target.Connection)
		sourceConnection := ""
		materialization := "table (create+replace)"

		if candidate.Source != nil {
			if candidate.Source.Snapshot.Mode == notebook.SnapshotModeSample {
				return nil, &APIError{
					Status: http.StatusBadRequest, Code: "sample_source_promotion_requires_full",
					Message: fmt.Sprintf("%s is an explicit sample. Change its snapshot mode to full and refresh it before promoting it to a pipeline.", candidate.Asset.Name),
				}
			}
			var renderErr error
			asset, assetType, connection, sourceConnection, materialization, renderErr = s.renderPromotedSourceAsset(
				candidate, strings.TrimSpace(target.TargetName), destination,
			)
			if renderErr != nil {
				return nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: renderErr.Error()}
			}
			plan.Assets[index] = asset
		} else if notebook.IsPythonCell(candidate) {
			assetType = notebook.PythonCellType
			materialization = "table"
		} else if strings.TrimSpace(candidate.Asset.Connection) != "" {
			assetType = string(candidate.Asset.Type)
			connection = strings.TrimSpace(candidate.Asset.Connection)
			materialization = "table"
			if destination.Connection != "" && !strings.EqualFold(connection, destination.Connection) {
				warnings = appendUniqueString(warnings, fmt.Sprintf(
					"%s remains on source connection %s; promotion does not copy it into pipeline target %s.",
					candidate.Asset.Name, connection, destination.Connection,
				))
			}
		} else {
			if assetType == "" {
				assetType = destination.SQLAssetType
			}
			if connection == "" {
				connection = destination.Connection
			}
			materialization = "table"
		}

		relPath, relErr := workspaceRelativePath(s.deps.WorkspaceRoot, asset.Path)
		if relErr != nil {
			return nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: relErr.Error()}
		}
		previewAssets = append(previewAssets, PromoteCellAssetPreview{
			CellID: target.CellID, CellName: candidate.Asset.Name, TargetName: target.TargetName,
			Path: relPath, AssetType: assetType, Connection: connection,
			SourceConnection: sourceConnection, Materialization: materialization,
		})
	}

	before, after, promotedIDs, apiErr := s.promotionFileSets(nb, plan)
	if apiErr != nil {
		return nil, apiErr
	}
	assetPaths := make([]string, 0, len(plan.Assets))
	for _, asset := range plan.Assets {
		rel, relErr := workspaceRelativePath(s.deps.WorkspaceRoot, asset.Path)
		if relErr != nil {
			return nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: relErr.Error()}
		}
		assetPaths = append(assetPaths, rel)
	}
	return &preparedCellPromotion{
		Notebook: nb, Plan: plan, Before: before, After: after,
		PromotedIDs: promotedIDs, AssetPaths: assetPaths,
		Preview: PromoteCellPlan{
			Status: "ok", BaseRevision: nb.Revision, Assets: previewAssets,
			Files: promotionFilePreviews(before, after), Warnings: warnings, CanApply: true,
		},
	}, nil
}

// promotionTargets builds the ordered promote set for the request. It also
// pins connection-bound source SQL and Python cells to their reviewed runtime
// instead of silently inheriting a different pipeline default.
func (s *NotebookService) promotionTargets(nb *notebook.Notebook, primary *notebook.Cell, req PromoteCellRequest, destination *promotionDestination) ([]notebook.PromoteTarget, *APIError) {
	primaryName := strings.TrimSpace(req.TargetName)
	if primaryName == "" {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: "a target asset name is required"}
	}
	if !strings.Contains(primaryName, ".") {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "missing_asset_prefix", Message: "target asset name must include a prefix, for example marts.orders"}
	}

	schemaPrefix := primaryName[:strings.LastIndex(primaryName, ".")+1]
	cells := []*notebook.Cell{primary}
	if req.IncludeUpstream {
		cells = append(cells, notebook.Ancestors(nb, primary)...)
	}
	if req.IncludeDownstream {
		cells = append(cells, notebook.Descendants(nb, primary)...)
	}

	seen := map[string]bool{}
	targets := make([]notebook.PromoteTarget, 0, len(cells))
	for _, candidate := range cells {
		if seen[candidate.ID] {
			continue
		}
		seen[candidate.ID] = true
		name := schemaPrefix + candidate.Asset.Name
		if candidate.ID == primary.ID {
			name = primaryName
		}
		target := notebook.PromoteTarget{CellID: candidate.ID, TargetName: name}
		switch {
		case candidate.Source != nil:
			// Source blocks are rendered to Seed/API/Load definitions below.
		case notebook.IsPythonCell(candidate):
			target.AssetType = notebook.PythonCellType
			target.Connection = destination.Connection
		case strings.TrimSpace(candidate.Asset.Connection) != "":
			target.AssetType = string(candidate.Asset.Type)
			target.Connection = strings.TrimSpace(candidate.Asset.Connection)
		default:
			target.AssetType = destination.SQLAssetType
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func promotionNameCollision(target *model.Pipeline, targets []notebook.PromoteTarget) *APIError {
	seen := map[string]bool{}
	for _, candidate := range targets {
		key := strings.ToLower(strings.TrimSpace(candidate.TargetName))
		if seen[key] {
			return &APIError{Status: http.StatusConflict, Code: "asset_exists", Message: fmt.Sprintf("two promoted cells would both be named %q", candidate.TargetName)}
		}
		seen[key] = true
		for _, asset := range target.Assets {
			if strings.EqualFold(asset.Name, candidate.TargetName) {
				return &APIError{Status: http.StatusConflict, Code: "asset_exists", Message: fmt.Sprintf("pipeline already has an asset named %q", candidate.TargetName)}
			}
		}
	}
	return nil
}

func (s *NotebookService) resolvePromotionTarget(pipelineID string) (*promotionDestination, *APIError) {
	if s.deps.CurrentState == nil {
		return nil, &APIError{Status: http.StatusInternalServerError, Code: "promote_failed", Message: "workspace state unavailable"}
	}
	state := s.deps.CurrentState()
	var target *model.Pipeline
	for index := range state.Pipelines {
		if state.Pipelines[index].ID == pipelineID {
			target = &state.Pipelines[index]
			break
		}
	}
	if target == nil {
		return nil, &APIError{Status: http.StatusNotFound, Code: "pipeline_not_found", Message: "destination pipeline not found"}
	}
	pipelineDir, err := SafeJoin(s.deps.WorkspaceRoot, filepath.FromSlash(target.Path))
	if err != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: err.Error()}
	}
	parsed, parseErr := NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(context.Background(), pipelineDir, pipeline.WithMutate())
	if parseErr != nil {
		return nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: fmt.Sprintf("load destination pipeline: %v", parseErr)}
	}

	connection, _ := defaultPipelineTargetConnection(parsed)
	assetType := ""
	if connection != "" {
		assetType, _ = sqlAssetTypeForConnectionName(parsed, connection)
	}
	for _, asset := range target.Assets {
		if !strings.HasSuffix(strings.ToLower(asset.Type), ".sql") {
			continue
		}
		if assetType == "" || (connection != "" && strings.EqualFold(asset.Connection, connection)) {
			assetType = asset.Type
			if connection == "" {
				connection = strings.TrimSpace(asset.Connection)
			}
			if connection == "" || strings.EqualFold(asset.Connection, connection) {
				break
			}
		}
	}
	if assetType == "" {
		assetType = "duckdb.sql"
	}
	if connection == "" && assetType == "duckdb.sql" {
		connection = "duckdb-default"
	}
	dialect := "duckdb"
	if parsedDialect, dialectErr := sqlparser.AssetTypeToDialect(pipeline.AssetType(assetType)); dialectErr == nil && parsedDialect != "" {
		dialect = parsedDialect
	}
	return &promotionDestination{
		Pipeline: target, Parsed: parsed, Dir: pipelineDir, SQLAssetType: assetType,
		Dialect: dialect, Connection: connection,
	}, nil
}

func (s *NotebookService) renderPromotedSourceAsset(cell *notebook.Cell, targetName string, destination *promotionDestination) (notebook.PromotedAsset, string, string, string, string, error) {
	path := filepath.Join(destination.Dir, filepath.FromSlash(assetPathForInferredName(targetName, ".asset.yml")))
	source := cell.Source
	if source == nil {
		return notebook.PromotedAsset{}, "", "", "", "", fmt.Errorf("notebook source definition is required")
	}
	if strings.TrimSpace(destination.Connection) == "" {
		return notebook.PromotedAsset{}, "", "", "", "", fmt.Errorf("destination pipeline has no resolvable target connection")
	}

	switch source.Kind {
	case notebook.SourceKindHTTP:
		content, err := renderPromotedHTTPSource(cell.Raw, targetName, destination.Connection, source.Columns)
		return notebook.PromotedAsset{Path: path, Content: content}, apiAssetType, destination.Connection, "", "table (create+replace)", err
	case notebook.SourceKindFile:
		if strings.TrimSpace(source.Connection) != "" {
			definition := promotedLoadSourceYAML{
				Name: targetName, Type: loadAssetType, Connection: destination.Connection,
				Parameters: loadAssetParametersYAML{
					SourceConnection: source.Connection, SourceTable: source.URI,
				},
				Materialization: loadAssetMaterializationYAML{Type: "table", Strategy: "create+replace"},
				Columns:         source.Columns,
			}
			content, err := yaml.Marshal(definition)
			return notebook.PromotedAsset{Path: path, Content: string(content)}, loadAssetType, destination.Connection, source.Connection, "table (create+replace)", err
		}

		seedType, ok := seedAssetTypeForPromotion(destination.SQLAssetType)
		if !ok {
			sourcePath, err := resolveNotebookLocalSourcePath(s.deps.WorkspaceRoot, source.URI)
			if err != nil {
				return notebook.PromotedAsset{}, "", "", "", "", err
			}
			workspacePath, err := workspaceRelativePath(s.deps.WorkspaceRoot, sourcePath)
			if err != nil {
				return notebook.PromotedAsset{}, "", "", "", "", err
			}
			definition := promotedLoadSourceYAML{
				Name: targetName, Type: loadAssetType, Connection: destination.Connection,
				Parameters: loadAssetParametersYAML{
					SourceConnection: loadLocalConnectionName, SourceTable: workspacePath,
				},
				Materialization: loadAssetMaterializationYAML{Type: "table", Strategy: "create+replace"},
				Columns:         source.Columns,
			}
			content, marshalErr := yaml.Marshal(definition)
			return notebook.PromotedAsset{Path: path, Content: string(content)}, loadAssetType, destination.Connection, loadLocalConnectionName, "table (create+replace)", marshalErr
		}
		sourcePath, err := resolveNotebookLocalSourcePath(s.deps.WorkspaceRoot, source.URI)
		if err != nil {
			return notebook.PromotedAsset{}, "", "", "", "", err
		}
		relative, err := filepath.Rel(filepath.Dir(path), sourcePath)
		if err != nil {
			return notebook.PromotedAsset{}, "", "", "", "", err
		}
		relative = filepath.ToSlash(relative)
		if !strings.HasPrefix(relative, ".") {
			relative = "./" + relative
		}
		fileType := strings.ToLower(strings.TrimSpace(source.Format))
		if fileType == "" {
			fileType = seedFileTypeFromPath(source.URI)
		}
		if !containsString(seedFileTypes, fileType) {
			return notebook.PromotedAsset{}, "", "", "", "", fmt.Errorf("unsupported Seed file type %q", fileType)
		}
		enforce := true
		definition := promotedSeedSourceYAML{
			Name: targetName, Type: seedType, Connection: destination.Connection,
			Parameters:      promotedSeedParametersYAML{Path: relative, FileType: fileType, EnforceSchema: &enforce},
			Materialization: promotedMaterializationYAML{Type: "table", Strategy: "create+replace"},
			Columns:         source.Columns,
		}
		content, err := yaml.Marshal(definition)
		return notebook.PromotedAsset{Path: path, Content: string(content)}, seedType, destination.Connection, "", "table (create+replace)", err
	default:
		return notebook.PromotedAsset{}, "", "", "", "", fmt.Errorf("unsupported notebook source kind %q", source.Kind)
	}
}

type promotedMaterializationYAML struct {
	Type     string `yaml:"type"`
	Strategy string `yaml:"strategy"`
}

type promotedSeedParametersYAML struct {
	Path          string `yaml:"path"`
	FileType      string `yaml:"file_type"`
	EnforceSchema *bool  `yaml:"enforce_schema"`
}

type promotedSeedSourceYAML struct {
	Name            string                      `yaml:"name"`
	Type            string                      `yaml:"type"`
	Connection      string                      `yaml:"connection"`
	Parameters      promotedSeedParametersYAML  `yaml:"parameters"`
	Materialization promotedMaterializationYAML `yaml:"materialization"`
	Columns         []pipeline.Column           `yaml:"columns,omitempty"`
}

type promotedLoadSourceYAML struct {
	Name            string                       `yaml:"name"`
	Type            string                       `yaml:"type"`
	Connection      string                       `yaml:"connection"`
	Parameters      loadAssetParametersYAML      `yaml:"parameters"`
	Materialization loadAssetMaterializationYAML `yaml:"materialization"`
	Columns         []pipeline.Column            `yaml:"columns,omitempty"`
}

func renderPromotedHTTPSource(raw, targetName, connection string, columns []pipeline.Column) (string, error) {
	var sourceDoc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &sourceDoc); err != nil {
		return "", fmt.Errorf("parse notebook HTTP source: %w", err)
	}
	sourceRoot := documentMappingNode(&sourceDoc)
	parameters := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, key := range []string{"openapi", "openapi_url", "request", "iterate", "auth", "pagination", "response"} {
		if value := mappingValue(sourceRoot, key); value != nil {
			parameters.Content = append(parameters.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, cloneYAMLNode(value),
			)
		}
	}
	if len(parameters.Content) == 0 {
		return "", fmt.Errorf("HTTP source has no promotable request definition")
	}

	doc := yaml.Node{Kind: yaml.DocumentNode}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	doc.Content = []*yaml.Node{root}
	setMappingValue(root, "name", scalarYAMLNode(targetName))
	setMappingValue(root, "type", scalarYAMLNode(apiAssetType))
	setMappingValue(root, "connection", scalarYAMLNode(connection))
	materialization := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMappingValue(materialization, "type", scalarYAMLNode("table"))
	setMappingValue(materialization, "strategy", scalarYAMLNode("create+replace"))
	setMappingValue(root, "materialization", materialization)
	setMappingValue(root, "parameters", parameters)
	if len(columns) > 0 {
		var columnNode yaml.Node
		if err := columnNode.Encode(columns); err != nil {
			return "", err
		}
		setMappingValue(root, "columns", &columnNode)
	}

	buffer := bytes.NewBuffer(nil)
	encoder := yaml.NewEncoder(buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		clone.Content[index] = cloneYAMLNode(child)
	}
	return &clone
}

func scalarYAMLNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func seedAssetTypeForPromotion(queryType string) (string, bool) {
	connectionType := normalizeConnectionType(pipeline.AssetTypeConnectionMapping[pipeline.AssetType(queryType)])
	if connectionType == "" {
		return "", false
	}
	for _, assetType := range creatableSeedAssetTypes {
		if normalizeConnectionType(pipeline.AssetTypeConnectionMapping[assetType]) == connectionType {
			return string(assetType), true
		}
	}
	return "", false
}

func (s *NotebookService) promotionFileSets(nb *notebook.Notebook, plan *notebook.PromotePlan) (map[string][]byte, map[string][]byte, map[string]bool, *APIError) {
	before := map[string][]byte{}
	after := map[string][]byte{}
	promoted := map[string]bool{}
	for _, blockID := range plan.RemoveBlockIDs {
		promoted[blockID] = true
	}

	for _, asset := range plan.Assets {
		rel, err := workspaceRelativePath(s.deps.WorkspaceRoot, asset.Path)
		if err != nil {
			return nil, nil, nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: err.Error()}
		}
		if _, statErr := os.Stat(asset.Path); statErr == nil {
			return nil, nil, nil, &APIError{Status: http.StatusConflict, Code: "asset_exists", Message: fmt.Sprintf("an asset file already exists at %s", rel)}
		} else if !os.IsNotExist(statErr) {
			return nil, nil, nil, &APIError{Status: http.StatusInternalServerError, Code: "promote_failed", Message: statErr.Error()}
		}
		after[rel] = []byte(asset.Content)
	}
	for _, edit := range plan.ReferenceEdits {
		rel, err := workspaceRelativePath(s.deps.WorkspaceRoot, edit.Path)
		if err != nil {
			return nil, nil, nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: err.Error()}
		}
		content, readErr := os.ReadFile(edit.Path)
		if readErr != nil {
			return nil, nil, nil, &APIError{Status: http.StatusInternalServerError, Code: "promote_failed", Message: readErr.Error()}
		}
		before[rel] = content
		after[rel] = []byte(edit.NewContent)
	}
	for _, removePath := range plan.RemoveCellPaths {
		rel, err := workspaceRelativePath(s.deps.WorkspaceRoot, removePath)
		if err != nil {
			return nil, nil, nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: err.Error()}
		}
		content, readErr := os.ReadFile(removePath)
		if readErr != nil {
			return nil, nil, nil, &APIError{Status: http.StatusInternalServerError, Code: "promote_failed", Message: readErr.Error()}
		}
		before[rel] = content
		delete(after, rel)
	}

	next := *nb
	next.Blocks = make([]notebook.Block, 0, len(nb.Blocks))
	for _, block := range nb.Blocks {
		if block.Cell != "" && promoted[block.Cell] {
			continue
		}
		next.Blocks = append(next.Blocks, block)
	}
	manifestContent, err := notebook.MarshalManifest(&next)
	if err != nil {
		return nil, nil, nil, &APIError{Status: http.StatusInternalServerError, Code: "promote_failed", Message: err.Error()}
	}
	manifestPath := filepath.Join(nb.Dir, notebook.ManifestFileName)
	manifestRel, err := workspaceRelativePath(s.deps.WorkspaceRoot, manifestPath)
	if err != nil {
		return nil, nil, nil, &APIError{Status: http.StatusBadRequest, Code: "promote_failed", Message: err.Error()}
	}
	currentManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, nil, nil, &APIError{Status: http.StatusInternalServerError, Code: "promote_failed", Message: err.Error()}
	}
	before[manifestRel] = currentManifest
	after[manifestRel] = manifestContent
	return before, after, promoted, nil
}

func workspaceRelativePath(workspaceRoot, path string) (string, error) {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("promotion path is outside the workspace: %s", path)
	}
	return filepath.ToSlash(rel), nil
}

func promotionFilePreviews(before, after map[string][]byte) []PromoteCellFilePreview {
	paths := make(map[string]bool, len(before)+len(after))
	for path := range before {
		paths[path] = true
	}
	for path := range after {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	result := make([]PromoteCellFilePreview, 0, len(ordered))
	for _, path := range ordered {
		beforeContent, beforeExists := before[path]
		afterContent, afterExists := after[path]
		if beforeExists && afterExists && bytes.Equal(beforeContent, afterContent) {
			continue
		}
		status := "modified"
		if !beforeExists {
			status = "added"
		} else if !afterExists {
			status = "deleted"
		}
		result = append(result, PromoteCellFilePreview{Path: path, Status: status})
	}
	return result
}
