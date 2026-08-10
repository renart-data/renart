package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/dependencygraph"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
)

func TestExecutionTargetSnapshotCapturesSecretFreeTargetAndFingerprintEvidence(t *testing.T) {
	t.Parallel()

	exact := materializedTargetAsset(pipeline.AssetTypePostgresQuery, "analytics.customers", "private-warehouse-alias")
	exact.ExecutableFile = pipeline.ExecutableFile{Content: "select {{ var.threshold }} as threshold"}
	runtimeOnly := &pipeline.Asset{
		Name:           "analytics.preview",
		Type:           pipeline.AssetTypePostgresQuery,
		Connection:     "private-warehouse-alias",
		ExecutableFile: pipeline.ExecutableFile{Content: "select 1 as value"},
	}
	pl := &pipeline.Pipeline{
		LegacyID:       "pipeline-target-snapshot-id",
		Name:           "analytics",
		DefinitionFile: pipeline.DefinitionFile{Path: filepath.Join(t.TempDir(), "pipeline.yml")},
		Assets:         []*pipeline.Asset{exact, runtimeOnly},
		Variables: pipeline.Variables{
			"threshold": {"type": "integer", "default": 7},
			"unused":    {"type": "string", "default": "stable"},
		},
	}
	cfg := &config.Config{
		SelectedEnvironmentName: "private-environment",
		SelectedEnvironment: &config.Environment{Connections: &config.Connections{
			Postgres: []config.PostgresConnection{{
				ConnectionMetadata: targetMetadata("private-warehouse-alias"),
				Host:               "private.pg.internal",
				Port:               5432,
				Database:           "warehouse_database",
				Schema:             "private_schema",
				Username:           "private-user",
				Password:           "super-secret-password",
			}},
		}},
	}
	executor := NewHybridBruinExecutor(t.TempDir(), "", nil, nil)

	snapshot, err := executor.resolveExecutionTargetSnapshot(pl, cfg, pl.Assets)
	require.NoError(t, err)
	require.Equal(t, ExecutionTargetSnapshotVersion, snapshot.Version)
	assert.Equal(t, pl.LegacyID, snapshot.PipelineUUID)
	expectedConfiguration := selectedPipelineConfigurationIdentity("", cfg, pl, pl.Assets)
	assert.Equal(t, expectedConfiguration.Digest, snapshot.ConfigurationDigest)
	assert.Equal(t, string(expectedConfiguration.Fidelity), snapshot.ConfigurationFidelity)
	assert.NotEmpty(t, snapshot.ConfigurationDigest)
	require.Len(t, snapshot.Entries, 2)

	vars := fingerprint.EffectiveVars(pl, nil)
	dag, err := fingerprint.NewEngine().DAG(pl, vars)
	require.NoError(t, err)
	varsHash := fingerprint.AllVarsHash(vars)
	for _, asset := range pl.Assets {
		assetID := identity.AssetID(pl.LegacyID, asset.Name)
		entry, ok := snapshot.Entries[asset.Name]
		require.True(t, ok)
		assert.Equal(t, assetID, entry.AssetID)
		assert.Equal(t, string(dag[assetID].FP), entry.Fingerprint)
		assert.Equal(t, string(dag[assetID].OwnContent), entry.OwnContent)
		assert.Equal(t, dag[assetID].ConsumedVarsHash, entry.ConsumedVarsHash)
		assert.Equal(t, varsHash, entry.VarsHash)
	}

	exactEntry := snapshot.Entries[exact.Name]
	assert.Equal(t, AssetRenderFidelityExact, exactEntry.TargetFidelity)
	assert.NotEmpty(t, exactEntry.TargetIdentity)
	assert.Equal(t, assetWriteResourceWarehouse, exactEntry.WriteResourceKind)
	assert.Equal(t, AssetRenderFidelityExact, exactEntry.WriteResourceFidelity)
	assert.NotEmpty(t, exactEntry.WriteResourceIdentity)
	runtimeEntry := snapshot.Entries[runtimeOnly.Name]
	assert.Equal(t, AssetRenderFidelityRuntimeOnly, runtimeEntry.TargetFidelity)
	assert.Empty(t, runtimeEntry.TargetIdentity)
	assert.Equal(t, assetWriteResourcePipeline, runtimeEntry.WriteResourceKind)
	assert.Equal(t, AssetRenderFidelityRuntimeOnly, runtimeEntry.WriteResourceFidelity)
	assert.Empty(t, runtimeEntry.WriteResourceIdentity)

	body, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var wire struct {
		Version int                       `json:"version"`
		Entries map[string]map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(body, &wire))
	require.Len(t, wire.Entries, 2)
	for name, entry := range wire.Entries {
		expectedKeys := []string{
			"asset_id",
			"target_identity",
			"target_fidelity",
			"write_resource_kind",
			"write_resource_fidelity",
			"execution_contract",
			"fingerprint",
			"own_content",
			"consumed_vars_hash",
			"vars_hash",
			"upstreams",
			"coverage_mode",
			"refresh_restricted",
		}
		if wireEntry := snapshot.Entries[name]; wireEntry.WriteResourceIdentity != "" {
			expectedKeys = append(expectedKeys, "write_resource_identity")
		}
		assert.ElementsMatch(t, expectedKeys, mapKeys(entry))
	}
	for _, secret := range []string{
		"private-environment",
		"private-warehouse-alias",
		"private.pg.internal",
		"warehouse_database",
		"private_schema",
		"private-user",
		"super-secret-password",
	} {
		assert.NotContains(t, string(body), secret)
	}
}

