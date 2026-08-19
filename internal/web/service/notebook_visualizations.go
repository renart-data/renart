package service

import (
	"context"
	"fmt"
	"strings"

	"renart/internal/sqlintelligence"
	"renart/internal/web/notebook"
	"renart/internal/web/presentation"
)

// NotebookVisualizationCheckRequest is shared by the visual builder and the
// YAML Definition editor. DefinitionYAML wins when present so invalid drafts
// can be checked without first parsing them in the browser.
type NotebookVisualizationCheckRequest struct {
	Source         string         `json:"source"`
	Definition     map[string]any `json:"definition,omitempty"`
	DefinitionYAML string         `json:"definition_yaml,omitempty"`
}

// NotebookVisualizationCheckResult returns the canonical parsed definition,
// resolved source schema, and the backend-owned presentation findings.
type NotebookVisualizationCheckResult struct {
	Status         string                      `json:"status"`
	Source         string                      `json:"source"`
	Definition     map[string]any              `json:"definition,omitempty"`
	DefinitionYAML string                      `json:"definition_yaml,omitempty"`
	Schema         presentation.ResolvedSchema `json:"schema"`
	Findings       []presentation.Finding      `json:"findings"`
	CanApply       bool                        `json:"can_apply"`
}

// CheckVisualization validates a notebook visualization without mutating its
// manifest. Notebook checks are deliberately non-strict: unknown runtime
// fields remain warnings while structurally invalid or incompatible known
// fields are errors.
func (s *NotebookService) CheckVisualization(ctx context.Context, notebookID string, request NotebookVisualizationCheckRequest) (NotebookVisualizationCheckResult, *APIError) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return NotebookVisualizationCheckResult{}, apiErr
	}

	result := NotebookVisualizationCheckResult{
		Status:   "ok",
		Source:   strings.TrimSpace(request.Source),
		Findings: []presentation.Finding{},
	}
	raw := cloneStringAnyMap(request.Definition)
	var definition presentation.VisualizationDefinition
	if strings.TrimSpace(request.DefinitionYAML) != "" {
		raw, definition, result.Findings = presentation.DecodeVisualizationDefinitionYAML(request.DefinitionYAML)
	} else {
		definition, result.Findings = presentation.DecodeVisualizationDefinition(raw)
	}
	result.Definition = raw
	if raw != nil {
		if encoded, err := presentation.EncodeVisualizationDefinitionYAML(raw); err == nil {
			result.DefinitionYAML = encoded
		}
	}

	cell := nb.CellByID(result.Source)
	if cell == nil {
		result.Findings = append(result.Findings, presentation.Finding{
			Code: "visualization-source-missing", Severity: "error", Path: "source",
			Message: fmt.Sprintf("Source cell %q does not exist in this notebook.", result.Source),
		})
	} else {
		result.Schema = s.resolveNotebookVisualizationSchema(ctx, nb, cell)
		if !hasPresentationErrors(result.Findings) {
			result.Findings = append(result.Findings,
				(presentation.Checker{}).CheckVisualization(ctx, definition, result.Schema, presentation.CheckOptions{})...)
		}
	}
	result.CanApply = !hasPresentationErrors(result.Findings)
	return result, nil
}

func (s *NotebookService) resolveNotebookVisualizationSchema(ctx context.Context, nb *notebook.Notebook, cell *notebook.Cell) presentation.ResolvedSchema {
	return s.resolveNotebookVisualizationSchemaWithRuntime(ctx, nb, cell, true)
}

