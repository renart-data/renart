package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	"renart/internal/web/dependencygraph"
	"renart/internal/web/fingerprint"
	"renart/internal/web/snapshot"
)

type snapshotPrerequisiteWorkspace struct {
	graph       dependencygraph.Graph
	vars        map[string]fingerprint.Vars
	deployments map[string]snapshotPrerequisiteDeployment
	cleanup     func()
}

type snapshotPrerequisiteDeployment struct {
	selection PipelinePlanProducerDeployment
	snapshot  snapshot.Snapshot
	parsed    *pipeline.Pipeline
}

func (s *PipelinePlanService) addSnapshotCrossPipelinePrerequisites(
	ctx context.Context,
	plan *PipelinePlan,
	resolved *resolvedPipelinePlanSource,
	selected selectedPipelinePlanAssets,
	cfg *config.Config,
	timeWindow ExecutionTimeWindow,
) {
	consumerIDs := make(map[string]struct{}, len(selected.items))
	for _, item := range selected.items {
		if item.asset != nil {
			consumerIDs[plan.PipelineUUID+":"+item.asset.Name] = struct{}{}
		}
	}
	if s.deps.Snapshots == nil || s.deps.ResolveProducerDeployment == nil ||
		s.deps.Fingerprints == nil || s.deps.Materializations == nil {
		s.addCrossPipelinePrerequisiteUnavailable(
			plan,
			pipelinePlanCodeCrossPipelineSnapshotPending,
			"cross-pipeline snapshot prerequisite resolution is unavailable",
			resolved.parsed,
			consumerIDs,
		)
		return
	}

	workspace, err := s.resolveSnapshotPrerequisiteWorkspace(ctx, plan, resolved)
	if err != nil {
		s.addCrossPipelinePrerequisiteUnavailable(
			plan,
			pipelinePlanCodeCrossPipelinePrerequisiteNotReady,
			err.Error(),
			resolved.parsed,
			consumerIDs,
		)
		return
	}
	defer workspace.cleanup()
	results, err := s.deps.Fingerprints.WorkspaceDAG(workspace.graph, workspace.vars)
	if err != nil {
		s.addCrossPipelinePrerequisiteUnavailable(
			plan,
			pipelinePlanCodeCrossPipelinePrerequisiteNotReady,
			"cross-pipeline deployment fingerprints could not be computed",
			resolved.parsed,
			consumerIDs,
		)
		return
	}

	items := append([]snapshot.DependencyManifestItem(nil), resolved.dependencyManifest.Dependencies...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].ConsumerAssetID != items[j].ConsumerAssetID {
			return items[i].ConsumerAssetID < items[j].ConsumerAssetID
		}
		return items[i].URI < items[j].URI
	})
	for _, manifestItem := range items {
		if manifestItem.Mode != "full" {
			continue
		}
		if _, selectedConsumer := consumerIDs[manifestItem.ConsumerAssetID]; !selectedConsumer {
			continue
		}
		consumer := workspace.graph.Nodes[manifestItem.ConsumerAssetID]
		prerequisite := PipelinePlanPrerequisite{
			Status:          PipelinePlanPrerequisiteBlocked,
			ConsumerAssetID: manifestItem.ConsumerAssetID,
			URI:             manifestItem.URI, Environment: plan.Context.Environment,
			RequiredStart: timeWindow.StartRFC3339(), RequiredEnd: timeWindow.EndRFC3339(),
			RequiredSeconds: timeWindow.End.Sub(timeWindow.Start).Seconds(),
		}
		if consumer != nil {
			prerequisite.ConsumerAssetName = consumer.AssetName
		}
		edge, ok := snapshotManifestGraphEdge(workspace.graph, manifestItem)
		if !ok || !edge.Resolved {
			prerequisite.Reason = fmt.Sprintf("deployed URI %q no longer resolves inside its bound producer deployment", manifestItem.URI)
			s.appendBlockedPrerequisite(plan, prerequisite)
			continue
		}
		producer := workspace.graph.Nodes[edge.ProducerID]
		deployment, ok := workspace.deployments[manifestItem.ProducerPipelineUUID]
		if producer == nil || !ok || producer.PipelineUUID != manifestItem.ProducerPipelineUUID {
			prerequisite.Reason = "the deployment-bound producer is unavailable"
			s.appendBlockedPrerequisite(plan, prerequisite)
			continue
		}
		prerequisite.ProducerPipelineID = deployment.selection.PipelineID
		prerequisite.ProducerPipelineUUID = producer.PipelineUUID
		prerequisite.ProducerPipelineName = deployment.selection.PipelineName
		prerequisite.ProducerAssetID = producer.AssetID
		prerequisite.ProducerAssetName = producer.AssetName
		prerequisite.ProducerSnapshotVersionID = deployment.snapshot.VersionID
		prerequisite.ProducerDeploymentOrdinal = deployment.snapshot.Ordinal
		if isSourceAssetType(producer.Asset.Type) {
			prerequisite.Reason = "external source assets do not produce Renart freshness evidence; use a symbolic dependency for lineage-only links"
			s.appendBlockedPrerequisite(plan, prerequisite)
			continue
		}
		if isSensorAssetType(producer.Asset.Type) {
			prerequisite.Reason = "sensor assets cannot satisfy a materialization prerequisite"
			s.appendBlockedPrerequisite(plan, prerequisite)
			continue
		}
		result, ok := results[producer.AssetID]
		if !ok {
			prerequisite.Reason = "the deployed producer fingerprint is unavailable"
			s.appendBlockedPrerequisite(plan, prerequisite)
			continue
		}
		prerequisite.ExpectedFingerprint = string(result.FP)
		prerequisite.VarsHash = fingerprint.AllVarsHash(workspace.vars[producer.PipelineUUID])
		producerTarget := resolveAssetPhysicalTarget(s.deps.WorkspaceRoot, &directPipelineInfo{
			Pipeline: producer.Pipeline, Asset: producer.Asset, Config: cfg,
		})
		if producerTarget.Fidelity != AssetRenderFidelityExact || strings.TrimSpace(producerTarget.Identity) == "" {
			prerequisite.Reason = "the deployed producer physical target cannot be resolved exactly"
			s.appendBlockedPrerequisite(plan, prerequisite)
			continue
		}
		prerequisite.TargetIdentity = producerTarget.Identity
		ready, reason, writer, covered, readyErr := s.crossPipelineProducerReady(
			ctx, producer, prerequisite, timeWindow,
		)
		prerequisite.CoveredSeconds = covered
		if readyErr != nil {
			prerequisite.Reason = "Renart materialization evidence could not be read"
			s.appendBlockedPrerequisite(plan, prerequisite)
			continue
		}
		if !ready {
			prerequisite.Reason = reason
			s.appendBlockedPrerequisite(plan, prerequisite)
			continue
		}
		prerequisite.Status = PipelinePlanPrerequisiteReady
		prerequisite.Reason = "Renart observed the deployed producer output with complete required coverage"
		prerequisite.TargetGeneration = writer.TargetGeneration
		prerequisite.WriterRunID = writer.RunID
		prerequisite.WriterSnapshotVersionID = writer.SnapshotVersionID
		prerequisite.WriterCompletionID = writer.CompletionID
		prerequisite.WriterCompletionOrdinal = writer.CompletionOrdinal
		prerequisite.WriterMaterializedAt = writer.MaterializedAt.UTC().Format(time.RFC3339Nano)
		plan.Prerequisites = append(plan.Prerequisites, prerequisite)
		if !deployment.selection.ScheduleFound || deployment.selection.ScheduleStatus != "active" {
			message := fmt.Sprintf("Producer %s has no active %s schedule; a matching manual Renart run still satisfies this prerequisite", producer.AssetName, plan.Context.Environment)
			plan.Readiness.Warnings = append(plan.Readiness.Warnings, PipelinePlanIssue{
				Code: "cross-pipeline-producer-schedule-inactive", Severity: "warning", Message: message,
				AssetID: prerequisite.ConsumerAssetID, AssetName: prerequisite.ConsumerAssetName,
			})
		}
	}
	sortPipelinePlanPrerequisites(plan.Prerequisites)
}

