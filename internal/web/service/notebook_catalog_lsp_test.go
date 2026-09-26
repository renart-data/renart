package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/sqlintelligence"
	"renart/internal/sqllsp"
	"renart/internal/web/model"
)

func TestNotebookLSPResolvesQualifiedWarehouseSource(t *testing.T) {
	for _, reference := range []string{"marts.blub", "chess_playground.marts.blub", `"marts"."blub"`, `"chess_playground"."marts"."blub"`} {
		t.Run(reference, func(t *testing.T) {
			refs, err := sqlintelligence.UsedTables("select * from "+reference, "duckdb")
			require.NoError(t, err)
			state := model.WorkspaceState{
				Revision:            1,
				SelectedEnvironment: "default",
				Connections:         map[string]string{"duckdb-default": "duckdb"},
				Notebooks: []model.Notebook{{ID: "charts", Path: "notebooks/charts", Cells: []model.Asset{{
					ID: "crisp_stone", Name: "crisp_stone", CellID: "crisp", Class: "notebook",
					Type: "duckdb.sql", Connection: "duckdb-default", Path: "notebooks/charts/crisp_stone.sql",
					Content: "select * from " + reference, ExternalRefs: refs,
				}}}},
			}
			svc := notebookLSPService(t, state)
			response, apiErr := svc.Diagnostics(t.Context(), SQLLSPRequest{AssetID: "crisp_stone", Content: "select * from " + reference})
			require.Nil(t, apiErr)
			for _, diagnostic := range response.Diagnostics {
				require.NotEqual(t, "unresolved-relation", diagnostic.Code, "%+v", diagnostic)
			}
			// Keep explicitly different catalogs unresolved instead of silently
			// matching the known relation by its schema/table suffix.
			response, apiErr = svc.Diagnostics(t.Context(), SQLLSPRequest{
				AssetID: "crisp_stone", Content: `select * from "another_catalog"."marts"."blub"`,
			})
			require.Nil(t, apiErr)
			var unresolved bool
			for _, diagnostic := range response.Diagnostics {
				unresolved = unresolved || diagnostic.Code == "unresolved-relation"
			}
			require.True(t, unresolved, "an explicit catalog must not be discarded")
		})
	}
}

func TestNotebookLSPPreservesInferredPipelineColumns(t *testing.T) {
	for _, connection := range []string{"", "duckdb-default"} {
		t.Run("connection="+connection, func(t *testing.T) {
			state := notebookLSPState()
			asset := &state.Pipelines[0].Assets[0]
			asset.Columns = nil
			asset.Content = "select 1 as order_id, 2 as total_amount"
			state.Notebooks[0].Cells[1].Connection = connection
			svc := notebookLSPService(t, state)
			query := "select o.\nfrom analytics.orders o"
			response, apiErr := svc.Completions(t.Context(), SQLLSPRequest{
				AssetID: "nb1-summary", Content: query,
				Position: sqllsp.Position{Line: 0, Character: len("select o.")},
			})
			require.Nil(t, apiErr)
			labels := []string{}
			for _, item := range response.Completions {
				labels = append(labels, item.Label)
			}
			require.Contains(t, labels, "order_id")
			require.Contains(t, labels, "total_amount")

			// Extending one notebook must not mutate the revision-cached graph.
			response, apiErr = svc.Completions(t.Context(), SQLLSPRequest{
				AssetID: state.Notebooks[1].Cells[0].ID, Content: query,
				Position: sqllsp.Position{Line: 0, Character: len("select o.")},
			})
			require.Nil(t, apiErr)
			labels = nil
			for _, item := range response.Completions {
				labels = append(labels, item.Label)
			}
			require.Contains(t, labels, "total_amount")
		})
	}
}
