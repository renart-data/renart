package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/identity"
	webmodel "renart/internal/web/model"
)

// SchemaEvidenceProvider is the common boundary for every asset schema
// collector. Providers advertise required access and return facts; precedence,
// comparability, and merge policy belong to the resolver below.
type SchemaEvidenceProvider interface {
	ID() string
	Matches(AssetSchemaContext) bool
	Capabilities(AssetSchemaContext) SchemaEvidenceCapabilities
	Observe(context.Context, SchemaEvidenceRequest) (SchemaEvidence, *APIError)
}

type AssetSchemaContext struct {
	Service        *AssetService
	AssetID        string
	Pipeline       *pipeline.Pipeline
	Asset          *pipeline.Asset
	ConnectionName string
	OutputIdentity string
	// ResolveDefinition returns a source asset's declaration schema without
	// external I/O. Load providers use it for recursion and cycle handling.
	ResolveDefinition func(context.Context, *pipeline.Asset) []pipeline.Column
}

type SchemaEvidenceStage string

const (
	SchemaStageContract     SchemaEvidenceStage = "contract"
	SchemaStageDeclaration  SchemaEvidenceStage = "declaration"
	SchemaStageMaterialized SchemaEvidenceStage = "materialized"
	SchemaStageRuntime      SchemaEvidenceStage = "runtime"
)

type SchemaCompleteness string

const (
	SchemaComplete SchemaCompleteness = "complete"
	SchemaPartial  SchemaCompleteness = "partial"
	SchemaUnknown  SchemaCompleteness = "unknown"
)

type SchemaConfidence string

const (
	SchemaConfidenceHigh   SchemaConfidence = "high"
	SchemaConfidenceMedium SchemaConfidence = "medium"
	SchemaConfidenceLow    SchemaConfidence = "low"
)

type SchemaEvidenceAccess struct {
	Filesystem bool
	Network    bool
	Warehouse  bool
	UserCode   bool
}

type SchemaEvidenceCapabilities struct {
	Source       webmodel.ColumnInferenceSource
	Stage        SchemaEvidenceStage
	Completeness SchemaCompleteness
	Confidence   SchemaConfidence
	Access       SchemaEvidenceAccess
	ExposeInSync bool
}

type SchemaEvidenceRequest struct {
	Context     AssetSchemaContext
	Environment string
	Allow       SchemaEvidenceAccess
}

type SchemaEvidenceScope struct {
	Environment string
	Connection  string
	Relation    string
}

type SchemaEvidence struct {
	Source         webmodel.ColumnInferenceSource
	Stage          SchemaEvidenceStage
	Scope          SchemaEvidenceScope
	Completeness   SchemaCompleteness
	Confidence     SchemaConfidence
	AssetRevision  string
	OutputIdentity string
	ObservedAt     time.Time
	Columns        []WorkspaceColumn
	Notes          []string
	SampleRecords  *int
	Fresh          *bool
	Diagnostics    []string
}

type SchemaEvidenceExclusion struct {
	Evidence       SchemaEvidence
	Classification string
	Reason         string
}

type SchemaEvidenceResolution struct {
	Comparable []SchemaEvidence
	Excluded   []SchemaEvidenceExclusion
}

