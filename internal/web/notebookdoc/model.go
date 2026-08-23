package notebookdoc

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/workspacefs"
)

// ToModel converts one freshly loaded authored notebook into the shared API
// DTO. Runtime-only Python environment metadata is supplied explicitly so the
// document model remains independent of the uv/session implementation.
func ToModel(workspaceRoot string, nb *notebook.Notebook, metadata ModelMetadata) model.Notebook {
	if nb == nil {
		return model.Notebook{}
	}
	relDir, err := filepath.Rel(workspaceRoot, nb.Dir)
	if err != nil {
		relDir = nb.Dir
	}

	result := model.Notebook{
		ID:               workspacefs.EncodePathID(filepath.ToSlash(relDir)),
		UUID:             nb.UUID,
		ManifestVersion:  nb.Version,
		Revision:         nb.Revision,
		Title:            nb.Title,
		Path:             filepath.ToSlash(relDir),
		Target:           nb.Target,
		Parameters:       make([]model.NotebookParameter, 0, len(nb.Parameters)),
		Blocks:           make([]model.NotebookBlock, 0, len(nb.Blocks)),
		Cells:            make([]model.Asset, 0, len(nb.Cells)),
		Problems:         append([]string(nil), nb.Problems...),
		Dependencies:     append([]string(nil), metadata.Dependencies...),
		InstalledModules: append([]string(nil), metadata.InstalledModules...),
	}
	for _, parameter := range nb.Parameters {
		converted := model.NotebookParameter{
			ID: parameter.ID, Label: parameter.Label, Type: string(parameter.Type), Default: parameter.Default,
			Min: parameter.Min, Max: parameter.Max, Step: parameter.Step,
		}
		if parameter.Options != nil {
			converted.Options = &model.NotebookParameterOptions{
				Values: append([]any(nil), parameter.Options.Values...), Dataset: parameter.Options.Dataset,
				ValueField: parameter.Options.ValueField, LabelField: parameter.Options.LabelField,
			}
		}
		result.Parameters = append(result.Parameters, converted)
	}

	for _, block := range nb.Blocks {
		converted := model.NotebookBlock{ID: block.ID, Cell: block.Cell, Markdown: block.Markdown, Control: block.Control}
		if block.Visualization != nil {
			converted.Visualization = &model.NotebookVisualization{
				ID: block.Visualization.ID, Source: block.Visualization.Source,
				Definition: cloneStringAnyMap(block.Visualization.Definition),
			}
		}
		result.Blocks = append(result.Blocks, converted)
	}

	for _, cell := range nb.Cells {
		if cell == nil || cell.Asset == nil {
			continue
		}
		relPath, relErr := filepath.Rel(workspaceRoot, cell.Path)
		if relErr != nil {
			relPath = cell.Path
		}
		upstreams := make([]string, 0, len(cell.Asset.Upstreams))
		for _, upstream := range cell.Asset.Upstreams {
			upstreams = append(upstreams, upstream.Value)
		}
		content := cell.Raw
		if strings.TrimSpace(content) == "" {
			content = cell.Asset.ExecutableFile.Content
			if strings.TrimSpace(content) == "" {
				if raw, readErr := os.ReadFile(cell.Path); readErr == nil {
					content = string(raw)
				}
			}
		}
		result.Cells = append(result.Cells, model.Asset{
			ID:                 workspacefs.EncodePathID(filepath.ToSlash(relPath)),
			Name:               cell.Asset.Name,
			Description:        strings.TrimSpace(cell.Asset.Description),
			Type:               string(cell.Asset.Type),
			Path:               filepath.ToSlash(relPath),
			Content:            content,
			ContentRevision:    notebook.ContentRevision(content),
			Upstreams:          upstreams,
			Meta:               cell.Asset.Meta,
			Columns:            columnsToModel(cell.Asset.Columns),
			CustomChecks:       customChecksToModel(cell.Asset.CustomChecks),
			Class:              notebook.ClassNotebook,
			CellID:             cell.ID,
			ExternalRefs:       append([]string(nil), cell.ExternalRefs...),
			NotebookSource:     sourceToModel(cell.Source),
			Connection:         cell.Asset.Connection,
			ExplicitConnection: cell.Asset.Connection,
		})
	}
	return result
}

func sourceToModel(source *notebook.SourceDefinition) *model.NotebookSourceDefinition {
	if source == nil {
		return nil
	}
	return &model.NotebookSourceDefinition{
		Version: source.Version, ID: source.ID, Kind: source.Kind,
		Connection: source.Connection, URI: source.URI, Format: source.Format,
		Request: model.NotebookSourceRequest{
			URL: source.Request.URL, Method: source.Request.Method, Headers: source.Request.Headers,
			Params: source.Request.Params, Body: source.Request.Body,
		},
		Response: model.NotebookSourceResponse{
			RecordsPath: source.Response.RecordsPath, Fields: source.Response.Fields,
		},
		Snapshot: model.NotebookSourceSnapshot{Mode: source.Snapshot.Mode, RowLimit: source.Snapshot.RowLimit},
	}
}

func columnsToModel(columns []pipeline.Column) []model.Column {
	result := make([]model.Column, 0, len(columns))
	for _, column := range columns {
		var nullable *bool
		if column.Nullable.Value != nil {
			value := *column.Nullable.Value
			nullable = &value
		}
		checks := make([]model.ColumnCheck, 0, len(column.Checks))
		for _, check := range column.Checks {
			checks = append(checks, model.ColumnCheck{
				Name: check.Name, Value: columnCheckValue(check.Value), Blocking: check.Blocking.Value,
				Description: check.Description,
			})
		}
		var foreignKey *model.ColumnReference
		if column.ForeignKey != nil {
			foreignKey = &model.ColumnReference{Table: column.ForeignKey.Table, Column: column.ForeignKey.Column}
		}
		result = append(result, model.Column{
			Name: column.Name, SourceColumn: column.SourceColumn, Type: column.Type, Mask: column.Mask,
			Description: column.Description, Tags: append([]string(nil), column.Tags...),
			PrimaryKey: column.PrimaryKey, UpdateOnMerge: column.UpdateOnMerge, MergeSQL: column.MergeSQL,
			Nullable: nullable, Default: column.Default, Precision: cloneInt(column.Precision),
			Scale: cloneInt(column.Scale), Length: cloneInt(column.Length), Collation: column.Collation,
			ForeignKey: foreignKey, Owner: column.Owner, Domains: append([]string(nil), column.Domains...),
			Meta: column.Meta, Checks: checks,
		})
	}
	return result
}

func customChecksToModel(checks []pipeline.CustomCheck) []model.CustomCheck {
	result := make([]model.CustomCheck, 0, len(checks))
	for _, check := range checks {
		result = append(result, model.CustomCheck{
			Name: check.Name, Description: check.Description, Value: check.Value, Count: check.Count,
			Blocking: check.Blocking.Value, Query: check.Query, Retries: check.Retries,
		})
	}
	return result
}

func columnCheckValue(value pipeline.ColumnCheckValue) any {
	switch {
	case value.IntArray != nil:
		return *value.IntArray
	case value.Int != nil:
		return *value.Int
	case value.Float != nil:
		return *value.Float
	case value.StringArray != nil:
		return *value.StringArray
	case value.String != nil:
		return *value.String
	case value.Bool != nil:
		return *value.Bool
	default:
		return nil
	}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