func TestExecutionTargetSnapshotRequiresOperatorEvidenceForPythonTable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	asset := materializedTargetAsset(pipeline.AssetTypePython, "analytics.python_table", "warehouse")
	asset.ExecutableFile = pipeline.ExecutableFile{Path: filepath.Join(root, "analytics", "assets", "python_table.py"), Content: "def materialize():\n    return dataframe\n"}
	pl := &pipeline.Pipeline{
		LegacyID: "pipeline-python-target-id", Name: "analytics",
		DefinitionFile: pipeline.DefinitionFile{Path: filepath.Join(root, "analytics", "pipeline.yml")},
		Assets:         []*pipeline.Asset{asset},
	}
	cfg := &config.Config{
		SelectedEnvironmentName: "default",
		SelectedEnvironment: &config.Environment{Connections: &config.Connections{
			DuckDB: []config.DuckDBConnection{{ConnectionMetadata: targetMetadata("warehouse"), Path: filepath.Join(root, "warehouse.duckdb")}},
		}},
	}

	snapshot, err := NewHybridBruinExecutor(root, "", nil, nil).resolveExecutionTargetSnapshot(pl, cfg, pl.Assets)
	require.NoError(t, err)
	entry := snapshot.Entries[asset.Name]
	assert.Equal(t, AssetRenderFidelityExact, entry.TargetFidelity)
	assert.NotEmpty(t, entry.TargetIdentity)
	assert.True(t, entry.TargetWriteEvidenceRequired)
}

