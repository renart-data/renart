package service

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/dependencygraph"
	"renart/internal/web/fingerprint"
	"renart/internal/web/matlog"
)

const (
	pipelinePlanCodeCrossPipelinePrerequisiteNotReady = "cross-pipeline-prerequisite-not-ready"
	pipelinePlanCodeCrossPipelineSnapshotPending      = "cross-pipeline-snapshot-prerequisites-pending"
)

func (s *PipelinePlanService) addCrossPipelinePrerequisites(
	ctx context.Context,
	plan *PipelinePlan,
	parsed *pipeline.Pipeline,
	selected selectedPipelinePlanAssets,
	cfg *config.Config,
	sourceKind string,
	purpose string,
	timeWindow ExecutionTimeWindow,
	variableOverrides map[string]any,
) {
	if plan == nil || parsed == nil || len(selected.items) == 0 {
		return
	}
	consumerIDs := make(map[string]struct{}, len(selected.items))
	for _, selectedAsset := range selected.items {
		if selectedAsset.asset != nil {
			consumerIDs[plan.PipelineUUID+":"+selectedAsset.asset.Name] = struct{}{}
		}
	}
	if !pipelineHasSelectedFullURIDependency(parsed, consumerIDs) {
		return
	}

	if purpose == PipelinePlanPurposeDeployment {
		// Deployment admission persists and validates the immutable URI-owner
		// manifest. Runtime writer/coverage evidence belongs to execution only.
		return
	}
	if purpose != PipelinePlanPurposeExecution || sourceKind != PipelinePlanSourceWorkingTree {
		s.addCrossPipelinePrerequisiteUnavailable(
			plan,
			pipelinePlanCodeCrossPipelineSnapshotPending,
			"cross-pipeline deployment bindings and scheduled snapshot prerequisites are not available yet",
			parsed,
			consumerIDs,
		)
		return
	}
	if s.deps.DependencyGraph == nil || s.deps.Fingerprints == nil || s.deps.Materializations == nil {
		s.addCrossPipelinePrerequisiteUnavailable(
			plan,
			pipelinePlanCodeCrossPipelinePrerequisiteNotReady,
			"cross-pipeline prerequisite evidence is unavailable",
			parsed,
			consumerIDs,
		)
		return
	}

	graph, err := s.deps.DependencyGraph(ctx, map[string]*pipeline.Pipeline{plan.PipelineUUID: parsed})
	if err != nil {
		s.addCrossPipelinePrerequisiteUnavailable(
			plan,
			pipelinePlanCodeCrossPipelinePrerequisiteNotReady,
			"cross-pipeline dependencies could not be resolved",
			parsed,
			consumerIDs,
		)
		return
	}
	varsByPipeline := workspaceFingerprintVars(graph, plan.PipelineUUID, variableOverrides)
	results, err := s.deps.Fingerprints.WorkspaceDAG(graph, varsByPipeline)
	if err != nil {
		s.addCrossPipelinePrerequisiteUnavailable(
			plan,
			pipelinePlanCodeCrossPipelinePrerequisiteNotReady,
			"cross-pipeline fingerprints could not be computed",
			parsed,
			consumerIDs,
		)
		return
	}

	consumerIDList := make([]string, 0, len(consumerIDs))
	for consumerID := range consumerIDs {
		consumerIDList = append(consumerIDList, consumerID)
	}
	sort.Strings(consumerIDList)
	for _, consumerID := range consumerIDList {
		consumer := graph.Nodes[consumerID]
		if consumer == nil {
			continue
		}
		for _, edge := range graph.EdgesByConsumer[consumerID] {
			if !fullURIEdge(edge) {
				continue
			}
			prerequisite := PipelinePlanPrerequisite{
				Status:          PipelinePlanPrerequisiteBlocked,
				ConsumerAssetID: consumerID, ConsumerAssetName: consumer.AssetName,
				URI: edge.Value, Environment: plan.Context.Environment,
				RequiredStart: timeWindow.StartRFC3339(), RequiredEnd: timeWindow.EndRFC3339(),
				RequiredSeconds: timeWindow.End.Sub(timeWindow.Start).Seconds(),
			}
			producer := graph.Nodes[edge.ProducerID]
			if !edge.Resolved || producer == nil {
				prerequisite.Reason = fmt.Sprintf("URI %q does not resolve to one producer in this workspace", edge.Value)
				s.appendBlockedPrerequisite(plan, prerequisite)
				continue
			}
			prerequisite.ProducerPipelineID = producer.PipelineID
			prerequisite.ProducerPipelineUUID = producer.PipelineUUID
			prerequisite.ProducerPipelineName = producer.PipelineName
			prerequisite.ProducerAssetID = producer.AssetID
			prerequisite.ProducerAssetName = producer.AssetName
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
				prerequisite.Reason = "the producer fingerprint is unavailable"
				s.appendBlockedPrerequisite(plan, prerequisite)
				continue
			}
			prerequisite.ExpectedFingerprint = string(result.FP)
			producerVars := varsByPipeline[producer.PipelineUUID]
			prerequisite.VarsHash = fingerprint.AllVarsHash(producerVars)
			target := resolveAssetPhysicalTarget(s.deps.WorkspaceRoot, &directPipelineInfo{
				Pipeline: producer.Pipeline,
				Asset:    producer.Asset,
				Config:   cfg,
			})
			if target.Fidelity != AssetRenderFidelityExact || strings.TrimSpace(target.Identity) == "" {
				prerequisite.Reason = "the producer physical target cannot be resolved exactly before execution"
				s.appendBlockedPrerequisite(plan, prerequisite)
				continue
			}
			prerequisite.TargetIdentity = target.Identity
			ready, reason, writer, covered, err := s.crossPipelineProducerReady(
				ctx,
				producer,
				prerequisite,
				timeWindow,
			)
			prerequisite.CoveredSeconds = covered
			if err != nil {
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
			prerequisite.Reason = "Renart observed the current producer output with complete required coverage"
			prerequisite.TargetGeneration = writer.TargetGeneration
			prerequisite.WriterRunID = writer.RunID
			prerequisite.WriterSnapshotVersionID = writer.SnapshotVersionID
			prerequisite.WriterCompletionID = writer.CompletionID
			prerequisite.WriterCompletionOrdinal = writer.CompletionOrdinal
			prerequisite.WriterMaterializedAt = writer.MaterializedAt.UTC().Format(time.RFC3339Nano)
			plan.Prerequisites = append(plan.Prerequisites, prerequisite)
		}
	}
	sortPipelinePlanPrerequisites(plan.Prerequisites)
}

