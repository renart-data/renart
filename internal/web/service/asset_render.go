package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/mask"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/bruin-data/bruin/pkg/tablename"
	"github.com/spf13/afero"

	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/runcontext"
	"renart/internal/web/snapshot"
)

const (
	assetRenderPreviewRunID          = "renart-render-preview"
	assetRenderFingerprintPipelineID = "renart-render-only-pipeline"
)

var (
	ErrAssetRenderSourceChanged  = errors.New("asset source changed while rendering")
	errQuerySensorQueryIsMissing = errors.New("query sensor parameter \"query\" is required")
)

type assetRenderManifestCollector func(string) (map[string]string, error)
type assetRenderSourceStateCollector func(string) (snapshot.SourceState, error)

type assetRenderBoundaryError struct {
	status  int
	code    string
	message string
	cause   error
}

func (e *assetRenderBoundaryError) Error() string {
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

func (e *assetRenderBoundaryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func classifyAssetRenderError(status int, code, message string, cause error) error {
	return &assetRenderBoundaryError{status: status, code: code, message: message, cause: cause}
}

type AssetRenderStatus string

const (
	AssetRenderStatusOK          AssetRenderStatus = "ok"
	AssetRenderStatusPartial     AssetRenderStatus = "partial"
	AssetRenderStatusUnsupported AssetRenderStatus = "unsupported"
	AssetRenderStatusError       AssetRenderStatus = "error"
)

type AssetRenderStageStatus string

const (
	AssetRenderStageStatusOK          AssetRenderStageStatus = "ok"
	AssetRenderStageStatusUnsupported AssetRenderStageStatus = "unsupported"
	AssetRenderStageStatusError       AssetRenderStageStatus = "error"
)

type AssetRenderFidelity string

const (
	AssetRenderFidelityExact       AssetRenderFidelity = "exact"
	AssetRenderFidelitySemantic    AssetRenderFidelity = "semantic"
	AssetRenderFidelityRuntimeOnly AssetRenderFidelity = "runtime_only"
	AssetRenderFidelityUnsupported AssetRenderFidelity = "unsupported"
)

// renart:web
type AssetRenderRequest struct {
	Environment   string `json:"environment,omitempty"`
	StartDate     string `json:"start_date,omitempty"`
	EndDate       string `json:"end_date,omitempty"`
	ExecutionTime string `json:"execution_time,omitempty"`
	FullRefresh   bool   `json:"full_refresh"`
}

type AssetRenderSource struct {
	Kind              string `json:"kind"`
	VersionID         string `json:"version_id,omitempty"`
	DeploymentOrdinal int64  `json:"deployment_ordinal,omitempty"`
	PipelinePath      string `json:"pipeline_path"`
	MerkleRoot        string `json:"merkle_root"`
}

type AssetRenderContext struct {
	Environment           string                          `json:"environment,omitempty"`
	SchemaPrefix          string                          `json:"schema_prefix,omitempty"`
	StartDate             string                          `json:"start_date"`
	EndDate               string                          `json:"end_date"`
	ExecutionTime         string                          `json:"execution_time"`
	RunID                 string                          `json:"run_id"`
	RequestedFullRefresh  bool                            `json:"requested_full_refresh"`
	FullRefresh           bool                            `json:"full_refresh"`
	VariablesDigest       string                          `json:"variables_digest"`
	CoverageVariablesHash string                          `json:"coverage_variables_hash"`
	VariableProvenance    []AssetRenderVariableProvenance `json:"variable_provenance"`
	ConfigurationDigest   string                          `json:"configuration_digest"`
	ConfigurationFidelity string                          `json:"configuration_fidelity"`
	ConfigurationMessage  string                          `json:"configuration_message,omitempty"`
}

type AssetRenderVariableProvenance struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

type AssetRenderProvenance struct {
	Source   AssetRenderSource  `json:"source"`
	Pipeline string             `json:"pipeline"`
	Context  AssetRenderContext `json:"context"`
}

// AssetRenderTarget identifies the mutable physical output selected by the
// same saved asset and environment configuration used by execution. Identity
// is deliberately empty unless Fidelity is exact. Object is presentation-only
// and never contains connection endpoint coordinates or a DuckDB database path.
type AssetRenderTarget struct {
	Kind          string                   `json:"kind"`
	Object        string                   `json:"object,omitempty"`
	Identity      string                   `json:"identity,omitempty"`
	Fidelity      AssetRenderFidelity      `json:"fidelity"`
	Message       string                   `json:"message,omitempty"`
	WriteResource AssetRenderWriteResource `json:"write_resource"`
}

// AssetRenderWriteResource is the exclusive mutation resource selected by the
// same resolver as execution. Identity is an opaque secret-free digest. A
// pipeline-scoped runtime-only claim deliberately prevents optimistic
// destination isolation.
type AssetRenderWriteResource struct {
	Kind     string              `json:"kind"`
	Identity string              `json:"identity,omitempty"`
	Fidelity AssetRenderFidelity `json:"fidelity"`
	Message  string              `json:"message,omitempty"`
}

type AssetRenderAsset struct {
	ID             string            `json:"id,omitempty"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Dialect        string            `json:"dialect,omitempty"`
	ConnectionName string            `json:"connection_name,omitempty"`
	Fingerprint    string            `json:"fingerprint,omitempty"`
	Target         AssetRenderTarget `json:"target"`
}

type AssetRenderStage struct {
	Kind          string                 `json:"kind"`
	Label         string                 `json:"label,omitempty"`
	Language      string                 `json:"language"`
	Content       string                 `json:"content,omitempty"`
	Status        AssetRenderStageStatus `json:"status"`
	Fidelity      AssetRenderFidelity    `json:"fidelity"`
	Conditional   bool                   `json:"conditional,omitempty"`
	CheckKind     string                 `json:"check_kind,omitempty"`
	CheckName     string                 `json:"check_name,omitempty"`
	CheckColumn   string                 `json:"check_column,omitempty"`
	CheckBlocking *bool                  `json:"check_blocking,omitempty"`
	Redacted      bool                   `json:"redacted,omitempty"`
	Message       string                 `json:"message,omitempty"`
}

type AssetRenderRedaction struct {
	Kind        string `json:"kind"`
	Replacement string `json:"replacement"`
}

type AssetRenderIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// renart:web
type AssetRenderResult struct {
	Status     AssetRenderStatus      `json:"status"`
	Provenance AssetRenderProvenance  `json:"provenance"`
	Asset      AssetRenderAsset       `json:"asset"`
	Stages     []AssetRenderStage     `json:"stages"`
	Issues     []AssetRenderIssue     `json:"issues"`
	Redactions []AssetRenderRedaction `json:"redactions"`
}

// AssetRenderService renders a saved working-tree asset without connecting to
// a warehouse. SQL assets expose compiled queries and, where the direct
// materializer supports it, exact execution SQL. Renart-owned non-SQL runtimes
// expose secret-free semantic or runtime-only operation descriptions.
type AssetRenderService struct {
	workspaceRoot      string
	physicalTargetRoot string
	fs                 afero.Fs
	now                func() time.Time
	collectManifest    assetRenderManifestCollector
	collectSourceState assetRenderSourceStateCollector
	fingerprintEngine  *fingerprint.Engine
	configPath         string
	source             AssetRenderSource
	// variableOverrides is an internal exact-source planning input. HTTP asset
	// rendering never populates it; scheduled planning uses the same strict
	// pre-asset mutator as execution.
	variableOverrides      map[string]any
	variableOverrideSource string
}

func NewAssetRenderService(workspaceRoot string) *AssetRenderService {
	return &AssetRenderService{
		workspaceRoot:      workspaceRoot,
		physicalTargetRoot: workspaceRoot,
		fs:                 afero.NewOsFs(),
		now:                func() time.Time { return time.Now().UTC() },
		collectManifest:    snapshot.CollectManifestHashes,
		collectSourceState: snapshot.CollectSourceState,
		fingerprintEngine:  fingerprint.NewEngine(),
	}
}

// newAssetRenderServiceForSource creates the exact-source renderer used by
// pipeline planning. Source files may live in an isolated snapshot directory,
// while physicalTargetRoot remains the real runtime workspace used to resolve
// relative output paths. It remains package-private so HTTP asset rendering
// cannot accept client-owned paths or source metadata.
func newAssetRenderServiceForSource(
	sourceRoot string,
	physicalTargetRoot string,
	configPath string,
	source AssetRenderSource,
) *AssetRenderService {
	service := NewAssetRenderService(sourceRoot)
	service.physicalTargetRoot = physicalTargetRoot
	service.configPath = configPath
	service.source = source
	return service
}

func (s *AssetRenderService) computeAssetRenderFingerprint(
	pl *pipeline.Pipeline,
	asset *pipeline.Asset,
	vars fingerprint.Vars,
) (string, error) {
	if pl == nil || asset == nil {
		return "", fmt.Errorf("asset fingerprint context is incomplete")
	}

	// Fingerprint DAG results are keyed by the durable pipeline ID. Older
	// pipelines may not have one yet, but rendering is read-only and must not
	// self-assign it on disk. Use a shallow copy with a request-local sentinel;
	// LegacyID only selects the result map key and is not part of the canonical
	// fingerprint input.
	fingerprintPipeline := *pl
	pipelineID := strings.TrimSpace(fingerprintPipeline.LegacyID)
	if pipelineID == "" {
		pipelineID = assetRenderFingerprintPipelineID
		fingerprintPipeline.LegacyID = pipelineID
	}

	engine := s.fingerprintEngine
	if engine == nil {
		engine = fingerprint.NewEngine()
	}
	results, err := engine.DAG(&fingerprintPipeline, vars)
	if err != nil {
		return "", err
	}
	result, ok := results[identity.AssetID(pipelineID, asset.Name)]
	if !ok {
		return "", fmt.Errorf("asset fingerprint result is missing")
	}
	return string(result.FP), nil
}

// RenderAsset renders the saved asset identified by the same opaque workspace
// ID used by the rest of the HTTP API. Callers cannot provide a filesystem
// path or run ID: both are resolved/owned by the server.
func (s *AssetRenderService) RenderAsset(ctx context.Context, assetID string, req AssetRenderRequest) (AssetRenderResult, *APIError) {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return AssetRenderResult{}, &APIError{Status: 400, Code: "asset_id_required", Message: "asset id is required"}
	}
	assetPath, err := DecodeID(assetID)
	if err != nil {
		return AssetRenderResult{}, &APIError{Status: 400, Code: "invalid_asset_id", Message: "asset id is invalid"}
	}
	result, err := s.renderPath(ctx, assetPath, req)
	if err == nil {
		return result, nil
	}
	return AssetRenderResult{}, assetRenderAPIError(err)
}

func assetRenderAPIError(err error) *APIError {
	if errors.Is(err, ErrAssetNotFound) {
		return &APIError{Status: 404, Code: "asset_not_found", Message: "asset was not found"}
	}
	if errors.Is(err, ErrAssetRenderSourceChanged) {
		return &APIError{
			Status:  409,
			Code:    "source_changed",
			Message: "asset source changed while rendering; retry the preview",
		}
	}
	var boundaryErr *assetRenderBoundaryError
	if errors.As(err, &boundaryErr) {
		return &APIError{
			Status:  boundaryErr.status,
			Code:    boundaryErr.code,
			Message: boundaryErr.message,
		}
	}
	// Keep unclassified parse/render failures as authoring errors. Infrastructure
	// failures are classified at the point where they occur so they cannot be
	// mistaken for a bad saved asset without turning incomplete authoring into a
	// server fault.
	return &APIError{Status: 400, Code: "asset_render_failed", Message: "asset could not be rendered"}
}

// RenderPath is the in-process/CLI entry point. HTTP handlers must use
// RenderAsset so a request body can never choose a filesystem path.
func (s *AssetRenderService) RenderPath(ctx context.Context, assetPath string, req AssetRenderRequest) (AssetRenderResult, error) {
	return s.renderPath(ctx, assetPath, req)
}

func (s *AssetRenderService) renderPath(ctx context.Context, assetPath string, req AssetRenderRequest) (result AssetRenderResult, resultErr error) {
	if s == nil {
		return AssetRenderResult{}, fmt.Errorf("asset render service is not configured")
	}
	assetPath, err := resolveWorkspaceRenderAssetPath(s.workspaceRoot, assetPath)
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) {
			return AssetRenderResult{}, ErrAssetNotFound
		}
		return AssetRenderResult{}, classifyAssetRenderError(
			http.StatusBadRequest,
			"invalid_asset_path",
			"asset path is invalid",
			err,
		)
	}

	fs := s.fs
	if fs == nil {
		fs = afero.NewOsFs()
	}
	pipelineDir, err := findPipelineRootForAsset(assetPath)
	if err != nil {
		return AssetRenderResult{}, fmt.Errorf("resolve pipeline for rendering: %w", err)
	}
	collectManifest := s.collectManifest
	if collectManifest == nil {
		collectManifest = snapshot.CollectManifestHashes
	}
	collectSourceState := s.collectSourceState
	if collectSourceState == nil {
		collectSourceState = snapshot.CollectSourceState
	}
	sourceStateBeforeHash, err := collectSourceState(pipelineDir)
	if err != nil {
		return AssetRenderResult{}, classifyAssetRenderError(
			http.StatusInternalServerError,
			"source_identity_failed",
			"asset source identity could not be computed",
			fmt.Errorf("capture source state: %w", err),
		)
	}
	manifest, err := collectManifest(pipelineDir)
	if err != nil {
		return AssetRenderResult{}, classifyAssetRenderError(
			http.StatusInternalServerError,
			"source_identity_failed",
			"asset source identity could not be computed",
			fmt.Errorf("compute source identity: %w", err),
		)
	}
	if len(manifest) == 0 {
		err = fmt.Errorf("compute source identity: pipeline contains no source files")
		return AssetRenderResult{}, classifyAssetRenderError(
			http.StatusInternalServerError,
			"source_identity_failed",
			"asset source identity could not be computed",
			err,
		)
	}
	sourceMerkleRoot := snapshot.ManifestRoot(manifest)
	sourceState, err := collectSourceState(pipelineDir)
	if err != nil {
		return AssetRenderResult{}, classifyAssetRenderError(
			http.StatusInternalServerError,
			"source_identity_failed",
			"asset source identity could not be verified",
			fmt.Errorf("verify source identity after hashing: %w", err),
		)
	}
	if !sourceStateBeforeHash.Equal(sourceState) {
		return AssetRenderResult{}, ErrAssetRenderSourceChanged
	}
	defer func() {
		latestSourceState, verifyErr := collectSourceState(pipelineDir)
		if verifyErr != nil {
			if resultErr == nil {
				result = AssetRenderResult{}
				resultErr = classifyAssetRenderError(
					http.StatusInternalServerError,
					"source_identity_failed",
					"asset source identity could not be verified",
					fmt.Errorf("verify source identity after rendering: %w", verifyErr),
				)
			}
			return
		}
		if !sourceState.Equal(latestSourceState) {
			result = AssetRenderResult{}
			resultErr = ErrAssetRenderSourceChanged
		}
	}()

	var pp *directPipelineInfo
	if strings.TrimSpace(s.configPath) != "" {
		var mutator pipeline.PipelineMutator
		if len(s.variableOverrides) > 0 {
			mutator, err = variableOverridesMutator(s.variableOverrides)
			if err != nil {
				return AssetRenderResult{}, err
			}
		}
		pp, err = getDirectPipelineAndAssetWithConfigLoaderAndMutator(
			ctx, s.workspaceRoot, assetPath, fs, s.configPath, loadSelectedConfigReadOnlyFS, mutator,
		)
	} else {
		pp, err = getDirectPipelineAndAssetReadOnly(ctx, s.workspaceRoot, assetPath, fs)
	}
	if err != nil {
		return AssetRenderResult{}, fmt.Errorf("resolve asset for rendering: %w", err)
	}
	if pp == nil || pp.Pipeline == nil || pp.Asset == nil {
		return AssetRenderResult{}, fmt.Errorf("resolved asset is incomplete")
	}
	// Keep credential redaction structural: every result assembled after the
	// config is loaded passes through one finalizer, including stages appended by
	// later deferred work. This defer is registered before the quality-check
	// defer below so LIFO ordering redacts those stages too.
	defer func() {
		result = finalizeAssetRenderResult(result, pp.Config)
	}()
	if _, err := selectConfigEnvironment(pp.Config, req.Environment); err != nil {
		return AssetRenderResult{}, classifyAssetRenderError(
			http.StatusBadRequest,
			"invalid_environment",
			"selected environment is not configured",
			fmt.Errorf("select environment: %w", err),
		)
	}
	executionFullRefresh := req.FullRefresh && !selectedEnvironmentRestrictsFullRefresh(pp.Config)
	applySelectedEnvironmentRefreshRestriction(pp.Config, pp.Pipeline.Assets)
	effectiveFullRefresh := executionFullRefresh && !assetRefreshRestricted(pp.Asset)

	executionTime, err := s.resolveExecutionTime(req.ExecutionTime)
	if err != nil {
		return AssetRenderResult{}, classifyAssetRenderError(
			http.StatusBadRequest,
			"invalid_execution_time",
			"execution_time must be an RFC3339 timestamp",
			err,
		)
	}
	timeWindow, err := ResolveExecutionTimeWindow(
		string(pp.Pipeline.Schedule),
		req.StartDate,
		req.EndDate,
		executionTime,
	)
	if err != nil {
		return AssetRenderResult{}, classifyAssetRenderError(
			http.StatusBadRequest,
			"invalid_time_window",
			err.Error(),
			fmt.Errorf("resolve execution window: %w", err),
		)
	}

	runID := assetRenderPreviewRunID
	effectiveVars := fingerprint.EffectiveVars(pp.Pipeline, nil)
	coverageVariablesHash := fingerprint.AllVarsHash(effectiveVars)

	source := s.source
	if strings.TrimSpace(source.Kind) == "" {
		source.Kind = "working_tree"
	}
	if strings.TrimSpace(source.PipelinePath) == "" {
		source.PipelinePath = workspaceRelativeRenderPath(s.workspaceRoot, pp.Pipeline.DefinitionFile.Path)
	}
	if strings.TrimSpace(source.MerkleRoot) != "" && source.MerkleRoot != sourceMerkleRoot {
		return AssetRenderResult{}, ErrAssetRenderSourceChanged
	}
	source.MerkleRoot = sourceMerkleRoot

	result = AssetRenderResult{
		Status: AssetRenderStatusOK,
		Provenance: AssetRenderProvenance{
			Source:   source,
			Pipeline: pp.Pipeline.Name,
			Context: AssetRenderContext{
				Environment:           pp.Config.SelectedEnvironmentName,
				StartDate:             timeWindow.StartRFC3339(),
				EndDate:               timeWindow.EndRFC3339(),
				ExecutionTime:         executionTime.Format(time.RFC3339Nano),
				RunID:                 runID,
				RequestedFullRefresh:  req.FullRefresh,
				FullRefresh:           effectiveFullRefresh,
				VariablesDigest:       coverageVariablesHash,
				CoverageVariablesHash: coverageVariablesHash,
			},
		},
		Asset: AssetRenderAsset{
			ID:   assetReportID(s.workspaceRoot, pp.Asset),
			Name: pp.Asset.Name,
			Type: string(pp.Asset.Type),
		},
		Stages:     []AssetRenderStage{},
		Issues:     []AssetRenderIssue{},
		Redactions: []AssetRenderRedaction{},
	}
	assetFingerprint, fingerprintErr := s.computeAssetRenderFingerprint(pp.Pipeline, pp.Asset, effectiveVars)
	if fingerprintErr != nil {
		result.Status = AssetRenderStatusPartial
		result.Issues = append(result.Issues, AssetRenderIssue{
			Code:     "asset_fingerprint_failed",
			Severity: "warning",
			Message:  "asset/DAG fingerprint could not be computed",
		})
	} else {
		result.Asset.Fingerprint = assetFingerprint
	}
	if req.FullRefresh && !effectiveFullRefresh {
		result.Issues = append(result.Issues, AssetRenderIssue{
			Code:     "full_refresh_restricted",
			Severity: "warning",
			Message:  "full refresh is restricted for this asset in the selected environment; execution uses the configured materialization strategy",
		})
	}
	if pp.Config.SelectedEnvironment != nil {
		result.Provenance.Context.SchemaPrefix = pp.Config.SelectedEnvironment.SchemaPrefix
	}
	connectionName, connectionErr := assetRenderConnectionName(pp)
	if connectionErr == nil {
		result.Asset.ConnectionName = connectionName
	} else {
		result.Status = AssetRenderStatusPartial
		result.Issues = append(result.Issues, AssetRenderIssue{
			Code:     "connection_unresolved",
			Severity: "error",
			Message:  connectionErr.Error(),
		})
	}
	configurationIdentity := selectedConfigurationIdentityWithBindings(
		s.workspaceRoot,
		pp.Config,
		assetRenderConfigurationConnectionNames(pp, result.Asset.ConnectionName),
	)
	if connectionErr != nil && !assetRenderAssetIsConnectionless(pp) {
		configurationIdentity = runcontext.Identity{
			Fidelity: runcontext.IdentityFidelityRuntimeOnly,
			Message:  "asset connection configuration could not be resolved",
		}
	}
	result.Provenance.Context.ConfigurationDigest = configurationIdentity.Digest
	result.Provenance.Context.ConfigurationFidelity = string(configurationIdentity.Fidelity)
	result.Provenance.Context.ConfigurationMessage = configurationIdentity.Message
	result.Provenance.Context.VariableProvenance = assetRenderVariableProvenanceWithOverrides(
		pp.Pipeline, s.variableOverrides, s.variableOverrideSource,
	)
	targetRoot := s.physicalTargetRoot
	if strings.TrimSpace(targetRoot) == "" {
		targetRoot = s.workspaceRoot
	}
	result.Asset.Target = resolveAssetPhysicalTarget(targetRoot, pp)
	if result.Asset.Target.Fidelity == AssetRenderFidelityRuntimeOnly {
		result.Status = mergeAssetRenderStatus(result.Status, AssetRenderStatusPartial)
	}

	renderer, err := buildAssetPlanRenderer(fs, pp.Pipeline, timeWindow, executionTime, runID)
	if err != nil {
		return AssetRenderResult{}, fmt.Errorf("build renderer: %w", err)
	}
	renderCtx := assetPlanRenderContext(ctx, pp.Config, timeWindow, executionTime, runID, effectiveFullRefresh)
	// Every main-render branch below returns independently so incomplete SQL or
	// unsupported materialization never erases a useful partial preview. Append
	// scheduler-created post tasks in one deferred finalizer to give checks and
	// metadata push the same partial-result behavior without duplicating every
	// return path. Their presentation order matches Bruin's task-instance order:
	// checks are declared before metadata push. They are sibling post-tasks that
	// both depend on the main task, not on each other.
	defer func() {
		if resultErr != nil {
			return
		}
		checkOutcome := renderAssetCheckStages(renderCtx, pp.Pipeline, pp.Asset, renderer)
		if len(checkOutcome.stages) > 0 || len(checkOutcome.issues) > 0 {
			result.Stages = append(result.Stages, checkOutcome.stages...)
			result.Issues = append(result.Issues, checkOutcome.issues...)
			result.Status = mergeAssetRenderStatus(result.Status, checkOutcome.status)
		}

		metadataOutcome := renderAssetMetadataPushStages(pp.Pipeline, pp.Asset)
		if len(metadataOutcome.stages) > 0 || len(metadataOutcome.issues) > 0 {
			result.Stages = append(result.Stages, metadataOutcome.stages...)
			result.Issues = append(result.Issues, metadataOutcome.issues...)
			result.Status = mergeAssetRenderStatus(result.Status, metadataOutcome.status)
		}
	}()
	if outcome := renderSemanticAsset(pp, renderer, renderCtx, result.Asset.ConnectionName, effectiveFullRefresh, s.workspaceRoot); outcome.handled {
		result.Stages = append(result.Stages, outcome.stages...)
		result.Issues = append(result.Issues, outcome.issues...)
		result.Redactions = append(result.Redactions, outcome.redactions...)
		result.Status = mergeAssetRenderStatus(result.Status, outcome.status)
		return result, nil
	}

	dialect, dialectErr := AssetTypeToDialect(pp.Asset.Type)
	if dialectErr != nil {
		result.Status = AssetRenderStatusUnsupported
		result.Stages = append(result.Stages, AssetRenderStage{
			Kind:     "query",
			Language: languageForRenderAsset(pp.Asset),
			Status:   AssetRenderStageStatusUnsupported,
			Fidelity: AssetRenderFidelityUnsupported,
			Message:  "static rendering is not supported for this asset type",
		})
		return result, nil
	}
	result.Asset.Dialect = dialect
	extractor := newDirectSQLQueryExtractor(fs, renderer, pp.Asset.Type)
	assetExtractor, err := extractor.CloneForAsset(renderCtx, pp.Pipeline, pp.Asset)
	if err != nil {
		result.Status = AssetRenderStatusError
		result.Stages = append(result.Stages, failedExactRenderStage("compiled_query", err))
		return result, nil
	}

	querySource, err := querySourceForRenderAsset(pp.Asset)
	if err != nil {
		result.Status = AssetRenderStatusError
		result.Stages = append(result.Stages, failedExactRenderStage("compiled_query", err))
		if errors.Is(err, errQuerySensorQueryIsMissing) {
			result.Issues = append(result.Issues, AssetRenderIssue{
				Code:     "query_sensor_query_missing",
				Severity: "error",
				Message:  err.Error(),
			})
		}
		return result, nil
	}

	queries, err := assetExtractor.ExtractQueriesFromString(querySource)
	if err != nil {
		result.Status = AssetRenderStatusError
		result.Stages = append(result.Stages, failedExactRenderStage("compiled_query", err))
		return result, nil
	}
	compiledQuery, err := compiledQueryForRenderAsset(pp.Asset, queries)
	if err != nil {
		result.Status = AssetRenderStatusError
		result.Stages = append(result.Stages, failedExactRenderStage("compiled_query", err))
		return result, nil
	}

	if compiledQuery != "" {
		result.Stages = append(result.Stages, AssetRenderStage{
			Kind:     "compiled_query",
			Language: "sql",
			Content:  compiledQuery,
			Status:   AssetRenderStageStatusOK,
			Fidelity: AssetRenderFidelityExact,
		})
	}
	targetName := result.Asset.Target.Object
	if strings.TrimSpace(targetName) == "" {
		targetName = pp.Asset.Name
	}
	if lifecycleStage, ok := renderMaterializationTargetLifecycleStage(pp.Asset, targetName, executionFullRefresh); ok {
		result.Stages = append(result.Stages, lifecycleStage)
	}

	if isQuerySensorAssetType(pp.Asset.Type) {
		if pp.Asset.Type == pipeline.AssetTypeBigqueryQuerySensor {
			operatorOutcome := assetRenderSemanticOutcome{status: AssetRenderStatusOK}
			appendBigQueryQueryCostGuard(&operatorOutcome, pp)
			result.Stages = append(result.Stages, operatorOutcome.stages...)
			result.Issues = append(result.Issues, operatorOutcome.issues...)
			result.Status = mergeAssetRenderStatus(result.Status, operatorOutcome.status)
		}
		result.Stages = append(result.Stages, AssetRenderStage{
			Kind:     "execution_sql",
			Language: "sql",
			Content:  compiledQuery,
			Status:   AssetRenderStageStatusOK,
			Fidelity: AssetRenderFidelityExact,
			Message:  "exact rendered query submitted by the sensor; polling mode, interval, and timeout are runtime controls and are not included in this SQL stage",
		})
		return result, nil
	}
	if pp.Asset.Type == pipeline.AssetTypeOracleQuery {
		if pp.Asset.Materialization.Type != pipeline.MaterializationTypeNone {
			result.Status = AssetRenderStatusPartial
			result.Stages = append(result.Stages, failedExactRenderStage(
				"execution_sql",
				fmt.Errorf("direct oracle execution only supports assets without materialization"),
			))
			return result, nil
		}

		executionMessage := ""
		if len(pp.Asset.Hooks.Pre) > 0 || len(pp.Asset.Hooks.Post) > 0 {
			result.Status = AssetRenderStatusPartial
			executionMessage = "the direct Oracle runtime does not execute declared pre/post hooks"
			result.Issues = append(result.Issues, AssetRenderIssue{
				Code:     "oracle_hooks_unsupported",
				Severity: "warning",
				Message:  executionMessage,
			})
		}
		result.Stages = append(result.Stages, AssetRenderStage{
			Kind:     "execution_sql",
			Language: "sql",
			Content:  compiledQuery,
			Status:   AssetRenderStageStatusOK,
			Fidelity: AssetRenderFidelityExact,
			Message:  executionMessage,
		})
		return result, nil
	}

	if outcome := renderBigQuerySnowflakeExecution(
		renderCtx,
		pp,
		renderer,
		assetExtractor,
		compiledQuery,
		executionFullRefresh,
		effectiveFullRefresh,
	); outcome.handled {
		result.Stages = append(result.Stages, outcome.stages...)
		result.Issues = append(result.Issues, outcome.issues...)
		result.Redactions = append(result.Redactions, outcome.redactions...)
		result.Status = mergeAssetRenderStatus(result.Status, outcome.status)
		return result, nil
	}
	if outcome := renderAthenaExecution(
		renderCtx,
		pp,
		renderer,
		assetExtractor,
		compiledQuery,
		executionFullRefresh,
		effectiveFullRefresh,
	); outcome.handled {
		result.Stages = append(result.Stages, outcome.stages...)
		result.Issues = append(result.Issues, outcome.issues...)
		result.Redactions = append(result.Redactions, outcome.redactions...)
		result.Status = mergeAssetRenderStatus(result.Status, outcome.status)
		return result, nil
	}

	if supportsOrderedBatchExecutionRender(pp.Asset) {
		resolvedHooks, resolveErr := resolveAssetHookTemplates(renderCtx, pp.Pipeline, pp.Asset, renderer)
		if resolveErr != nil {
			result.Status = AssetRenderStatusPartial
			result.Stages = append(result.Stages, failedExactRenderStage("execution_sql", resolveErr))
			return result, nil
		}
		assetCopy := *pp.Asset
		assetCopy.Hooks = resolvedHooks
		orderedStages, supported, renderErr := renderExactQueryBatchExecutionStages(
			&assetCopy,
			assetExtractor,
			compiledQuery,
			executionFullRefresh,
		)
		if !supported {
			result.Status = AssetRenderStatusPartial
			result.Stages = append(result.Stages, AssetRenderStage{
				Kind:     "execution_sql",
				Language: "sql",
				Status:   AssetRenderStageStatusUnsupported,
				Fidelity: AssetRenderFidelityUnsupported,
				Message:  "exact ordered execution rendering is not available for this SQL asset type",
			})
			return result, nil
		}
		if renderErr != nil {
			result.Status = AssetRenderStatusPartial
			result.Stages = append(result.Stages, failedExactRenderStage("execution_sql", renderErr))
			return result, nil
		}

		if pp.Asset.Materialization.Type != pipeline.MaterializationTypeNone && pp.Asset.Type == pipeline.AssetTypeDatabricksQuery {
			appendDatabricksPreparationStages(&result, pp.Asset)
		}

		executionMessage := ""
		if result.Provenance.Context.SchemaPrefix != "" {
			executionMessage = "pre-rewrite materializer SQL; the developer-environment table rewrite depends on live warehouse state"
		}
		ephemeralIdentifiers := executionMaterializationUsesEphemeralIdentifiers(pp.Asset, executionFullRefresh)
		for index := range orderedStages {
			stage := &orderedStages[index]
			message := executionMessage
			// When hook provenance remains trustworthy, generated temporary
			// identifiers affect only the materializer statements. If DECLARE
			// hoisting erased that boundary, every generic execution stage must
			// conservatively carry the downgrade.
			ephemeralStage := ephemeralIdentifiers && stage.Kind == "execution_sql"
			if executionMessage != "" || ephemeralStage {
				stage.Fidelity = AssetRenderFidelityRuntimeOnly
				if ephemeralStage {
					if message != "" {
						message += "; "
					}
					message += "temporary table identifiers are generated independently when execution starts"
				}
				stage.Message = message
				result.Status = AssetRenderStatusPartial
			}
		}
		result.Stages = append(result.Stages, orderedStages...)
		return result, nil
	}

	executionAsset := pp.Asset
	if supportsDirectStringExecutionRender(pp.Asset) {
		resolvedHooks, resolveErr := resolveAssetHookTemplates(renderCtx, pp.Pipeline, pp.Asset, renderer)
		if resolveErr != nil {
			result.Status = AssetRenderStatusPartial
			result.Stages = append(result.Stages, failedExactRenderStage("execution_sql", resolveErr))
			return result, nil
		}
		assetCopy := *pp.Asset
		assetCopy.Hooks = resolvedHooks
		executionAsset = &assetCopy
	}

	executionSQL, supported, err := renderExactStringExecutionSQL(executionAsset, assetExtractor, compiledQuery, executionFullRefresh)
	if !supported {
		result.Status = AssetRenderStatusPartial
		result.Stages = append(result.Stages, AssetRenderStage{
			Kind:     "execution_sql",
			Language: "sql",
			Status:   AssetRenderStageStatusUnsupported,
			Fidelity: AssetRenderFidelityUnsupported,
			Message:  "exact execution rendering is not yet exposed for this SQL asset type",
		})
		return result, nil
	}
	if err != nil {
		result.Status = AssetRenderStatusPartial
		result.Stages = append(result.Stages, failedExactRenderStage("execution_sql", err))
		return result, nil
	}

	if pp.Asset.Materialization.Type != pipeline.MaterializationTypeNone && directExecutionCreatesSchema(pp.Asset.Type) {
		if schemaName, hasSchema := tablename.SchemaToCreate(pp.Asset.Name, strings.ToLower); hasSchema {
			result.Stages = append(result.Stages, AssetRenderStage{
				Kind:        "schema_preparation",
				Language:    "sql",
				Content:     "CREATE SCHEMA IF NOT EXISTS " + schemaName,
				Status:      AssetRenderStageStatusOK,
				Fidelity:    AssetRenderFidelitySemantic,
				Conditional: true,
				Message:     "executed until the connection-local schema cache records this schema",
			})
		}
	}
	appendDirectStringSCD2MigrationStage(&result, pp.Asset, executionFullRefresh)

	executionFidelity := AssetRenderFidelityExact
	executionMessage := ""
	if result.Provenance.Context.SchemaPrefix != "" {
		// The SQL operator's developer-environment modifier consults the live
		// database schema and may rename referenced tables after materialization.
		// Planning must remain connection-free, so expose the canonical
		// pre-rewrite SQL while being explicit that the final query is runtime-only.
		result.Status = AssetRenderStatusPartial
		executionFidelity = AssetRenderFidelityRuntimeOnly
		executionMessage = "pre-rewrite materializer SQL; the developer-environment table rewrite depends on live warehouse state"
	}
	if executionMaterializationUsesEphemeralIdentifiers(pp.Asset, executionFullRefresh) {
		result.Status = AssetRenderStatusPartial
		executionFidelity = AssetRenderFidelityRuntimeOnly
		if executionMessage != "" {
			executionMessage += "; "
		}
		executionMessage += "temporary table identifiers are generated independently when execution starts"
	}

	result.Stages = append(result.Stages, AssetRenderStage{
		Kind:     "execution_sql",
		Language: "sql",
		Content:  executionSQL,
		Status:   AssetRenderStageStatusOK,
		Fidelity: executionFidelity,
		Message:  executionMessage,
	})
	return result, nil
}

func appendDirectStringSCD2MigrationStage(result *AssetRenderResult, asset *pipeline.Asset, requestedFullRefresh bool) {
	if result == nil || asset == nil || requestedFullRefresh || !asset.Materialization.IsSCD2() {
		return
	}

	operation := ""
	message := ""
	switch asset.Type {
	case pipeline.AssetTypePostgresQuery, pipeline.AssetTypeRedshiftQuery:
		operation = "migrate_postgres_scd2_target"
		message = "inspects and migrates the live PostgreSQL-compatible SCD2 target timestamp columns before submitting materializer SQL"
	case pipeline.AssetTypeMySQLQuery:
		operation = "migrate_mysql_scd2_target"
		message = "inspects and migrates the live MySQL SCD2 target timestamp columns before submitting materializer SQL"
	default:
		return
	}

	result.Status = mergeAssetRenderStatus(result.Status, AssetRenderStatusPartial)
	result.Stages = append(result.Stages, AssetRenderStage{
		Kind:        "scd2_migration",
		Language:    "json",
		Content:     mustRenderOperationJSON(map[string]any{"operation": operation, "strategy": asset.Materialization.Strategy}),
		Status:      AssetRenderStageStatusOK,
		Fidelity:    AssetRenderFidelityRuntimeOnly,
		Conditional: true,
		Message:     message,
	})
}

func directExecutionCreatesSchema(assetType pipeline.AssetType) bool {
	switch assetType {
	case pipeline.AssetTypeDuckDBQuery,
		pipeline.AssetTypeMotherduckQuery,
		pipeline.AssetTypePostgresQuery,
		pipeline.AssetTypeRedshiftQuery,
		pipeline.AssetTypeMySQLQuery,
		pipeline.AssetTypeFabricQuery,
		pipeline.AssetTypeFabricQueryLegacy:
		return true
	default:
		return false
	}
}

func supportsOrderedBatchExecutionRender(asset *pipeline.Asset) bool {
	if asset == nil {
		return false
	}
	switch asset.Type {
	case pipeline.AssetTypeDatabricksQuery,
		pipeline.AssetTypeClickHouse,
		pipeline.AssetTypeSynapseQuery:
		return true
	default:
		return false
	}
}

func appendDatabricksPreparationStages(result *AssetRenderResult, asset *pipeline.Asset) {
	if result == nil || asset == nil {
		return
	}
	if catalogName, hasCatalog := tablename.ContainerToCreate(asset.Name, strings.ToUpper); hasCatalog {
		appendDatabricksPreparationStage(result, "Catalog", "CREATE CATALOG IF NOT EXISTS "+catalogName)
	}
	if schemaName, hasSchema := tablename.SchemaToCreate(asset.Name, strings.ToUpper); hasSchema {
		appendDatabricksPreparationStage(result, "Schema", "CREATE SCHEMA IF NOT EXISTS "+schemaName)
	}
}

func appendDatabricksPreparationStage(result *AssetRenderResult, label, sql string) {
	result.Stages = append(result.Stages, AssetRenderStage{
		Kind:        "schema_preparation",
		Label:       label,
		Language:    "sql",
		Content:     sql,
		Status:      AssetRenderStageStatusOK,
		Fidelity:    AssetRenderFidelitySemantic,
		Conditional: true,
		Message:     "executed until the connection-local preparation cache records this object",
	})
}

func assetRenderConfigurationConnectionNames(info *directPipelineInfo, primary string) []string {
	names := make([]string, 0, 3)
	if assetRenderAssetIsConnectionless(info) {
		return names
	}
	if info != nil && info.Pipeline != nil && info.Asset != nil {
		if resolved, err := info.Pipeline.GetAllConnectionNamesForAsset(info.Asset); err == nil {
			for _, name := range resolved {
				if !assetRenderUsesLocalPseudoConnection(info, name) {
					names = append(names, name)
				}
			}
		}
		if isLoadAsset(info.Asset) {
			params := loadParamsFromAsset(info.Asset)
			if !assetRenderUsesLocalPseudoConnection(info, params.SourceConnection) {
				names = append(names, params.SourceConnection)
			}
		}
	}
	if !assetRenderUsesLocalPseudoConnection(info, primary) {
		names = append(names, primary)
	}
	return names
}

// `local` is a reserved Sling endpoint for filesystem-backed Load sources and
// destinations, not a named Bruin connection. The asset's source/destination
// path remains fingerprinted and its physical destination has a separate
// canonical target identity, so requiring `local` in .bruin.yml only creates a
// false runtime-only configuration result.
func assetRenderUsesLocalPseudoConnection(info *directPipelineInfo, name string) bool {
	return info != nil && info.Asset != nil && isLoadAsset(info.Asset) && isLocalLoadConnection(name)
}

func assetRenderAssetIsConnectionless(info *directPipelineInfo) bool {
	if info == nil || info.Pipeline == nil || info.Asset == nil {
		return false
	}
	names, err := info.Pipeline.GetAllConnectionNamesForAsset(info.Asset)
	return err == nil && len(names) == 0
}

func assetRenderVariableProvenance(pl *pipeline.Pipeline) []AssetRenderVariableProvenance {
	return assetRenderVariableProvenanceWithOverrides(pl, nil, "")
}

func assetRenderVariableProvenanceWithOverrides(
	pl *pipeline.Pipeline,
	overrides map[string]any,
	overrideSource string,
) []AssetRenderVariableProvenance {
	if pl == nil {
		return []AssetRenderVariableProvenance{}
	}
	names := make([]string, 0, len(pl.Variables))
	for name := range pl.Variables {
		names = append(names, name)
	}
	layers := []runcontext.VariableLayer{{
		Source: runcontext.VariableSourcePipelineDefault, Names: names,
	}}
	if len(overrides) > 0 {
		overrideNames := make([]string, 0, len(overrides))
		for name := range overrides {
			overrideNames = append(overrideNames, name)
		}
		if strings.TrimSpace(overrideSource) == "" {
			overrideSource = runcontext.VariableSourceRunOverride
		}
		layers = append(layers, runcontext.VariableLayer{Source: overrideSource, Names: overrideNames})
	}
	provenance := runcontext.ValueFreeVariableProvenance(layers...)
	result := make([]AssetRenderVariableProvenance, 0, len(provenance))
	for _, variable := range provenance {
		result = append(result, AssetRenderVariableProvenance{Name: variable.Name, Source: variable.Source})
	}
	return result
}

func finalizeAssetRenderResult(result AssetRenderResult, cfg *config.Config) AssetRenderResult {
	if cfg == nil || cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return result
	}
	redactor := mask.New(mask.InlineSensitiveValues(cfg.SelectedEnvironment.Connections))
	if redactor.Empty() {
		return result
	}

	redacted := false
	for i := range result.Stages {
		content := redactor.Mask(result.Stages[i].Content)
		message := redactor.Mask(result.Stages[i].Message)
		if content != result.Stages[i].Content || message != result.Stages[i].Message {
			result.Stages[i].Redacted = true
			redacted = true
		}
		result.Stages[i].Content = content
		result.Stages[i].Message = message
	}
	for i := range result.Issues {
		message := redactor.Mask(result.Issues[i].Message)
		redacted = redacted || message != result.Issues[i].Message
		result.Issues[i].Message = message
	}
	object := redactor.Mask(result.Asset.Target.Object)
	targetMessage := redactor.Mask(result.Asset.Target.Message)
	redacted = redacted || object != result.Asset.Target.Object || targetMessage != result.Asset.Target.Message
	result.Asset.Target.Object = object
	result.Asset.Target.Message = targetMessage
	if redacted {
		alreadyReported := false
		for _, redaction := range result.Redactions {
			if redaction.Kind == "connection_credentials" && redaction.Replacement == mask.Mask {
				alreadyReported = true
				break
			}
		}
		if !alreadyReported {
			result.Redactions = append(result.Redactions, AssetRenderRedaction{
				Kind:        "connection_credentials",
				Replacement: mask.Mask,
			})
		}
	}
	return result
}

func (s *AssetRenderService) resolveExecutionTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if s.now == nil {
			return time.Now().UTC(), nil
		}
		return s.now().UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid execution_time: %w", err)
	}
	return parsed.UTC(), nil
}

func buildAssetPlanRenderer(fs afero.Fs, pl *pipeline.Pipeline, timeWindow ExecutionTimeWindow, executionTime time.Time, runID string) (*jinja.Renderer, error) {
	macroContent, err := jinja.LoadMacros(fs, pl.MacrosPath)
	if err != nil {
		return nil, err
	}
	return jinja.NewRendererWithStartEndDatesAndMacros(
		&timeWindow.Start,
		&timeWindow.End,
		&executionTime,
		pl.Name,
		runID,
		nil,
		macroContent,
	), nil
}

func assetPlanRenderContext(ctx context.Context, cfg *config.Config, timeWindow ExecutionTimeWindow, executionTime time.Time, runID string, fullRefresh bool) context.Context {
	ctx = context.WithValue(ctx, pipeline.RunConfigStartDate, timeWindow.Start)
	ctx = context.WithValue(ctx, pipeline.RunConfigEndDate, timeWindow.End)
	ctx = context.WithValue(ctx, pipeline.RunConfigExecutionDate, executionTime)
	ctx = context.WithValue(ctx, pipeline.RunConfigRunID, runID)
	ctx = context.WithValue(ctx, pipeline.RunConfigFullRefresh, fullRefresh)
	ctx = context.WithValue(ctx, pipeline.RunConfigApplyIntervalModifiers, false)
	if cfg != nil {
		ctx = context.WithValue(ctx, config.EnvironmentContextKey, cfg.SelectedEnvironment)
		ctx = context.WithValue(ctx, config.EnvironmentNameContextKey, cfg.SelectedEnvironmentName)
	}
	return ctx
}

func renderExactStringExecutionSQL(asset *pipeline.Asset, extractor query.QueryExtractor, compiledQuery string, fullRefresh bool) (string, bool, error) {
	if !supportsDirectStringExecutionRender(asset) {
		return "", false, nil
	}
	materializer, supported, err := newDirectStringExecutionMaterializer(asset.Type, fullRefresh)
	if err != nil || !supported {
		return "", supported, err
	}
	executionSQL, err := materializer.Render(asset, compiledQuery)
	if err != nil {
		return "", true, err
	}

	// The direct DuckDB operator re-renders the materialized script for the
	// time_interval strategy because that materializer introduces date Jinja
	// expressions. Preserve that exact behavior and keep the result as one
	// canonical SQL blob rather than attempting to split or classify it.
	if asset.Materialization.Strategy == pipeline.MaterializationStrategyTimeInterval {
		rendered, renderErr := extractor.ExtractQueriesFromString(executionSQL)
		if renderErr != nil {
			return "", true, fmt.Errorf("re-render time_interval execution SQL: %w", renderErr)
		}
		if len(rendered) == 0 {
			return "", true, fmt.Errorf("time_interval execution SQL rendered empty")
		}
		if asset.Type == pipeline.AssetTypeTrinoQuery {
			statements := make([]string, 0, len(rendered))
			for _, statement := range rendered {
				if statement != nil && strings.TrimSpace(statement.Query) != "" {
					statements = append(statements, statement.Query)
				}
			}
			if len(statements) == 0 {
				return "", true, fmt.Errorf("time_interval execution SQL rendered empty")
			}
			executionSQL = strings.Join(statements, ";\n") + ";"
		} else {
			executionSQL = rendered[0].Query
		}
	}

	return executionSQL, true, nil
}

func querySourceForRenderAsset(asset *pipeline.Asset) (string, error) {
	if asset == nil {
		return "", fmt.Errorf("asset is required")
	}
	if !isQuerySensorAssetType(asset.Type) {
		return asset.ExecutableFile.Content, nil
	}

	querySource, ok := asset.Parameters.GetString("query")
	if !ok || strings.TrimSpace(querySource) == "" {
		return "", errQuerySensorQueryIsMissing
	}
	return querySource, nil
}

func compiledQueryForRenderAsset(asset *pipeline.Asset, queries []*query.Query) (string, error) {
	if len(queries) > 1 && asset != nil && asset.Materialization.Type != pipeline.MaterializationTypeNone {
		return "", fmt.Errorf("cannot enable materialization for tasks with multiple queries")
	}
	if len(queries) == 0 || queries[0] == nil || strings.TrimSpace(queries[0].Query) == "" {
		// Bruin's MSSQL and Trino operators deliberately invoke the DDL
		// materializer with an empty query because the statement is derived
		// entirely from columns.
		// Preserve that exact metadata-only path instead of requiring placeholder
		// SQL in an otherwise declarative asset.
		if asset != nil && (asset.Type == pipeline.AssetTypeMsSQLQuery ||
			asset.Type == pipeline.AssetTypeTrinoQuery ||
			athenaExecutionAllowsEmptyCompiledQuery(asset)) &&
			asset.Materialization.Type == pipeline.MaterializationTypeTable &&
			asset.Materialization.Strategy == pipeline.MaterializationStrategyDDL {
			return "", nil
		}
		return "", fmt.Errorf("no query was extracted")
	}
	return queries[0].Query, nil
}

func executionMaterializationUsesEphemeralIdentifiers(asset *pipeline.Asset, fullRefresh bool) bool {
	if asset == nil || asset.Materialization.Type != pipeline.MaterializationTypeTable {
		return false
	}
	effectiveFullRefresh := fullRefresh && asset.Materialization.Strategy != pipeline.MaterializationStrategyDDL &&
		(asset.RefreshRestricted == nil || !*asset.RefreshRestricted)
	strategy := asset.Materialization.Strategy
	switch asset.Type {
	case pipeline.AssetTypeDatabricksQuery:
		if effectiveFullRefresh {
			// Databricks' full-refresh SCD2 builders are deterministic, while
			// its ordinary create/replace path stages through a generated table.
			return strategy != pipeline.MaterializationStrategySCD2ByColumn &&
				strategy != pipeline.MaterializationStrategySCD2ByTime
		}
		return strategy == pipeline.MaterializationStrategyNone ||
			strategy == pipeline.MaterializationStrategyCreateReplace ||
			strategy == pipeline.MaterializationStrategyDeleteInsert
	case pipeline.AssetTypeClickHouse:
		return !effectiveFullRefresh && strategy == pipeline.MaterializationStrategyDeleteInsert
	case pipeline.AssetTypeSynapseQuery:
		return effectiveFullRefresh ||
			strategy == pipeline.MaterializationStrategyNone ||
			strategy == pipeline.MaterializationStrategyCreateReplace ||
			strategy == pipeline.MaterializationStrategyDeleteInsert
	case pipeline.AssetTypeDuckDBQuery, pipeline.AssetTypeMotherduckQuery:
		return !effectiveFullRefresh && (strategy == pipeline.MaterializationStrategyDeleteInsert ||
			strategy == pipeline.MaterializationStrategyMerge)
	case pipeline.AssetTypePostgresQuery, pipeline.AssetTypeRedshiftQuery:
		return !effectiveFullRefresh && (strategy == pipeline.MaterializationStrategyDeleteInsert ||
			strategy == pipeline.MaterializationStrategySCD2ByColumn ||
			strategy == pipeline.MaterializationStrategySCD2ByTime)
	case pipeline.AssetTypeMySQLQuery:
		return !effectiveFullRefresh && (strategy == pipeline.MaterializationStrategyDeleteInsert ||
			strategy == pipeline.MaterializationStrategyMerge ||
			strategy == pipeline.MaterializationStrategySCD2ByColumn ||
			strategy == pipeline.MaterializationStrategySCD2ByTime)
	case pipeline.AssetTypeBigqueryQuery, pipeline.AssetTypeSnowflakeQuery:
		return !effectiveFullRefresh && strategy == pipeline.MaterializationStrategyDeleteInsert
	case pipeline.AssetTypeMsSQLQuery, pipeline.AssetTypeVerticaQuery:
		return !effectiveFullRefresh && strategy == pipeline.MaterializationStrategyDeleteInsert
	default:
		return false
	}
}

func failedExactRenderStage(kind string, err error) AssetRenderStage {
	return AssetRenderStage{
		Kind:     kind,
		Language: "sql",
		Status:   AssetRenderStageStatusError,
		Fidelity: AssetRenderFidelityExact,
		Message:  err.Error(),
	}
}

func languageForRenderAsset(asset *pipeline.Asset) string {
	if asset == nil {
		return ""
	}
	path := asset.ExecutableFile.Path
	if path == "" {
		path = asset.DefinitionFile.Path
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py":
		return "python"
	case ".sql":
		return "sql"
	case ".yml", ".yaml":
		return "yaml"
	default:
		return ""
	}
}

func workspaceRelativeRenderPath(workspaceRoot, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	rel, err := filepath.Rel(workspaceRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func resolveWorkspaceRenderAssetPath(workspaceRoot, input string) (string, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	input = strings.TrimSpace(input)
	if filepath.IsAbs(input) {
		return "", fmt.Errorf("asset_path must be workspace-relative")
	}

	clean := filepath.Clean(filepath.FromSlash(input))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("asset_path must stay within the workspace")
	}

	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	joined, err := filepath.Abs(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("resolve asset_path: %w", err)
	}
	if !renderPathIsWithin(root, joined) {
		return "", fmt.Errorf("asset_path must stay within the workspace")
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	resolvedAsset, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrAssetNotFound
		}
		return "", fmt.Errorf("resolve asset_path symlinks: %w", err)
	}
	if !renderPathIsWithin(resolvedRoot, resolvedAsset) {
		return "", fmt.Errorf("asset_path must stay within the workspace")
	}
	return joined, nil
}

func renderPathIsWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
