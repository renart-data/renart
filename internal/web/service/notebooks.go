package service

import (
	"path/filepath"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/spf13/afero"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
)

// appendNotebooks discovers and loads every notebook folder in the
// workspace into the state. Cells are class-tagged assets; load failures
// degrade to state errors so a broken notebook never hides the rest of the
// workspace.
func (s *WorkspaceService) appendNotebooks(state *model.WorkspaceState) {
	fs := afero.NewOsFs()
	dirs, err := notebook.Discover(fs, s.workspaceRoot)
	if err != nil {
		state.Errors = append(state.Errors, "notebook discovery failed: "+err.Error())
		return
	}
	if len(dirs) == 0 {
		return
	}

	parser, parserErr := sqlparser.NewSQLParser(false)
	var usedTables notebook.UsedTablesFunc
	if parserErr == nil {
		defer parser.Close()
		usedTables = func(sql, assetType string) ([]string, error) {
			dialect, dialectErr := sqlparser.AssetTypeToDialect(pipeline.AssetType(assetType))
			if dialectErr != nil || dialect == "" {
				dialect = "duckdb"
			}
			return parser.UsedTables(sql, dialect)
		}
	} else {
		state.Errors = append(state.Errors, "notebook dependency scan unavailable: "+parserErr.Error())
	}

	loader := notebook.NewLoader(fs, pipeline.CreateTaskFromFileComments(fs), usedTables).
		WithWorkspaceRoot(s.workspaceRoot)
	for _, dir := range dirs {
		nb, loadErr := loader.Load(dir)
		if loadErr != nil {
			state.Errors = append(state.Errors, dir+": "+loadErr.Error())
			continue
		}
		state.Notebooks = append(state.Notebooks, s.notebookToModel(nb))
	}
}

func (s *WorkspaceService) notebookToModel(nb *notebook.Notebook) model.Notebook {
	relDir, err := filepath.Rel(s.workspaceRoot, nb.Dir)
	if err != nil {
		relDir = nb.Dir
	}

	result := model.Notebook{
		ID:               EncodeID(filepath.ToSlash(relDir)),
		UUID:             nb.UUID,
		ManifestVersion:  nb.Version,
		Revision:         nb.Revision,
		Title:            nb.Title,
		Path:             filepath.ToSlash(relDir),
		Target:           nb.Target,
		Parameters:       make([]model.NotebookParameter, 0, len(nb.Parameters)),
		Blocks:           make([]model.NotebookBlock, 0, len(nb.Blocks)),
		Cells:            make([]model.Asset, 0, len(nb.Cells)),
		Problems:         nb.Problems,
		Dependencies:     readNotebookDependencies(nb.Dir),
		InstalledModules: notebookInstalledModules(notebookVenvDir(s.workspaceRoot, nb.Dir)),
	}
	for _, parameter := range nb.Parameters {
		modelParameter := model.NotebookParameter{
			ID: parameter.ID, Label: parameter.Label, Type: string(parameter.Type), Default: parameter.Default,
			Min: parameter.Min, Max: parameter.Max, Step: parameter.Step,
		}
		if parameter.Options != nil {
			modelParameter.Options = &model.NotebookParameterOptions{
				Values: append([]any(nil), parameter.Options.Values...), Dataset: parameter.Options.Dataset,
				ValueField: parameter.Options.ValueField, LabelField: parameter.Options.LabelField,
			}
		}
		result.Parameters = append(result.Parameters, modelParameter)
	}

	for _, block := range nb.Blocks {
		modelBlock := model.NotebookBlock{ID: block.ID, Cell: block.Cell, Markdown: block.Markdown, Control: block.Control}
		if block.Visualization != nil {
			modelBlock.Visualization = &model.NotebookVisualization{
				ID:         block.Visualization.ID,
				Source:     block.Visualization.Source,
				Definition: cloneStringAnyMap(block.Visualization.Definition),
			}
		}
		result.Blocks = append(result.Blocks, modelBlock)
	}

	for _, cell := range nb.Cells {
		relPath, relErr := filepath.Rel(s.workspaceRoot, cell.Path)
		if relErr != nil {
			relPath = cell.Path
		}

		upstreams := make([]string, 0, len(cell.Asset.Upstreams))
		for _, upstream := range cell.Asset.Upstreams {
			upstreams = append(upstreams, upstream.Value)
		}

		// Raw is the authoritative on-disk snapshot (including an id inserted by
		// EnsureCellID after the Bruin asset was parsed). Expose and version the
		// same bytes so optimistic save preconditions survive parser rewrites.
		content := cell.Raw
		if strings.TrimSpace(content) == "" {
			content = cell.Asset.ExecutableFile.Content
			if strings.TrimSpace(content) == "" {
				if raw, readErr := afero.ReadFile(afero.NewOsFs(), cell.Path); readErr == nil {
					content = string(raw)
				}
			}
		}

		var sourceDefinition *model.NotebookSourceDefinition
		if cell.Source != nil {
			sourceDefinition = &model.NotebookSourceDefinition{
				Version: cell.Source.Version, ID: cell.Source.ID, Kind: cell.Source.Kind,
				Connection: cell.Source.Connection, URI: cell.Source.URI, Format: cell.Source.Format,
				Request: model.NotebookSourceRequest{
					URL: cell.Source.Request.URL, Method: cell.Source.Request.Method,
					Headers: cell.Source.Request.Headers, Params: cell.Source.Request.Params,
					Body: cell.Source.Request.Body,
				},
				Response: model.NotebookSourceResponse{
					RecordsPath: cell.Source.Response.RecordsPath, Fields: cell.Source.Response.Fields,
				},
				Snapshot: model.NotebookSourceSnapshot{
					Mode: cell.Source.Snapshot.Mode, RowLimit: cell.Source.Snapshot.RowLimit,
				},
			}
		}

		result.Cells = append(result.Cells, model.Asset{
			ID:                 EncodeID(filepath.ToSlash(relPath)),
			Name:               cell.Asset.Name,
			Description:        strings.TrimSpace(cell.Asset.Description),
			Type:               string(cell.Asset.Type),
			Path:               filepath.ToSlash(relPath),
			Content:            content,
			ContentRevision:    notebook.ContentRevision(content),
			Upstreams:          upstreams,
			Meta:               cell.Asset.Meta,
			Columns:            PipelineColumnsToModelColumns(cell.Asset.Columns),
			CustomChecks:       PipelineCustomChecksToModelCustomChecks(cell.Asset.CustomChecks),
			Class:              notebook.ClassNotebook,
			CellID:             cell.ID,
			ExternalRefs:       cell.ExternalRefs,
			NotebookSource:     sourceDefinition,
			Connection:         cell.Asset.Connection,
			ExplicitConnection: cell.Asset.Connection,
		})
	}

	return result
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
