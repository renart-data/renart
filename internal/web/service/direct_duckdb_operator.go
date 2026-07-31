package service

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/bruin-data/bruin/pkg/ansisql"
	"github.com/bruin-data/bruin/pkg/config"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/bruin-data/bruin/pkg/scheduler"

	"renart/internal/web/duckcoord"
	"renart/internal/web/duckdbsession"
)

type duckDBStringMaterializer interface {
	Render(*pipeline.Asset, string) (string, error)
	LogIfFullRefreshAndDDL(interface{}, *pipeline.Asset) error
}

// directDuckDBOperator owns DuckDB coordination for native main tasks.
// Eligible assets execute through a shared Database instance; everything else
// retains Bruin's established operator behind the whole-file lease.
type directDuckDBOperator struct {
	manager                 config.ConnectionAndDetailsGetter
	extractor               query.QueryExtractor
	materializer            duckDBStringMaterializer
	fallback                bruinexecutor.Operator
	sessions                *duckdbsession.Manager
	coordinator             *duckcoord.Coordinator
	cfg                     *config.Config
	workspaceRoot           string
	disableFilesystemAccess bool
}

func (o *directDuckDBOperator) Run(ctx context.Context, instance scheduler.TaskInstance) error {
	if instance == nil || instance.GetAsset() == nil {
		return fmt.Errorf("DuckDB execution requires an asset")
	}
	asset := instance.GetAsset()
	pl := instance.GetPipeline()
	path, eligible := concurrentNativeDuckDBPath(o.workspaceRoot, o.cfg, pl, asset)
	if !eligible {
		return o.runFallback(ctx, instance, path)
	}
	return o.runConcurrent(ctx, pl, asset, path)
}

func (o *directDuckDBOperator) runConcurrent(
	ctx context.Context,
	pl *pipeline.Pipeline,
	asset *pipeline.Asset,
	path string,
) error {
	extractor, err := o.extractor.CloneForAsset(ctx, pl, asset)
	if err != nil {
		return fmt.Errorf("failed to clone extractor for asset %s: %w", asset.Name, err)
	}
	queries, err := extractor.ExtractQueriesFromString(asset.ExecutableFile.Content)
	if err != nil {
		return fmt.Errorf("cannot extract queries from the task file: %w", err)
	}
	if len(queries) == 0 {
		return nil
	}
	if len(queries) > 1 && asset.Materialization.Type != pipeline.MaterializationTypeNone {
		return fmt.Errorf("cannot enable materialization for tasks with multiple queries")
	}

	writer := ctx.Value(bruinexecutor.KeyPrinter)
	if err := o.materializer.LogIfFullRefreshAndDDL(writer, asset); err != nil {
		return err
	}
	materialized, err := o.materializer.Render(asset, queries[0].String())
	if err != nil {
		return err
	}
	if asset.Materialization.Strategy == pipeline.MaterializationStrategyTimeInterval {
		renderedQueries, extractErr := extractor.ExtractQueriesFromString(materialized)
		if extractErr != nil {
			return fmt.Errorf("cannot re-extract/render materialized query for time_interval strategy: %w", extractErr)
		}
		if len(renderedQueries) == 0 {
			return fmt.Errorf("rendered queries unexpectedly empty")
		}
		materialized = renderedQueries[0].Query
	}

	ansisql.LogQueryIfVerbose(ctx, writer, materialized)
	owner := directTaskLeaseOwner(ctx, pl, asset)
	if owner.OnWait == nil {
		if output, ok := writer.(io.Writer); ok {
			owner.OnWait = func(databasePath string) {
				_, _ = fmt.Fprintf(output, "Waiting for DuckDB database %s to become available...\n", filepath.Base(databasePath))
			}
		}
	}
	return o.sessions.Execute(ctx, duckdbsession.Request{
		Path:                    path,
		WorkspaceRoot:           o.workspaceRoot,
		AssetName:               asset.Name,
		SQL:                     materialized,
		DisableFilesystemAccess: o.disableFilesystemAccess,
		Owner:                   owner,
	})
}

func (o *directDuckDBOperator) runFallback(
	ctx context.Context,
	instance scheduler.TaskInstance,
	resolvedPath string,
) error {
	if o.fallback == nil {
		return fmt.Errorf("DuckDB fallback operator is unavailable")
	}
	path := resolvedPath
	if path == "" {
		asset := instance.GetAsset()
		pl := instance.GetPipeline()
		connectionName, err := pl.GetConnectionNameForAsset(asset)
		if err != nil {
			return err
		}
		rawPath, ok := duckDBConnectionPath(o.manager.GetConnectionDetails(connectionName))
		if ok {
			path, err = duckcoord.CanonicalPath(o.workspaceRoot, rawPath)
			if err != nil {
				return err
			}
		}
	}

	owner := directTaskLeaseOwner(ctx, instance.GetPipeline(), instance.GetAsset())
	if owner.OnWait == nil {
		if output, ok := ctx.Value(bruinexecutor.KeyPrinter).(io.Writer); ok {
			owner.OnWait = func(databasePath string) {
				_, _ = fmt.Fprintf(output, "Waiting for DuckDB database %s to become available...\n", filepath.Base(databasePath))
			}
		}
	}
	lease, err := o.coordinator.Acquire(ctx, []string{path}, owner)
	if err != nil {
		return err
	}
	defer lease.Release()
	return o.fallback.Run(ctx, instance)
}

func concurrentNativeDuckDBPath(
	workspaceRoot string,
	cfg *config.Config,
	pl *pipeline.Pipeline,
	asset *pipeline.Asset,
) (string, bool) {
	if pl == nil || asset == nil {
		return "", false
	}
	connectionName, err := pl.GetConnectionNameForAsset(asset)
	if err != nil || strings.TrimSpace(connectionName) == "" {
		return "", false
	}
	connection, ok := selectedConfigurationConnection(cfg, connectionName)
	if !ok {
		return "", false
	}
	coordinates, _, _, exact := resolveRelationTargetCoordinates(
		workspaceRoot,
		"duckdb",
		connection,
		asset.Name,
	)
	if !exact || !isConcurrentNativeDuckDBTarget(cfg, asset, connection, coordinates) {
		return coordinates.FilePath, false
	}
	return coordinates.FilePath, true
}