func (s *PipelinePlanService) resolveSnapshotPrerequisiteWorkspace(
	ctx context.Context,
	plan *PipelinePlan,
	consumer *resolvedPipelinePlanSource,
) (snapshotPrerequisiteWorkspace, error) {
	root, err := os.MkdirTemp("", "renart-prerequisite-plan-")
	if err != nil {
		return snapshotPrerequisiteWorkspace{}, fmt.Errorf("prepare producer deployments: %w", err)
	}
	workspace := snapshotPrerequisiteWorkspace{
		vars: make(map[string]fingerprint.Vars), deployments: make(map[string]snapshotPrerequisiteDeployment),
		cleanup: func() { _ = os.RemoveAll(root) },
	}
	fail := func(err error) (snapshotPrerequisiteWorkspace, error) {
		workspace.cleanup()
		return snapshotPrerequisiteWorkspace{}, err
	}
	consumerDeployment := snapshotPrerequisiteDeployment{
		selection: PipelinePlanProducerDeployment{
			PipelineID: plan.PipelineID, PipelineName: consumer.parsed.Name,
			SnapshotVersionID: consumer.source.VersionID, VariableOverrides: nil,
		},
		snapshot: snapshot.Snapshot{
			VersionID: consumer.source.VersionID, PipelineUUID: plan.PipelineUUID,
			Ordinal: consumer.source.DeploymentOrdinal, DependencyManifest: consumer.dependencyManifest,
		},
		parsed: consumer.parsed,
	}
	workspace.deployments[plan.PipelineUUID] = consumerDeployment
	workspace.vars[plan.PipelineUUID] = fingerprint.EffectiveVars(consumer.parsed, nil)

	queue := []string{plan.PipelineUUID}
	for len(queue) > 0 {
		pipelineUUID := queue[0]
		queue = queue[1:]
		current := workspace.deployments[pipelineUUID]
		if err := validateParsedSnapshotDependencyManifest(current.parsed, current.snapshot.DependencyManifest); err != nil {
			return fail(fmt.Errorf("deployment %s dependency manifest does not match its source: %w", current.snapshot.VersionID, err))
		}
		for _, item := range current.snapshot.DependencyManifest.Dependencies {
			producerUUID := strings.TrimSpace(item.ProducerPipelineUUID)
			if producerUUID == "" {
				continue
			}
			if _, loaded := workspace.deployments[producerUUID]; loaded {
				continue
			}
			selection, resolveErr := s.deps.ResolveProducerDeployment(ctx, producerUUID, plan.Context.Environment)
			if resolveErr != nil {
				return fail(fmt.Errorf("resolve producer deployment for URI %q: %w", item.URI, resolveErr))
			}
			if strings.TrimSpace(selection.SnapshotVersionID) == "" {
				return fail(fmt.Errorf("producer pipeline %s has no deployment for environment %s", producerUUID, plan.Context.Environment))
			}
			deployed, validateErr := s.deps.Snapshots.ValidateMetadata(ctx, selection.SnapshotVersionID, producerUUID)
			if validateErr != nil {
				return fail(fmt.Errorf("producer deployment %s is unavailable: %w", selection.SnapshotVersionID, validateErr))
			}
			pipelineDir := filepath.Join(root, producerUUID)
			if mkdirErr := os.MkdirAll(pipelineDir, 0o755); mkdirErr != nil {
				return fail(mkdirErr)
			}
			if materializeErr := s.deps.Snapshots.MaterializeForPipelineExecution(
				ctx, deployed.VersionID, producerUUID, pipelineDir,
			); materializeErr != nil {
				return fail(fmt.Errorf("materialize producer deployment %s: %w", deployed.VersionID, materializeErr))
			}
			builder := NewRenartPipelineBuilder(afero.NewOsFs())
			if overrideErr := addVariableOverrides(builder, selection.VariableOverrides); overrideErr != nil {
				return fail(fmt.Errorf("producer deployment variables are invalid: %w", overrideErr))
			}
			parsed, parseErr := builder.CreatePipelineFromPath(ctx, pipelineDir, pipeline.WithMutate())
			if parseErr != nil {
				return fail(fmt.Errorf("parse producer deployment %s: %w", deployed.VersionID, parseErr))
			}
			if strings.TrimSpace(parsed.LegacyID) != producerUUID {
				return fail(fmt.Errorf("producer deployment %s has a mismatched pipeline identity", deployed.VersionID))
			}
			workspace.deployments[producerUUID] = snapshotPrerequisiteDeployment{
				selection: selection, snapshot: deployed, parsed: parsed,
			}
			workspace.vars[producerUUID] = fingerprint.EffectiveVars(parsed, nil)
			queue = append(queue, producerUUID)
		}
	}

	inputs := make([]dependencygraph.PipelineInput, 0, len(workspace.deployments))
	for pipelineUUID, deployment := range workspace.deployments {
		inputs = append(inputs, dependencygraph.PipelineInput{
			UUID: pipelineUUID, ID: deployment.selection.PipelineID,
			Name: deployment.selection.PipelineName, Parsed: deployment.parsed,
		})
	}
	workspace.graph = dependencygraph.Resolve(inputs)
	for _, diagnostic := range workspace.graph.Diagnostics {
		if diagnostic.Severity == dependencygraph.SeverityError {
			return fail(fmt.Errorf("deployment dependency graph is invalid: %s", diagnostic.Message))
		}
	}
	for _, deployment := range workspace.deployments {
		for _, item := range deployment.snapshot.DependencyManifest.Dependencies {
			if item.ProducerPipelineUUID == "" {
				continue
			}
			edge, ok := snapshotManifestGraphEdge(workspace.graph, item)
			if !ok || !edge.Resolved {
				return fail(fmt.Errorf("deployed URI %q does not resolve in producer deployment %s", item.URI, deployment.snapshot.VersionID))
			}
			producer := workspace.graph.Nodes[edge.ProducerID]
			if producer == nil || producer.PipelineUUID != item.ProducerPipelineUUID || producer.URI != item.ProducerAssetURI {
				return fail(fmt.Errorf("deployed URI %q changed ownership from pipeline %s", item.URI, item.ProducerPipelineUUID))
			}
		}
	}
	return workspace, nil
}

