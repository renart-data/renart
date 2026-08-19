package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPresentationTypeChecksAreScopedToConsumedPipeline(t *testing.T) {
	parsed, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics", map[string]string{
		"orders.sql": `
/* @bruin
name: analytics.orders
type: duckdb.sql
columns:
  - name: id
    type: bigint
@bruin */
select 1::bigint as id
`,
	})
	require.NoError(t, os.MkdirAll(filepath.Join(root, "other", "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "other", "pipeline.yml"), []byte("id: other-uuid\nname: other\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "other", "assets", "accounts.sql"), []byte(`
/* @bruin
name: other.accounts
type: duckdb.sql
columns:
  - name: id
    type: bigint
@bruin */
select 1::bigint as id
`), 0o644))

	writeWorkspaceFile(t, root, "dashboards/orders.dashboard.yml", `
version: 1
id: orders
title: Orders
datasets:
  orders:
    asset: analytics.orders
visualizations:
  - id: total
    dataset: orders
    definition:
      version: 1
      type: metric
      value:
        field: missing_total
layout:
  - visualization: total
`)
	writeWorkspaceFile(t, root, "dashboards/unrelated.dashboard.yml", `
version: 1
id: unrelated
title: Unrelated
datasets:
  accounts:
    asset: other.accounts
visualizations:
  - id: total
    dataset: accounts
    definition:
      version: 1
      type: metric
      value:
        field: missing_total
layout:
  - visualization: total
`)

	tw, err := ResolveExecutionTimeWindow(string(parsed.Schedule), "", "", time.Now().UTC())
	require.NoError(t, err)
	report := CheckPipeline(context.Background(), afero.NewOsFs(), parsed, root, tw)
	state, err := NewWorkspaceService(root, filepath.Join(root, ".bruin.yml")).ComputeState(context.Background())
	require.NoError(t, err)
	AppendPresentationTypeChecks(
		context.Background(), afero.NewOsFs(), root, EncodeID("analytics"), state, &report,
	)

	require.Len(t, report.Presentations, 1)
	assert.Equal(t, "orders", report.Presentations[0].ID)
	assert.Equal(t, typeCheckStatusError, report.Presentations[0].Status)
	assert.Equal(t, 1, report.Summary.Presentations)
	assert.Greater(t, report.Summary.Errors, 0)
	assert.Equal(t, typeCheckStatusError, report.Status)
}
