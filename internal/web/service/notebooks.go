package service

import (
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/bruincompat"
	"renart/internal/sqlintelligence"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/notebookdoc"
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

	usedTables := func(sql, assetType string) ([]string, error) {
		dialect, dialectErr := bruincompat.AssetTypeToDialect(pipeline.AssetType(assetType))
		if dialectErr != nil || dialect == "" {
			dialect = "duckdb"
		}
		return sqlintelligence.UsedTables(sql, dialect)
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
	return notebookdoc.ToModel(s.workspaceRoot, nb, notebookdoc.ModelMetadata{
		Dependencies:     readNotebookDependencies(nb.Dir),
		InstalledModules: notebookInstalledModules(notebookVenvDir(s.workspaceRoot, nb.Dir)),
	})
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