// resolveSchemaEvidence partitions observations before merge policy sees them.
// A stale or differently scoped table is historical evidence, not a second
// simultaneous truth about the current definition.
func resolveSchemaEvidence(evidence []SchemaEvidence, requested SchemaEvidenceScope) SchemaEvidenceResolution {
	result := SchemaEvidenceResolution{
		Comparable: make([]SchemaEvidence, 0, len(evidence)),
		Excluded:   make([]SchemaEvidenceExclusion, 0),
	}
	var baseline *SchemaEvidence
	for index := range evidence {
		if evidence[index].Stage == SchemaStageContract || evidence[index].Stage == SchemaStageDeclaration {
			baseline = &evidence[index]
			break
		}
	}

	for _, item := range evidence {
		if baseline != nil && item.Fresh != nil && !*item.Fresh && item.Stage == SchemaStageMaterialized {
			result.Excluded = append(result.Excluded, SchemaEvidenceExclusion{
				Evidence: item, Classification: "stale",
				Reason: "The materialized relation was produced from an older asset fingerprint.",
			})
			continue
		}
		if mismatch := schemaScopeMismatch(item.Scope, requested); mismatch != "" {
			result.Excluded = append(result.Excluded, SchemaEvidenceExclusion{
				Evidence: item, Classification: "scoped", Reason: mismatch,
			})
			continue
		}
		if baseline != nil && item.Source.ID != baseline.Source.ID {
			if baseline.OutputIdentity != "" && item.OutputIdentity != "" && baseline.OutputIdentity != item.OutputIdentity {
				result.Excluded = append(result.Excluded, SchemaEvidenceExclusion{
					Evidence: item, Classification: "scoped",
					Reason: "The observation describes a different physical output identity.",
				})
				continue
			}
			if baseline.AssetRevision != "" && item.AssetRevision != "" && baseline.AssetRevision != item.AssetRevision {
				result.Excluded = append(result.Excluded, SchemaEvidenceExclusion{
					Evidence: item, Classification: "stale",
					Reason: "The observation describes a different asset revision.",
				})
				continue
			}
		}
		result.Comparable = append(result.Comparable, item)
	}
	return result
}

func schemaScopeMismatch(actual, requested SchemaEvidenceScope) string {
	checks := []struct {
		label string
		left  string
		right string
	}{
		{label: "environment", left: actual.Environment, right: requested.Environment},
		{label: "connection", left: actual.Connection, right: requested.Connection},
		{label: "relation", left: actual.Relation, right: requested.Relation},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.left) != "" && strings.TrimSpace(check.right) != "" && !strings.EqualFold(strings.TrimSpace(check.left), strings.TrimSpace(check.right)) {
			return fmt.Sprintf("The observation belongs to a different %s.", check.label)
		}
	}
	return ""
}

func schemaEvidenceScopeFor(context AssetSchemaContext, environment string) SchemaEvidenceScope {
	relation := ""
	if context.Asset != nil {
		relation = strings.TrimSpace(context.Asset.Name)
	}
	return SchemaEvidenceScope{
		Environment: strings.TrimSpace(environment),
		Connection:  strings.TrimSpace(context.ConnectionName),
		Relation:    relation,
	}
}

func (s *AssetService) schemaEvidenceOutputIdentity(
	pp *pipeline.Pipeline,
	asset *pipeline.Asset,
	environment string,
) string {
	if s == nil || pp == nil || asset == nil || strings.TrimSpace(pp.LegacyID) == "" {
		return ""
	}
	environment = strings.TrimSpace(environment)
	if environment == "" && s.deps.SelectedEnvironment != nil {
		environment = strings.TrimSpace(s.deps.SelectedEnvironment())
	}
	configPath := strings.TrimSpace(s.deps.ConfigPath)
	if configPath == "" && strings.TrimSpace(s.deps.WorkspaceRoot) != "" {
		configPath = filepath.Join(s.deps.WorkspaceRoot, ".bruin.yml")
	}
	targets, err := ResolvePipelinePhysicalTargets(s.deps.WorkspaceRoot, configPath, environment, pp)
	if err != nil {
		return ""
	}
	target := targets[identity.AssetID(pp.LegacyID, asset.Name)]
	if target.Fidelity != AssetRenderFidelityExact {
		return ""
	}
	return strings.TrimSpace(target.Identity)
}