func (s *PipelinePlanService) crossPipelineProducerReady(
	ctx context.Context,
	producer *dependencygraph.Node,
	prerequisite PipelinePlanPrerequisite,
	timeWindow ExecutionTimeWindow,
) (bool, string, matlog.LatestSuccessfulWriter, float64, error) {
	targets := []string{prerequisite.TargetIdentity}
	writers, err := s.deps.Materializations.LatestWriters(ctx, targets)
	if err != nil {
		return false, "", matlog.LatestSuccessfulWriter{}, 0, err
	}
	coverage, err := s.deps.Materializations.CurrentTargetCoverage(
		ctx,
		map[string]string{producer.AssetID: prerequisite.TargetIdentity},
		prerequisite.Environment,
		prerequisite.VarsHash,
	)
	if err != nil {
		return false, "", matlog.LatestSuccessfulWriter{}, 0, err
	}
	confirmed, err := s.deps.Materializations.LatestWriters(ctx, targets)
	if err != nil {
		return false, "", matlog.LatestSuccessfulWriter{}, 0, err
	}
	if !reflect.DeepEqual(writers, confirmed) {
		return false, "the producer changed while prerequisite evidence was being read", matlog.LatestSuccessfulWriter{}, 0, nil
	}
	writer, ok := writers[prerequisite.TargetIdentity]
	if !ok {
		return false, "the producer has no stable Renart-observed output (a write may be active or uncertain)", matlog.LatestSuccessfulWriter{}, 0, nil
	}
	if writer.Ambiguous {
		return false, "the producer target has an ambiguous latest writer", writer, 0, nil
	}
	if writer.AssetID != producer.AssetID || writer.Environment != prerequisite.Environment {
		return false, "the selected physical target was most recently written by another asset or environment", writer, 0, nil
	}
	if writer.Fingerprint != prerequisite.ExpectedFingerprint || writer.VarsHash != prerequisite.VarsHash {
		return false, "the producer's latest Renart-observed output does not match its current source and variables", writer, 0, nil
	}
	rows := coverage[producer.AssetID]
	if !matlog.IntervalAware(producer.Asset) {
		if len(rows) == 0 {
			return false, "the current producer output has no Renart-observed coverage", writer, 0, nil
		}
		return true, "", writer, prerequisite.RequiredSeconds, nil
	}
	covered, complete := prerequisiteCoverage(rows, timeWindow.Start, timeWindow.End)
	if !complete {
		return false, "the producer does not cover the full required execution interval", writer, covered.Seconds(), nil
	}
	return true, "", writer, covered.Seconds(), nil
}