func TestExecutionTargetSnapshotBindsReviewedCrossPipelinePrerequisite(t *testing.T) {
	root := t.TempDir()
	producerAsset := materializedTargetAsset(pipeline.AssetTypeDuckDBQuery, "raw.orders", "warehouse")
	producerAsset.URI = "duckdb://warehouse/raw/orders"
	producerAsset.ExecutableFile = pipeline.ExecutableFile{Content: "select 1 as id"}
	producer := &pipeline.Pipeline{
		LegacyID: "producer", Name: "raw",
		DefinitionFile: pipeline.DefinitionFile{Path: filepath.Join(root, "raw", "pipeline.yml")},
		Assets:         []*pipeline.Asset{producerAsset},
	}
	consumerAsset := materializedTargetAsset(pipeline.AssetTypeDuckDBQuery, "analytics.orders", "warehouse")
	consumerAsset.ExecutableFile = pipeline.ExecutableFile{Content: "select * from raw.orders"}
	consumerAsset.Upstreams = []pipeline.Upstream{{Type: "uri", Value: producerAsset.URI}}
	consumer := &pipeline.Pipeline{
		LegacyID: "consumer", Name: "analytics",
		DefinitionFile: pipeline.DefinitionFile{Path: filepath.Join(root, "analytics", "pipeline.yml")},
		Assets:         []*pipeline.Asset{consumerAsset},
	}
	resolveGraph := func(_ context.Context, overrides map[string]*pipeline.Pipeline) (dependencygraph.Graph, error) {
		selectedConsumer := consumer
		if overrides[consumer.LegacyID] != nil {
			selectedConsumer = overrides[consumer.LegacyID]
		}
		return dependencygraph.Resolve([]dependencygraph.PipelineInput{
			{UUID: producer.LegacyID, ID: "raw", Name: producer.Name, Parsed: producer},
			{UUID: consumer.LegacyID, ID: "analytics", Name: consumer.Name, Parsed: selectedConsumer},
		}), nil
	}
	cfg := &config.Config{
		SelectedEnvironmentName: "default",
		SelectedEnvironment: &config.Environment{Connections: &config.Connections{
			DuckDB: []config.DuckDBConnection{{ConnectionMetadata: targetMetadata("warehouse"), Path: filepath.Join(root, "warehouse.duckdb")}},
		}},
	}
	executor := NewHybridBruinExecutor(root, "", nil, nil)
	executor.SetDependencyGraphResolver(resolveGraph)
	graph, err := resolveGraph(context.Background(), nil)
	require.NoError(t, err)
	results, err := executor.fingerprintEngine.WorkspaceDAG(graph, workspaceFingerprintVars(graph, consumer.LegacyID, nil))
	require.NoError(t, err)
	producerID := identity.AssetID(producer.LegacyID, producerAsset.Name)
	producerTarget := resolveAssetPhysicalTarget(root, &directPipelineInfo{Pipeline: producer, Asset: producerAsset, Config: cfg})
	require.Equal(t, AssetRenderFidelityExact, producerTarget.Fidelity)
	prerequisite := PipelinePlanPrerequisite{
		Status:            PipelinePlanPrerequisiteReady,
		ConsumerAssetID:   identity.AssetID(consumer.LegacyID, consumerAsset.Name),
		ConsumerAssetName: consumerAsset.Name, URI: producerAsset.URI,
		ProducerPipelineUUID: producer.LegacyID, ProducerAssetID: producerID, Environment: "default",
		ExpectedFingerprint: string(results[producerID].FP), TargetIdentity: producerTarget.Identity,
		VarsHash:         fingerprint.AllVarsHash(fingerprint.EffectiveVars(producer, nil)),
		TargetGeneration: 1, WriterCompletionID: "producer-run", WriterCompletionOrdinal: 0,
	}

	snapshot, err := executor.resolveExecutionTargetSnapshotForReviewedSelection(
		consumer, cfg, consumer.Assets, consumer.Assets, []PipelinePlanPrerequisite{prerequisite},
	)
	require.NoError(t, err)
	entry := snapshot.Entries[consumerAsset.Name]
	require.Len(t, entry.Upstreams, 1)
	assert.True(t, entry.Upstreams[0].Required)
	assert.Equal(t, producerID, entry.Upstreams[0].ResolvedAssetID)
	assert.Equal(t, prerequisite.ExpectedFingerprint, entry.Upstreams[0].ExpectedFingerprint)
	assert.Empty(t, entry.Upstreams[0].ProducerPipelineUUID)
	assert.Empty(t, entry.Upstreams[0].ProducerSnapshotVersionID)
	local, err := executor.fingerprintEngine.DAG(consumer, fingerprint.EffectiveVars(consumer, nil))
	require.NoError(t, err)
	assert.NotEqual(t, local[entry.AssetID].FP, fingerprint.Fingerprint(entry.Fingerprint))

	producerAsset.ExecutableFile.Content = "select 1 as id, 2 as version"
	_, err = executor.resolveExecutionTargetSnapshotForReviewedSelection(
		consumer, cfg, consumer.Assets, consumer.Assets, []PipelinePlanPrerequisite{prerequisite},
	)
	assert.ErrorContains(t, err, "changed after plan confirmation")

	prerequisite.ProducerPipelineUUID = producer.LegacyID
	prerequisite.ProducerSnapshotVersionID = "producer-deployment-7"
	prerequisite.ProducerDeploymentOrdinal = 7
	validated := false
	executor.SetProducerDeploymentValidator(func(_ context.Context, pipelineUUID, versionID string) error {
		validated = true
		assert.Equal(t, producer.LegacyID, pipelineUUID)
		assert.Equal(t, prerequisite.ProducerSnapshotVersionID, versionID)
		return nil
	})
	deploymentBound, err := executor.resolveExecutionTargetSnapshotForReviewedSelection(
		consumer, cfg, consumer.Assets, consumer.Assets, []PipelinePlanPrerequisite{prerequisite},
	)
	require.NoError(t, err)
	assert.True(t, validated)
	deploymentUpstream := deploymentBound.Entries[consumerAsset.Name].Upstreams[0]
	assert.Equal(t, producer.LegacyID, deploymentUpstream.ProducerPipelineUUID)
	assert.Equal(t, prerequisite.ProducerSnapshotVersionID, deploymentUpstream.ProducerSnapshotVersionID)
	assert.Equal(t, string(results[identity.AssetID(consumer.LegacyID, consumerAsset.Name)].FP), deploymentBound.Entries[consumerAsset.Name].Fingerprint)

	executor.SetProducerDeploymentValidator(func(context.Context, string, string) error {
		return os.ErrNotExist
	})
	_, err = executor.resolveExecutionTargetSnapshotForReviewedSelection(
		consumer, cfg, consumer.Assets, consumer.Assets, []PipelinePlanPrerequisite{prerequisite},
	)
	assert.ErrorContains(t, err, "no longer executable")
}