func (s *NotebookService) resolveNotebookVisualizationSchemaWithRuntime(ctx context.Context, nb *notebook.Notebook, cell *notebook.Cell, hydrate bool) presentation.ResolvedSchema {
	resolved := presentation.ResolvedSchema{
		Source: presentation.DataSourceRef{
			Kind: "notebook", ArtifactID: nb.UUID, ComponentID: cell.ID,
		},
		Columns: []presentation.ResolvedColumn{},
	}
	columnIndexes := map[string]int{}
	mergeColumn := func(name, physicalType string, nullable *bool) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if index, ok := columnIndexes[key]; ok {
			current := &resolved.Columns[index]
			if strings.TrimSpace(current.PhysicalType) == "" && strings.TrimSpace(physicalType) != "" {
				current.PhysicalType = strings.TrimSpace(physicalType)
				current.SemanticType = presentation.SemanticTypeForPhysicalType(current.PhysicalType)
			}
			if current.Nullable == nil && nullable != nil {
				value := *nullable
				current.Nullable = &value
			}
			return
		}
		columnIndexes[key] = len(resolved.Columns)
		resolved.Columns = append(resolved.Columns, presentation.ResolvedColumn{
			Name: name, PhysicalType: strings.TrimSpace(physicalType),
			SemanticType: presentation.SemanticTypeForPhysicalType(physicalType), Nullable: nullable,
		})
	}

	// Declarations are the author's primary contract.
	for _, column := range cell.Asset.Columns {
		var nullable *bool
		if column.Nullable.Value != nil {
			value := *column.Nullable.Value
			nullable = &value
		}
		mergeColumn(column.Name, column.SQLType(), nullable)
	}

	// Static inference fills declaration gaps without needing a live query.
	rt := s.runtimes.get(nb.UUID)
	schemaTables := s.buildCellSchemaTables(nb, cell, rt)
	staticSchema := make(sqlintelligence.Schema, len(schemaTables))
	for _, table := range schemaTables {
		columns := make(map[string]string, len(table.Columns))
		for _, column := range table.Columns {
			columns[column.Name] = column.Type
		}
		staticSchema[table.Name] = columns
	}
	if dialect, err := AssetTypeToDialect(cell.Asset.Type); err == nil {
		if inference, inferErr := sqlintelligence.InferOutputSchema(ctx, cell.Asset.ExecutableFile.Content, dialect, staticSchema); inferErr == nil {
			for _, column := range inference.Columns {
				mergeColumn(column.Name, column.Type, column.Nullable)
			}
		}
	}

	// A successful runtime observation supplies physical types that static SQL
	// cannot know (especially Python and remote source results) and the honest
	// complete/sample state used by presentation checks.
	if hydrate {
		s.hydrateRuntime(nb)
	}
	rt.mu.Lock()
	run, hasRun := rt.results[cell.ID]
	rt.mu.Unlock()
	if hasRun && run.Status == notebook.CellRunOK {
		for index, name := range run.Columns {
			physicalType := ""
			if index < len(run.ColumnTypes) {
				physicalType = run.ColumnTypes[index]
			}
			mergeColumn(name, physicalType, nil)
		}
		resolved.Sampled = run.Sampled
		resolved.Complete = !run.Sampled
		if run.Snapshot != nil {
			resolved.Sampled = run.Snapshot.Sampled
			resolved.Complete = run.Snapshot.Complete
		}
	}
	return resolved
}

func (s *NotebookService) notebookVisualizationProblems(ctx context.Context, nb *notebook.Notebook) (problems, blocking []string) {
	for _, block := range nb.Blocks {
		if block.Visualization == nil {
			continue
		}
		definition, findings := presentation.DecodeVisualizationDefinition(block.Visualization.Definition)
		cell := nb.CellByID(block.Visualization.Source)
		if cell == nil {
			findings = append(findings, presentation.Finding{
				Code: "visualization-source-missing", Severity: "error", Path: "source",
				Message: fmt.Sprintf("Source cell %q does not exist in this notebook.", block.Visualization.Source),
			})
		} else if !hasPresentationErrors(findings) {
			schema := s.resolveNotebookVisualizationSchemaWithRuntime(ctx, nb, cell, false)
			findings = append(findings, (presentation.Checker{}).CheckVisualization(ctx, definition, schema, presentation.CheckOptions{})...)
		}
		for _, finding := range findings {
			message := fmt.Sprintf("visualization %q: %s", block.StableID(), finding.Message)
			problems = append(problems, message)
			if strings.EqualFold(finding.Severity, "error") || strings.EqualFold(finding.Severity, "fatal") {
				blocking = append(blocking, message)
			}
		}
	}
	return problems, blocking
}

func hasPresentationErrors(findings []presentation.Finding) bool {
	for _, finding := range findings {
		if strings.EqualFold(finding.Severity, "error") || strings.EqualFold(finding.Severity, "fatal") {
			return true
		}
	}
	return false
}