func validateParsedSnapshotDependencyManifest(parsed *pipeline.Pipeline, manifest snapshot.DependencyManifest) error {
	if parsed == nil {
		return fmt.Errorf("pipeline source is empty")
	}
	expected := make(map[string]struct{})
	for _, asset := range parsed.Assets {
		if asset == nil {
			continue
		}
		consumerID := strings.TrimSpace(parsed.LegacyID) + ":" + asset.Name
		for _, upstream := range asset.Upstreams {
			if !strings.EqualFold(strings.TrimSpace(upstream.Type), "uri") {
				continue
			}
			mode := upstream.Mode.String()
			if mode == "" {
				mode = "full"
			}
			expected[consumerID+"\x00"+strings.TrimSpace(upstream.Value)+"\x00"+mode] = struct{}{}
		}
	}
	actual := make(map[string]struct{}, len(manifest.Dependencies))
	for _, item := range manifest.Dependencies {
		actual[item.ConsumerAssetID+"\x00"+item.URI+"\x00"+item.Mode] = struct{}{}
	}
	if len(expected) != len(actual) {
		return fmt.Errorf("expected %d URI edges, manifest contains %d", len(expected), len(actual))
	}
	for key := range expected {
		if _, ok := actual[key]; !ok {
			return fmt.Errorf("URI edge %q is missing", strings.ReplaceAll(key, "\x00", " / "))
		}
	}
	return nil
}

func snapshotManifestGraphEdge(graph dependencygraph.Graph, item snapshot.DependencyManifestItem) (dependencygraph.Edge, bool) {
	for _, edge := range graph.EdgesByConsumer[item.ConsumerAssetID] {
		if edge.Type == "uri" && edge.Value == item.URI && edge.Mode.String() == item.Mode {
			return edge, true
		}
	}
	return dependencygraph.Edge{}, false
}