func TestExecutionTargetSnapshotCallbackFailureStopsBeforeAnyTask(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, *HybridBruinExecutor, string, func(ExecutionTargetSnapshot) error, func(ExecutionAssetEvent) error) error
	}{
		{
			name: "asset",
			run: func(ctx context.Context, executor *HybridBruinExecutor, assetPath string, onTargets func(ExecutionTargetSnapshot) error, onAsset func(ExecutionAssetEvent) error) error {
				_, err := executor.RunAsset(ctx, RunAssetRequest{
					AssetPath:         assetPath,
					OnTargetsResolved: onTargets,
					AssetEvent:        onAsset,
				}, nil)
				return err
			},
		},
		{
			name: "pipeline",
			run: func(ctx context.Context, executor *HybridBruinExecutor, _ string, onTargets func(ExecutionTargetSnapshot) error, onAsset func(ExecutionAssetEvent) error) error {
				_, err := executor.RunPipeline(ctx, RunPipelineRequest{
					Target:            "analytics",
					OnTargetsResolved: onTargets,
					AssetEvent:        onAsset,
				}, nil)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspaceRoot, assetPath := createSuccessfulDuckDBWorkspace(t)
			addExecutionSnapshotPipelineID(t, workspaceRoot)
			executor, closeManager := executionSnapshotTestExecutor(t, workspaceRoot)
			defer closeManager()

			persistErr := errors.New("state database is unavailable")
			callbackCount := 0
			assetEvents := 0
			var captured ExecutionTargetSnapshot
			err := tc.run(context.Background(), executor, assetPath, func(snapshot ExecutionTargetSnapshot) error {
				callbackCount++
				captured = snapshot
				return persistErr
			}, func(ExecutionAssetEvent) error {
				assetEvents++
				return nil
			})

			require.ErrorIs(t, err, persistErr)
			assert.Equal(t, 1, callbackCount)
			assert.Zero(t, assetEvents)
			require.Contains(t, captured.Entries, "analytics.customers")
			assert.Equal(t, AssetRenderFidelityExact, captured.Entries["analytics.customers"].TargetFidelity)
			assert.False(t, executionSnapshotTableExists(t, executor), "the main materializer must not run")
		})
	}
}