func prerequisiteCoverage(rows []matlog.CoverageRow, start, end time.Time) (time.Duration, bool) {
	if !end.After(start) {
		return 0, false
	}
	type interval struct{ start, end time.Time }
	intervals := make([]interval, 0, len(rows))
	for _, row := range rows {
		if row.IntervalStart == nil || row.IntervalEnd == nil {
			return end.Sub(start), true
		}
		rowStart, rowEnd := row.IntervalStart.UTC(), row.IntervalEnd.UTC()
		if !rowEnd.After(start) || !end.After(rowStart) {
			continue
		}
		if rowStart.Before(start) {
			rowStart = start
		}
		if rowEnd.After(end) {
			rowEnd = end
		}
		intervals = append(intervals, interval{start: rowStart, end: rowEnd})
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	cursor := start
	var covered time.Duration
	for _, current := range intervals {
		if current.start.After(cursor) {
			return covered, false
		}
		if current.end.After(cursor) {
			covered += current.end.Sub(cursor)
			cursor = current.end
		}
	}
	return covered, !cursor.Before(end)
}

func workspaceFingerprintVars(
	graph dependencygraph.Graph,
	selectedPipelineUUID string,
	selectedOverrides map[string]any,
) map[string]fingerprint.Vars {
	result := make(map[string]fingerprint.Vars)
	for _, node := range graph.Nodes {
		if node == nil || node.Pipeline == nil {
			continue
		}
		if _, exists := result[node.PipelineUUID]; exists {
			continue
		}
		overrides := map[string]any(nil)
		if node.PipelineUUID == selectedPipelineUUID {
			overrides = selectedOverrides
		}
		result[node.PipelineUUID] = fingerprint.EffectiveVars(node.Pipeline, overrides)
	}
	return result
}

func pipelineHasSelectedFullURIDependency(parsed *pipeline.Pipeline, selected map[string]struct{}) bool {
	if parsed == nil {
		return false
	}
	for _, asset := range parsed.Assets {
		if asset == nil {
			continue
		}
		if _, ok := selected[parsed.LegacyID+":"+asset.Name]; !ok {
			continue
		}
		for _, upstream := range asset.Upstreams {
			if strings.EqualFold(strings.TrimSpace(upstream.Type), "uri") && upstream.Mode != pipeline.UpstreamModeSymbolic {
				return true
			}
		}
	}
	return false
}

func fullURIEdge(edge dependencygraph.Edge) bool {
	return strings.EqualFold(strings.TrimSpace(edge.Type), "uri") && edge.Mode != pipeline.UpstreamModeSymbolic
}

func (s *PipelinePlanService) addCrossPipelinePrerequisiteUnavailable(
	plan *PipelinePlan,
	code string,
	reason string,
	parsed *pipeline.Pipeline,
	selected map[string]struct{},
) {
	for _, asset := range parsed.Assets {
		if asset == nil {
			continue
		}
		assetID := parsed.LegacyID + ":" + asset.Name
		if _, ok := selected[assetID]; !ok {
			continue
		}
		for _, upstream := range asset.Upstreams {
			if !strings.EqualFold(strings.TrimSpace(upstream.Type), "uri") || upstream.Mode == pipeline.UpstreamModeSymbolic {
				continue
			}
			prerequisite := PipelinePlanPrerequisite{
				Status: PipelinePlanPrerequisiteBlocked, Reason: reason,
				ConsumerAssetID: assetID, ConsumerAssetName: asset.Name,
				URI: strings.TrimSpace(upstream.Value), Environment: plan.Context.Environment,
			}
			plan.Prerequisites = append(plan.Prerequisites, prerequisite)
			plan.Readiness.Blockers = append(plan.Readiness.Blockers, PipelinePlanIssue{
				Code: code, Severity: "error", Message: fmt.Sprintf("%s (%s)", reason, prerequisite.URI),
				AssetID: assetID, AssetName: asset.Name,
			})
		}
	}
	sortPipelinePlanPrerequisites(plan.Prerequisites)
}

func (s *PipelinePlanService) appendBlockedPrerequisite(plan *PipelinePlan, prerequisite PipelinePlanPrerequisite) {
	plan.Prerequisites = append(plan.Prerequisites, prerequisite)
	plan.Readiness.Blockers = append(plan.Readiness.Blockers, PipelinePlanIssue{
		Code:      pipelinePlanCodeCrossPipelinePrerequisiteNotReady,
		Severity:  "error",
		Message:   fmt.Sprintf("%s: %s", prerequisite.ProducerAssetName, prerequisite.Reason),
		AssetID:   prerequisite.ConsumerAssetID,
		AssetName: prerequisite.ConsumerAssetName,
	})
}

func sortPipelinePlanPrerequisites(items []PipelinePlanPrerequisite) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].ConsumerAssetID != items[j].ConsumerAssetID {
			return items[i].ConsumerAssetID < items[j].ConsumerAssetID
		}
		if items[i].URI != items[j].URI {
			return items[i].URI < items[j].URI
		}
		return items[i].ProducerAssetID < items[j].ProducerAssetID
	})
}