// schemaAssetRevision is a local, deterministic revision for evidence
// partitioning. It intentionally excludes declared columns and Renart
// provenance: accepting metadata must not make an otherwise current table look
// stale. Canonical execution fingerprints remain the stronger freshness source
// when the staleness service is available.
func schemaAssetRevision(asset *pipeline.Asset) string {
	if asset == nil {
		return ""
	}
	payload := struct {
		Name            string                   `json:"name"`
		Type            pipeline.AssetType       `json:"type"`
		Connection      string                   `json:"connection"`
		Executable      string                   `json:"executable"`
		Parameters      pipeline.ParameterMap    `json:"parameters"`
		Materialization pipeline.Materialization `json:"materialization"`
	}{
		Name: asset.Name, Type: asset.Type, Connection: asset.Connection,
		Executable: asset.ExecutableFile.Content, Parameters: asset.Parameters,
		Materialization: asset.Materialization,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])[:16]
}

func compareContractWithEvidence(contract []pipeline.Column, evidence SchemaEvidence) webmodel.ColumnSchemaDrift {
	contractEvidence := SchemaEvidence{
		Source: webmodel.ColumnInferenceSource{
			ID: columnSourceContract, Label: "Declared columns", Category: "contract",
		},
		Stage: SchemaStageContract, Scope: evidence.Scope,
		Completeness: SchemaComplete, Confidence: SchemaConfidenceHigh,
		AssetRevision: evidence.AssetRevision, OutputIdentity: evidence.OutputIdentity,
		Columns: PipelineColumnsToModelColumns(contract),
	}
	resolved := resolveSchemaEvidence([]SchemaEvidence{contractEvidence, evidence}, evidence.Scope)
	if len(resolved.Comparable) < 2 {
		return webmodel.ColumnSchemaDrift{Items: []webmodel.ColumnSchemaDriftItem{}}
	}
	drift := compareColumnSchemas(contract, evidence.Columns)
	if evidence.Completeness == SchemaComplete {
		return drift
	}
	// Partial and unknown observations prove only what they contain. Absence
	// cannot be interpreted as a removed result column.
	items := drift.Items[:0]
	for _, item := range drift.Items {
		if item.Kind != "removed" {
			items = append(items, item)
		}
	}
	drift.Removed = 0
	drift.Items = items
	return drift
}

func pipelineColumnsForSchemaEvidence(evidence SchemaEvidence) []pipeline.Column {
	return ModelColumnsToPipelineColumns(evidence.Columns)
}

func schemaEvidenceSnapshot(evidence SchemaEvidence, classification, excludedReason string) webmodel.ColumnSchemaSourceSnapshot {
	var observedAt *time.Time
	if !evidence.ObservedAt.IsZero() {
		value := evidence.ObservedAt
		observedAt = &value
	}
	source := evidence.Source
	if evidence.Completeness != "" && evidence.Completeness != SchemaComplete {
		source.MayOmitColumns = true
	}
	return webmodel.ColumnSchemaSourceSnapshot{
		Source: source, Columns: evidence.Columns, Notes: evidence.Notes,
		SampleRecords: evidence.SampleRecords, Fresh: evidence.Fresh,
		Stage: string(evidence.Stage), Completeness: string(evidence.Completeness), Confidence: string(evidence.Confidence),
		AssetRevision: evidence.AssetRevision, OutputIdentity: evidence.OutputIdentity,
		Environment: evidence.Scope.Environment, Connection: evidence.Scope.Connection, Relation: evidence.Scope.Relation,
		ObservedAt: observedAt, Classification: classification, ExcludedReason: excludedReason,
	}
}

func timeNowUTC() time.Time { return time.Now().UTC() }

func requireSchemaEvidenceAccess(required, allowed SchemaEvidenceAccess) *APIError {
	missing := make([]string, 0, 4)
	if required.Filesystem && !allowed.Filesystem {
		missing = append(missing, "filesystem")
	}
	if required.Network && !allowed.Network {
		missing = append(missing, "network")
	}
	if required.Warehouse && !allowed.Warehouse {
		missing = append(missing, "warehouse")
	}
	if required.UserCode && !allowed.UserCode {
		missing = append(missing, "user code")
	}
	if len(missing) == 0 {
		return nil
	}
	return badRequestError(
		"schema_evidence_access_denied",
		"schema observation requires disallowed access: "+strings.Join(missing, ", "),
	)
}
