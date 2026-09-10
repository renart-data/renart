package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/sqlintelligence"
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