func TestDirectRunAssetEventFailuresAreExecutionErrors(t *testing.T) {
	t.Run("running event aborts before the main task", func(t *testing.T) {
		workspaceRoot, assetPath := createSuccessfulDuckDBWorkspace(t)
		executor, closeManager := executionSnapshotTestExecutor(t, workspaceRoot)
		defer closeManager()
		persistErr := errors.New("running step was not durable")

		_, err := executor.RunAsset(context.Background(), RunAssetRequest{
			AssetPath: assetPath,
			AssetEvent: func(event ExecutionAssetEvent) error {
				if event.Status == "running" {
					return persistErr
				}
				return nil
			},
		}, nil)

		require.ErrorIs(t, err, persistErr)
		assert.False(t, executionSnapshotTableExists(t, executor))
	})

	t.Run("success event failure fails the completed execution", func(t *testing.T) {
		workspaceRoot, assetPath := createSuccessfulDuckDBWorkspace(t)
		executor, closeManager := executionSnapshotTestExecutor(t, workspaceRoot)
		defer closeManager()
		persistErr := errors.New("successful step was not durable")

		_, err := executor.RunAsset(context.Background(), RunAssetRequest{
			AssetPath: assetPath,
			AssetEvent: func(event ExecutionAssetEvent) error {
				if event.Status == "success" {
					return persistErr
				}
				return nil
			},
		}, nil)

		require.ErrorIs(t, err, persistErr)
		assert.True(t, executionSnapshotTableExists(t, executor), "the task completed before terminal persistence failed")
	})

	t.Run("failed event failure preserves both errors", func(t *testing.T) {
		workspaceRoot, assetPath := createSuccessfulDuckDBWorkspace(t)
		require.NoError(t, os.WriteFile(assetPath, []byte(strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: table
@bruin */

select * from a_table_that_does_not_exist
`)+"\n"), 0o644))
		executor, closeManager := executionSnapshotTestExecutor(t, workspaceRoot)
		defer closeManager()
		persistErr := errors.New("failed step was not durable")

		_, err := executor.RunAsset(context.Background(), RunAssetRequest{
			AssetPath: assetPath,
			AssetEvent: func(event ExecutionAssetEvent) error {
				if event.Status == "failed" {
					return persistErr
				}
				return nil
			},
		}, nil)

		require.ErrorIs(t, err, persistErr)
		assert.Contains(t, err.Error(), "a_table_that_does_not_exist")
	})
}

func TestDirectRunPipelineExecutesReviewedUnitsOnly(t *testing.T) {
	workspaceRoot, _ := createSuccessfulDuckDBWorkspace(t)
	addExecutionSnapshotPipelineID(t, workspaceRoot)
	require.NoError(t, os.WriteFile(
		filepath.Join(workspaceRoot, "analytics", "assets", "omitted.sql"),
		[]byte(strings.TrimSpace(`
/* @bruin
name: analytics.omitted
type: duckdb.sql
materialization:
  type: table
@bruin */

select * from a_table_that_does_not_exist
`)+"\n"),
		0o644,
	))
	executor, closeManager := executionSnapshotTestExecutor(t, workspaceRoot)
	defer closeManager()

	var snapshot ExecutionTargetSnapshot
	var assetEvents []ExecutionAssetEvent
	var unitEvents []PipelineExecutionUnitEvent
	_, err := executor.RunPipeline(context.Background(), RunPipelineRequest{
		Target:        "analytics",
		SelectionMode: PipelinePlanSelectionNeeded,
		ExecutionUnits: []PipelineExecutionUnit{{
			Position:  0,
			AssetID:   identity.AssetID("pipeline-target-callback-id", "analytics.customers"),
			AssetName: "analytics.customers",
			StartDate: "2026-07-17T10:00:00Z",
			EndDate:   "2026-07-17T11:00:00Z",
			Reason:    "missing_materialization",
		}},
		OnTargetsResolved: func(resolved ExecutionTargetSnapshot) error {
			snapshot = resolved
			return nil
		},
		AssetEvent: func(event ExecutionAssetEvent) error {
			assetEvents = append(assetEvents, event)
			return nil
		},
		UnitEvent: func(event PipelineExecutionUnitEvent) error {
			unitEvents = append(unitEvents, event)
			return nil
		},
	}, nil)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"analytics.customers", "analytics.omitted"}, executionSnapshotEntryNames(snapshot))
	require.Len(t, assetEvents, 2)
	for _, event := range assetEvents {
		assert.Equal(t, "analytics.customers", event.Asset)
		assert.True(t, event.HasUnitPosition)
		assert.Zero(t, event.UnitPosition)
	}
	require.Len(t, unitEvents, 2)
	assert.Equal(t, "running", unitEvents[0].Status)
	assert.Equal(t, "success", unitEvents[1].Status)
	assert.True(t, executionSnapshotTableNamedExists(t, executor, "customers"))
	assert.False(t, executionSnapshotTableNamedExists(t, executor, "omitted"))
}

func TestDirectRunPipelineExecutesRepeatedReviewedWindowsInOrder(t *testing.T) {
	workspaceRoot, _ := createSuccessfulDuckDBWorkspace(t)
	addExecutionSnapshotPipelineID(t, workspaceRoot)
	executor, closeManager := executionSnapshotTestExecutor(t, workspaceRoot)
	defer closeManager()

	units := []PipelineExecutionUnit{
		{
			Position:  0,
			AssetID:   identity.AssetID("pipeline-target-callback-id", "analytics.customers"),
			AssetName: "analytics.customers",
			StartDate: "2026-07-17T09:00:00Z",
			EndDate:   "2026-07-17T10:00:00Z",
			Reason:    "coverage_gap",
		},
		{
			Position:  1,
			AssetID:   identity.AssetID("pipeline-target-callback-id", "analytics.customers"),
			AssetName: "analytics.customers",
			StartDate: "2026-07-17T10:00:00Z",
			EndDate:   "2026-07-17T11:00:00Z",
			Reason:    "coverage_gap",
		},
	}
	var assetEvents []ExecutionAssetEvent
	var unitEvents []PipelineExecutionUnitEvent
	_, err := executor.RunPipeline(context.Background(), RunPipelineRequest{
		Target:         "analytics",
		SelectionMode:  PipelinePlanSelectionNeeded,
		ExecutionUnits: units,
		AssetEvent: func(event ExecutionAssetEvent) error {
			assetEvents = append(assetEvents, event)
			return nil
		},
		UnitEvent: func(event PipelineExecutionUnitEvent) error {
			unitEvents = append(unitEvents, event)
			return nil
		},
	}, nil)

	require.NoError(t, err)
	require.Len(t, unitEvents, 4)
	assert.Equal(t, []int{0, 0, 1, 1}, []int{
		unitEvents[0].Position, unitEvents[1].Position, unitEvents[2].Position, unitEvents[3].Position,
	})
	assert.Equal(t, []string{"running", "success", "running", "success"}, []string{
		unitEvents[0].Status, unitEvents[1].Status, unitEvents[2].Status, unitEvents[3].Status,
	})
	require.Len(t, assetEvents, 4)
	assert.Equal(t, []int{0, 0, 1, 1}, []int{
		assetEvents[0].UnitPosition, assetEvents[1].UnitPosition, assetEvents[2].UnitPosition, assetEvents[3].UnitPosition,
	})
}

func TestDirectRunPipelineAllowsReviewedNeededPlanToBecomeEmpty(t *testing.T) {
	workspaceRoot, _ := createSuccessfulDuckDBWorkspace(t)
	addExecutionSnapshotPipelineID(t, workspaceRoot)
	executor, closeManager := executionSnapshotTestExecutor(t, workspaceRoot)
	defer closeManager()

	targetCallbacks := 0
	assetEvents := 0
	unitEvents := 0
	output, err := executor.RunPipeline(context.Background(), RunPipelineRequest{
		Target:         "analytics",
		SelectionMode:  PipelinePlanSelectionNeeded,
		ExecutionUnits: nil,
		OnTargetsResolved: func(snapshot ExecutionTargetSnapshot) error {
			targetCallbacks++
			assert.Contains(t, snapshot.Entries, "analytics.customers")
			return nil
		},
		AssetEvent: func(ExecutionAssetEvent) error {
			assetEvents++
			return nil
		},
		UnitEvent: func(PipelineExecutionUnitEvent) error {
			unitEvents++
			return nil
		},
	}, nil)

	require.NoError(t, err)
	assert.Contains(t, string(output), "No reviewed execution units remain")
	assert.Equal(t, 1, targetCallbacks)
	assert.Zero(t, assetEvents)
	assert.Zero(t, unitEvents)
	assert.False(t, executionSnapshotTableExists(t, executor))
}

func TestHybridBruinExecutorSetFingerprintEngine(t *testing.T) {
	t.Parallel()
	executor := NewHybridBruinExecutor(t.TempDir(), "", nil, nil)
	shared := fingerprint.NewEngine()

	executor.SetFingerprintEngine(shared)
	assert.Same(t, shared, executor.fingerprintEngine)
	executor.SetFingerprintEngine(nil)
	assert.NotNil(t, executor.fingerprintEngine)
	assert.NotSame(t, shared, executor.fingerprintEngine)
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func addExecutionSnapshotPipelineID(t *testing.T, workspaceRoot string) {
	t.Helper()
	pipelinePath := filepath.Join(workspaceRoot, "analytics", "pipeline.yml")
	body, err := os.ReadFile(pipelinePath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(pipelinePath, append([]byte("id: pipeline-target-callback-id\n"), body...), 0o644))
}

func executionSnapshotTestExecutor(t *testing.T, workspaceRoot string) (*HybridBruinExecutor, func()) {
	t.Helper()
	cfg, err := loadSelectedConfig(filepath.Join(workspaceRoot, ".bruin.yml"), "")
	require.NoError(t, err)
	manager, err := newConnectionManagerFromConfig(context.Background(), cfg)
	require.NoError(t, err)
	executor := newCompatDirectExecutor(workspaceRoot, "")
	executor.newConnectionManager = func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
		return manager, nil
	}
	cleanup := func() {
		if connection, ok := manager.GetConnection("duckdb-default").(interface{ Close() }); ok {
			connection.Close()
		}
	}
	return executor, cleanup
}

func executionSnapshotTableExists(t *testing.T, executor *HybridBruinExecutor) bool {
	return executionSnapshotTableNamedExists(t, executor, "customers")
}

func executionSnapshotTableNamedExists(t *testing.T, executor *HybridBruinExecutor, tableName string) bool {
	t.Helper()
	body, err := executor.QueryConnection(context.Background(), QueryConnectionRequest{
		ConnectionName: "duckdb-default",
		Query:          "select count(*) from information_schema.tables where table_schema = 'analytics' and table_name = '" + tableName + "'",
		Output:         "json",
	})
	require.NoError(t, err)
	var payload struct {
		Rows [][]any `json:"rows"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, 1, len(payload.Rows))
	require.Equal(t, 1, len(payload.Rows[0]))
	count, ok := payload.Rows[0][0].(float64)
	require.True(t, ok)
	return count > 0
}

func executionSnapshotEntryNames(snapshot ExecutionTargetSnapshot) []string {
	names := make([]string, 0, len(snapshot.Entries))
	for name := range snapshot.Entries {
		names = append(names, name)
	}
	return names
}
