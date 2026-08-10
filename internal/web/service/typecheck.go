package service

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/spf13/afero"

	"renart/internal/authoringdiag"
	"renart/internal/sqlintelligence"
	"renart/internal/sqllsp"
	webmodel "renart/internal/web/model"
)

const typeCheckRunID = "renart-type-check"

// TypeCheck loads a pipeline by its workspace ID and type-checks every asset.
// startDate/endDate are optional ISO dates used to build the Jinja date context;
// when empty they are derived from the pipeline's schedule.
func (s *PipelineService) TypeCheck(ctx context.Context, pipelineID, startDate, endDate string) (TypeCheckReport, *APIError) {
	_, absPath, err := s.resolver().DecodePipelineID(pipelineID)
	if err != nil {
		return TypeCheckReport{}, &APIError{Status: 400, Code: "invalid_pipeline_id", Message: err.Error()}
	}
	// Load the full pipeline (WithMutate, not WithOnlyPipeline) so asset content
	// and columns are populated — type checking needs the rendered SQL.
	parsed, err := s.newPipelineBuilder().CreatePipelineFromPath(ctx, absPath, pipeline.WithMutate())
	if err != nil {
		return TypeCheckReport{}, &APIError{Status: 400, Code: "pipeline_not_found", Message: err.Error()}
	}

	tw, err := ResolveExecutionTimeWindow(string(parsed.Schedule), startDate, endDate, time.Now().UTC())
	if err != nil {
		return TypeCheckReport{}, &APIError{Status: 400, Code: "invalid_time_window", Message: err.Error()}
	}

	environment := ""
	if s.selectedEnvironment != nil {
		environment = strings.TrimSpace(s.selectedEnvironment())
	}
	options := typeCheckOptions{
		RemoteCatalog: s.remoteCatalog,
		Environment:   environment,
	}
	if state, stateErr := NewWorkspaceService(s.workspaceRoot, "").ComputeState(ctx); stateErr == nil {
		workspaceGraph := buildWorkspaceCanonicalGraph(ctx, s.workspaceRoot, state)
		options.WorkspaceGraph = &workspaceGraph
		options.WorkspaceState = &state
		options.DependencyDiagnostics = append(
			[]webmodel.WorkspaceDependencyDiagnostic(nil),
			state.DependencyDiagnostics...,
		)
	}
	report := checkPipelineAt(ctx, afero.NewOsFs(), parsed, s.workspaceRoot, tw, time.Now().UTC(), options)
	report.PipelineID = pipelineID
	return report, nil
}

const (
	typeCheckStatusOK      = "ok"
	typeCheckStatusWarning = "warning"
	typeCheckStatusError   = "error"

	typeCheckSeverityError   = "error"
	typeCheckSeverityWarning = "warning"
)

// TypeCheckFinding is a single diagnostic about an asset (a type/column error
// from the SQL parser, a template-rendering failure, or an asset-source
// warning). Line/column are 1-based and point into the authoring source when a
// trustworthy render/source mapping exists, or the Python source for Python
// findings. Generated SQL without a defensible mapping remains range-less.
type TypeCheckFinding struct {
	Code        string                `json:"code"`
	Source      string                `json:"source"`
	Severity    string                `json:"severity"`
	Message     string                `json:"message"`
	Line        int                   `json:"line,omitempty"`
	Column      int                   `json:"column,omitempty"`
	EndLine     int                   `json:"end_line,omitempty"`
	EndColumn   int                   `json:"end_column,omitempty"`
	Scope       string                `json:"scope,omitempty"`
	Confidence  string                `json:"confidence,omitempty"`
	Resolutions []TypeCheckResolution `json:"resolutions,omitempty"`
}

// TypeCheckResolution is a safe semantic edit Renart can offer for a finding.
// The transaction payload deliberately contains only fields used by currently
// supported resolutions; the asset transaction endpoint remains authoritative.
type TypeCheckResolution struct {
	ID          string                          `json:"id"`
	Title       string                          `json:"title"`
	Transaction *TypeCheckResolutionTransaction `json:"transaction,omitempty"`
	Action      *TypeCheckResolutionAction      `json:"action,omitempty"`
}

type TypeCheckResolutionTransaction struct {
	Type       string                 `json:"type"`
	Column     string                 `json:"column,omitempty"`
	Dependency *TransactionDependency `json:"dependency,omitempty"`
}

const (
	typeCheckResolutionActionImportExternalRelation = "import-external-relation"
	typeCheckResolutionActionOpenAsset              = "open-asset"
)

type TypeCheckResolutionAction struct {
	Type       string `json:"type"`
	RelationID string `json:"relation_id,omitempty"`
	PipelineID string `json:"pipeline_id,omitempty"`
	AssetID    string `json:"asset_id,omitempty"`
}

// TypeCheckAsset is the per-asset result of a pipeline type check.
type TypeCheckAsset struct {
	ID       string             `json:"id,omitempty"`
	Name     string             `json:"name"`
	Type     string             `json:"type"`
	Dialect  string             `json:"dialect,omitempty"`
	Status   string             `json:"status"`
	Findings []TypeCheckFinding `json:"findings"`
}

// TypeCheckSummary aggregates finding counts across the pipeline.
type TypeCheckSummary struct {
	Assets   int `json:"assets"`
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

// TypeCheckExternalRelation is positive, ephemeral catalog evidence used by
// the canvas and import review. It is never persisted as workspace state.
type TypeCheckExternalRelation struct {
	ID                     string      `json:"id"`
	Connection             string      `json:"connection"`
	Environment            string      `json:"environment,omitempty"`
	QualifiedName          string      `json:"qualified_name"`
	SchemaName             string      `json:"schema_name,omitempty"`
	Name                   string      `json:"name"`
	Columns                []SQLColumn `json:"columns"`
	ColumnsKnown           bool        `json:"columns_known"`
	ObservedAt             string      `json:"observed_at,omitempty"`
	Stale                  bool        `json:"stale,omitempty"`
	ReferencedByAssetIDs   []string    `json:"referenced_by_asset_ids"`
	ReferencedByAssetNames []string    `json:"referenced_by_asset_names"`
}

// TypeCheckCrossPipelineReference is an authoring-only observation that links
// a SQL relation use to a producer in another pipeline before the consumer has
// declared Bruin's explicit URI dependency. It drives provisional canvas
// lineage and disappears as soon as the dependency is persisted.
type TypeCheckCrossPipelineReference struct {
	ID                   string `json:"id"`
	Status               string `json:"status"`
	Relation             string `json:"relation"`
	ConsumerAssetID      string `json:"consumer_asset_id"`
	ConsumerAssetName    string `json:"consumer_asset_name"`
	ProducerAssetID      string `json:"producer_asset_id"`
	ProducerAssetName    string `json:"producer_asset_name"`
	ProducerPipelineID   string `json:"producer_pipeline_id"`
	ProducerPipelineName string `json:"producer_pipeline_name"`
	ProducerURI          string `json:"producer_uri,omitempty"`
}

// TypeCheckReport is the full result of type-checking a pipeline.
type TypeCheckReport struct {
	Status                  string                            `json:"status"`
	PipelineID              string                            `json:"pipeline_id,omitempty"`
	PipelineName            string                            `json:"pipeline_name"`
	StartDate               string                            `json:"start_date,omitempty"`
	EndDate                 string                            `json:"end_date,omitempty"`
	Assets                  []TypeCheckAsset                  `json:"assets"`
	ExternalRelations       []TypeCheckExternalRelation       `json:"external_relations,omitempty"`
	CrossPipelineReferences []TypeCheckCrossPipelineReference `json:"cross_pipeline_references,omitempty"`
	Summary                 TypeCheckSummary                  `json:"summary"`
}

// CheckPipeline type-checks every asset in a parsed pipeline.
//
// For SQL assets it renders the asset (Jinja templates, pipeline variables, and
// start/end/execution dates) and validates the rendered query against a schema
// built from the other assets' declared and inferable columns — surfacing
// unresolved tables/columns and type errors. For non-SQL assets that produce a
// table but whose schema cannot be inferred from their definition (Python,
// ingestr, Load, or API assets without response fields/OpenAPI metadata) it
// warns when no columns are declared, since that breaks downstream type
// checking.
//
// workspaceRoot may be empty (e.g. from the CLI), in which case asset IDs are
// left blank.
func CheckPipeline(ctx context.Context, fs afero.Fs, pp *pipeline.Pipeline, workspaceRoot string, tw ExecutionTimeWindow) TypeCheckReport {
	return CheckPipelineAt(ctx, fs, pp, workspaceRoot, tw, time.Now().UTC())
}

// CheckPipelineAt is the execution-context-aware form used by planning. The
// ordinary authoring typecheck keeps calling CheckPipeline with the current
// time, while a run plan checks the exact execution timestamp its renderer
// will use.
func CheckPipelineAt(
	ctx context.Context,
	fs afero.Fs,
	pp *pipeline.Pipeline,
	workspaceRoot string,
	tw ExecutionTimeWindow,
	executionTime time.Time,
) TypeCheckReport {
	return checkPipelineAt(ctx, fs, pp, workspaceRoot, tw, executionTime, typeCheckOptions{})
}

type typeCheckOptions struct {
	RemoteCatalog         RemoteCatalogProvider
	Environment           string
	WorkspaceGraph        *sqllsp.CanonicalGraph
	WorkspaceState        *webmodel.WorkspaceState
	DependencyDiagnostics []webmodel.WorkspaceDependencyDiagnostic
}

func checkPipelineAt(
	ctx context.Context,
	fs afero.Fs,
	pp *pipeline.Pipeline,
	workspaceRoot string,
	tw ExecutionTimeWindow,
	executionTime time.Time,
	options typeCheckOptions,
) TypeCheckReport {
	now := executionTime.UTC()
	macroContent, _ := jinja.LoadMacros(fs, pp.MacrosPath)
	renderer := jinja.NewRendererWithStartEndDatesAndMacros(
		&tw.Start, &tw.End, &now, pp.Name, typeCheckRunID, jinja.Context(pp.Variables.Value()), macroContent,
	)

	report := TypeCheckReport{
		PipelineName: pp.Name,
		StartDate:    tw.StartRFC3339(),
		EndDate:      tw.EndRFC3339(),
		Assets:       []TypeCheckAsset{},
	}

	assets := make([]*pipeline.Asset, 0, len(pp.Assets))
	for _, asset := range pp.Assets {
		if asset != nil {
			assets = append(assets, asset)
		}
	}
	sort.SliceStable(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })

	snapshot := buildTypeCheckSchemaSnapshot(ctx, fs, pp, workspaceRoot, renderer, now, tw, assets)
	if options.WorkspaceGraph != nil {
		// Keep the execution-context-rendered schema for this pipeline and add
		// sibling relations from the immutable workspace graph. Replacing the
		// graph outright would discard plan-time Jinja/interval inference, while
		// omitting it makes valid cross-pipeline SQL look unresolved.
		snapshot.Graph = mergeTypeCheckWorkspaceGraph(snapshot.Graph, *options.WorkspaceGraph)
		snapshot.Schema, snapshot.Constraints, snapshot.Confidence = sqllsp.ValidationSchemaWithConstraints(snapshot.Graph)
	}
	for _, asset := range assets {
		assetSnapshot := typeCheckSnapshotWithRemoteCatalog(snapshot, pp, asset, options)
		connectionEngine := sqllsp.NewEngine(assetSnapshot.Graph)
		ac := checkAsset(ctx, pp, workspaceRoot, asset, assetSnapshot, connectionEngine)
		if options.WorkspaceState != nil {
			sourceText := assetSQLSource(asset)
			for _, unit := range assetSnapshot.RenderedUnits[asset] {
				doc := sqllsp.TextDocumentItem{
					URI:        typeCheckAssetURI(workspaceRoot, asset),
					LanguageID: "sql",
					Text:       unit.RenderedSQL,
				}
				for _, reference := range crossPipelineAuthoringReferences(*options.WorkspaceState, ac.ID, connectionEngine, doc) {
					finding := findingFromMappedLSPDiagnostic(sourceText, unit, unit.RenderedSQL, reference.diagnostic())
					finding.Resolutions = reference.typeCheckResolutions()
					ac.Findings = append(ac.Findings, finding)
					if reportReference, ok := reference.reportReference(); ok {
						appendTypeCheckCrossPipelineReference(&report, reportReference)
					}
				}
			}
		}
		for _, diagnostic := range options.DependencyDiagnostics {
			if diagnostic.AssetID == ac.ID {
				ac.Findings = append(ac.Findings, workspaceDependencyTypeCheckFinding(diagnostic))
			}
		}
		ac.Status = statusFromFindings(ac.Findings)
		report.Assets = append(report.Assets, ac)
		appendTypeCheckExternalRelations(ctx, &report, pp, workspaceRoot, asset, assetSnapshot, connectionEngine, options)
		report.Summary.Assets++
		for _, finding := range ac.Findings {
			switch finding.Severity {
			case typeCheckSeverityError:
				report.Summary.Errors++
			case typeCheckSeverityWarning:
				report.Summary.Warnings++
			}
		}
	}

	report.Status = typeCheckStatusOK
	if report.Summary.Warnings > 0 {
		report.Status = typeCheckStatusWarning
	}
	if report.Summary.Errors > 0 {
		report.Status = typeCheckStatusError
	}
	sort.Slice(report.ExternalRelations, func(i, j int) bool {
		if report.ExternalRelations[i].Connection != report.ExternalRelations[j].Connection {
			return report.ExternalRelations[i].Connection < report.ExternalRelations[j].Connection
		}
		return report.ExternalRelations[i].QualifiedName < report.ExternalRelations[j].QualifiedName
	})
	sort.Slice(report.CrossPipelineReferences, func(i, j int) bool {
		return report.CrossPipelineReferences[i].ID < report.CrossPipelineReferences[j].ID
	})
	return report
}

func appendTypeCheckCrossPipelineReference(report *TypeCheckReport, reference TypeCheckCrossPipelineReference) {
	if report == nil || strings.TrimSpace(reference.ID) == "" {
		return
	}
	for _, current := range report.CrossPipelineReferences {
		if current.ID == reference.ID {
			return
		}
	}
	report.CrossPipelineReferences = append(report.CrossPipelineReferences, reference)
}

func mergeTypeCheckWorkspaceGraph(local, workspace sqllsp.CanonicalGraph) sqllsp.CanonicalGraph {
	relationNames := make(map[string]struct{}, len(local.Relations))
	assetIDs := make(map[string]struct{}, len(local.Assets))
	for _, relation := range local.Relations {
		relationNames[strings.ToLower(strings.TrimSpace(relation.Name))] = struct{}{}
	}
	for _, asset := range local.Assets {
		assetIDs[asset.ID] = struct{}{}
	}
	addedRelationIDs := make(map[string]struct{})
	for _, relation := range workspace.Relations {
		name := strings.ToLower(strings.TrimSpace(relation.Name))
		if name == "" {
			continue
		}
		if _, exists := relationNames[name]; exists {
			continue
		}
		relationNames[name] = struct{}{}
		addedRelationIDs[relation.ID] = struct{}{}
		local.Relations = append(local.Relations, relation)
	}
	if len(addedRelationIDs) == 0 {
		return local
	}
	for _, layer := range workspace.Schemas {
		if _, added := addedRelationIDs[layer.RelationID]; added {
			local.Schemas = append(local.Schemas, layer)
		}
	}
	for _, asset := range workspace.Assets {
		if _, exists := assetIDs[asset.ID]; exists {
			continue
		}
		include := false
		for _, relationID := range asset.OutputRelations {
			if _, added := addedRelationIDs[relationID]; added {
				include = true
				break
			}
		}
		if !include {
			continue
		}
		assetIDs[asset.ID] = struct{}{}
		local.Assets = append(local.Assets, asset)
	}
	return local
}

func workspaceDependencyTypeCheckFinding(diagnostic webmodel.WorkspaceDependencyDiagnostic) TypeCheckFinding {
	severity := typeCheckSeverityWarning
	if diagnostic.Severity == typeCheckSeverityError {
		severity = typeCheckSeverityError
	}
	return TypeCheckFinding{
		Code:       diagnostic.Code,
		Source:     authoringdiag.SourceRenart,
		Severity:   severity,
		Message:    diagnostic.Message,
		Scope:      string(authoringdiag.ScopeAsset),
		Confidence: string(authoringdiag.ConfidenceHigh),
	}
}

func appendTypeCheckExternalRelations(
	ctx context.Context,
	report *TypeCheckReport,
	pp *pipeline.Pipeline,
	workspaceRoot string,
	asset *pipeline.Asset,
	snapshot typeCheckSchemaSnapshot,
	engine *sqllsp.Engine,
	options typeCheckOptions,
) {
	if report == nil || asset == nil || engine == nil || options.RemoteCatalog == nil {
		return
	}
	connection, err := targetConnectionNameForAsset(asset, pp)
	if err != nil || strings.TrimSpace(connection) == "" {
		return
	}
	scope := RemoteCatalogScope{Connection: connection, Environment: strings.TrimSpace(options.Environment)}
	catalog := options.RemoteCatalog.Snapshot(scope)
	if len(catalog.Relations) == 0 {
		return
	}
	byID := make(map[string]RemoteCatalogRelation, len(catalog.Relations))
	for _, relation := range catalog.Relations {
		byID[remoteCatalogRelationID(scope, relation.QualifiedName)] = relation
	}
	assetID := assetReportID(workspaceRoot, asset)
	appendReferences := func(text string) {
		doc := sqllsp.TextDocumentItem{
			URI:        typeCheckAssetURI(workspaceRoot, asset),
			LanguageID: "sql",
			Text:       text,
		}
		for _, reference := range engine.ExternalRelationReferences(doc) {
			remote, ok := byID[reference.RelationID]
			if !ok {
				continue
			}
			appendTypeCheckExternalRelation(report, scope, catalog, remote, assetID, asset.Name)
		}
	}
	for _, unit := range snapshot.RenderedUnits[asset] {
		appendReferences(unit.RenderedSQL)
	}
	var renderer jinja.RendererInterface
	if snapshot.Renderer != nil {
		renderer = snapshot.Renderer
		if cloned, cloneErr := snapshot.Renderer.CloneForAsset(ctx, pp, asset); cloneErr == nil {
			renderer = cloned
		}
	}
	for _, check := range asset.CustomChecks {
		text := strings.TrimSpace(check.Query)
		if text == "" {
			continue
		}
		if renderer != nil {
			if rendered, renderErr := renderer.Render(text); renderErr == nil {
				text = rendered
			}
		}
		appendReferences(text)
	}
}

func appendTypeCheckExternalRelation(
	report *TypeCheckReport,
	scope RemoteCatalogScope,
	snapshot RemoteCatalogSnapshot,
	remote RemoteCatalogRelation,
	assetID string,
	assetName string,
) {
	id := remoteCatalogRelationID(scope, remote.QualifiedName)
	for index := range report.ExternalRelations {
		if report.ExternalRelations[index].ID != id {
			continue
		}
		report.ExternalRelations[index].ReferencedByAssetIDs = appendUniqueString(report.ExternalRelations[index].ReferencedByAssetIDs, assetID)
		report.ExternalRelations[index].ReferencedByAssetNames = appendUniqueString(report.ExternalRelations[index].ReferencedByAssetNames, assetName)
		return
	}
	columns := append([]SQLColumn(nil), remote.Columns...)
	if columns == nil {
		columns = []SQLColumn{}
	}
	observedAt := ""
	if !snapshot.ObservedAt.IsZero() {
		observedAt = snapshot.ObservedAt.UTC().Format(time.RFC3339Nano)
	}
	report.ExternalRelations = append(report.ExternalRelations, TypeCheckExternalRelation{
		ID:                     id,
		Connection:             scope.Connection,
		Environment:            scope.Environment,
		QualifiedName:          remote.QualifiedName,
		SchemaName:             remote.SchemaName,
		Name:                   remote.ShortName,
		Columns:                columns,
		ColumnsKnown:           remote.ColumnsKnown,
		ObservedAt:             observedAt,
		Stale:                  snapshot.Stale,
		ReferencedByAssetIDs:   appendUniqueString(nil, assetID),
		ReferencedByAssetNames: appendUniqueString(nil, assetName),
	})
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func typeCheckSnapshotWithRemoteCatalog(
	snapshot typeCheckSchemaSnapshot,
	pp *pipeline.Pipeline,
	asset *pipeline.Asset,
	options typeCheckOptions,
) typeCheckSchemaSnapshot {
	if options.RemoteCatalog == nil || asset == nil {
		return snapshot
	}
	connection, err := targetConnectionNameForAsset(asset, pp)
	if err != nil || strings.TrimSpace(connection) == "" {
		return snapshot
	}
	environment := strings.TrimSpace(options.Environment)
	scope := RemoteCatalogScope{Connection: connection, Environment: environment}
	graph := graphWithRemoteCatalogSnapshot(snapshot.Graph, scope, options.RemoteCatalog.Snapshot(scope))
	if len(graph.Relations) == len(snapshot.Graph.Relations) {
		return snapshot
	}
	snapshot.Graph = graph
	snapshot.Schema, snapshot.Constraints, snapshot.Confidence = sqllsp.ValidationSchemaWithConstraints(graph)
	return snapshot
}

type typeCheckSchemaSnapshot struct {
	Graph         sqllsp.CanonicalGraph
	Schema        sqlintelligence.Schema
	Constraints   sqlintelligence.SchemaConstraints
	Confidence    map[string]sqlintelligence.RelationConfidence
	RenderedUnits map[*pipeline.Asset][]sqllsp.RenderedSQL
	RenderErrors  map[*pipeline.Asset]error
	Renderer      *jinja.Renderer
}

func checkAsset(ctx context.Context, pp *pipeline.Pipeline, workspaceRoot string, asset *pipeline.Asset, snapshot typeCheckSchemaSnapshot, connectionEngine *sqllsp.Engine) TypeCheckAsset {
	ac := TypeCheckAsset{
		ID:       assetReportID(workspaceRoot, asset),
		Name:     asset.Name,
		Type:     string(asset.Type),
		Findings: assetLevelTypeCheckFindings(ctx, asset, pp, true),
	}

	dialect, dialectErr := AssetTypeToDialect(asset.Type)
	if dialectErr != nil {
		return finishAssetTypeCheck(ctx, pp, workspaceRoot, asset, snapshot, connectionEngine, ac)
	}

	ac.Dialect = dialect
	sourceText := assetSQLSource(asset)
	if strings.TrimSpace(sourceText) == "" {
		return finishAssetTypeCheck(ctx, pp, workspaceRoot, asset, snapshot, connectionEngine, ac)
	}

	units := snapshot.RenderedUnits[asset]
	renderErr := snapshot.RenderErrors[asset]
	if renderErr != nil {
		ac.Findings = append(ac.Findings, templateRenderFinding(renderErr))
		return finishAssetTypeCheck(ctx, pp, workspaceRoot, asset, snapshot, connectionEngine, ac)
	}

	sources := sqlintelligence.SchemaColumnSourceMethods{}
	ApplyAssetSQLDefinitionColumns(ctx, pp, asset, sqlintelligence.Schema{}, sources)

	for unitIndex, unit := range units {
		renderedQuery := unit.RenderedSQL
		for _, diagnostic := range connectionEngine.CrossConnectionDiagnostics(sqllsp.TextDocumentItem{
			URI:        typeCheckAssetURI(workspaceRoot, asset),
			LanguageID: "sql",
			Text:       renderedQuery,
		}) {
			ac.Findings = append(ac.Findings, findingFromMappedLSPDiagnostic(sourceText, unit, renderedQuery, diagnostic))
		}
		externalDoc := sqllsp.TextDocumentItem{
			URI:        typeCheckAssetURI(workspaceRoot, asset),
			LanguageID: "sql",
			Text:       renderedQuery,
		}
		for _, diagnostic := range connectionEngine.ExternalRelationDiagnostics(externalDoc) {
			finding := findingFromMappedLSPDiagnostic(sourceText, unit, renderedQuery, diagnostic)
			finding.Resolutions = externalRelationResolutions(connectionEngine, externalDoc, diagnostic)
			ac.Findings = append(ac.Findings, finding)
		}
		var expectedOutput []sqlintelligence.SchemaColumn
		if unitIndex == len(units)-1 {
			expectedOutput = declaredAssetOutputColumns(asset)
		}
		validation, err := sqlintelligence.ValidateSQL(ctx, sqlintelligence.ValidationRequest{
			URI:                 string(typeCheckAssetURI(workspaceRoot, asset)),
			SQL:                 renderedQuery,
			Dialect:             dialect,
			Schema:              snapshot.Schema,
			SchemaConstraints:   snapshot.Constraints,
			RelationConfidence:  snapshot.Confidence,
			ColumnSourceMethods: sources,
			ExpectedOutput:      expectedOutput,
		})
		if err != nil {
			ac.Findings = append(ac.Findings, TypeCheckFinding{
				Code:       authoringdiag.CodeSQLValidationFailed,
				Source:     authoringdiag.SourceRenart,
				Severity:   typeCheckSeverityError,
				Message:    "Failed to parse SQL: " + err.Error(),
				Scope:      string(authoringdiag.ScopeDocument),
				Confidence: string(authoringdiag.ConfidenceLow),
			})
			continue
		}
		for _, diagnostic := range validation.Diagnostics {
			ac.Findings = append(ac.Findings, findingFromMappedAuthoringDiagnostic(sourceText, unit, diagnostic))
		}
	}

	return finishAssetTypeCheck(ctx, pp, workspaceRoot, asset, snapshot, connectionEngine, ac)
}

func finishAssetTypeCheck(
	ctx context.Context,
	pp *pipeline.Pipeline,
	workspaceRoot string,
	asset *pipeline.Asset,
	snapshot typeCheckSchemaSnapshot,
	connectionEngine *sqllsp.Engine,
	ac TypeCheckAsset,
) TypeCheckAsset {
	findings, dialect := customCheckTypeCheckFindings(
		ctx,
		pp,
		workspaceRoot,
		asset,
		snapshot,
		connectionEngine,
	)
	ac.Findings = append(ac.Findings, findings...)
	if ac.Dialect == "" && len(asset.CustomChecks) > 0 {
		ac.Dialect = dialect
	}
	ac.Status = statusFromFindings(ac.Findings)
	return ac
}

func customCheckTypeCheckFindings(
	ctx context.Context,
	pp *pipeline.Pipeline,
	workspaceRoot string,
	asset *pipeline.Asset,
	snapshot typeCheckSchemaSnapshot,
	connectionEngine *sqllsp.Engine,
) ([]TypeCheckFinding, string) {
	if asset == nil || len(asset.CustomChecks) == 0 {
		return nil, ""
	}
	dialect := customCheckDialect(asset, pp)
	var renderer jinja.RendererInterface
	if snapshot.Renderer != nil {
		renderer = snapshot.Renderer
		if cloned, err := snapshot.Renderer.CloneForAsset(ctx, pp, asset); err == nil {
			renderer = cloned
		}
	}
	findings := make([]TypeCheckFinding, 0)
	for _, check := range asset.CustomChecks {
		queryText := strings.TrimSpace(check.Query)
		if queryText == "" {
			continue
		}
		label := strings.TrimSpace(check.Name)
		if label == "" {
			label = "unnamed"
		}
		prefix := "Custom check " + strconv.Quote(label) + ": "
		renderedQuery := queryText
		if renderer != nil {
			var err error
			renderedQuery, err = renderer.Render(queryText)
			if err != nil {
				finding := templateRenderFinding(err)
				finding.Message = prefix + finding.Message
				findings = append(findings, finding)
				continue
			}
		}
		doc := sqllsp.TextDocumentItem{
			URI:        typeCheckAssetURI(workspaceRoot, asset),
			LanguageID: "sql",
			Text:       renderedQuery,
		}
		for _, diagnostic := range connectionEngine.CrossConnectionDiagnostics(doc) {
			severity := typeCheckSeverityWarning
			if diagnostic.Severity == 1 {
				severity = typeCheckSeverityError
			}
			findings = append(findings, TypeCheckFinding{
				Code:       diagnostic.Code,
				Source:     diagnostic.Source,
				Severity:   severity,
				Message:    prefix + diagnostic.Message,
				Scope:      string(authoringdiag.ScopeDocument),
				Confidence: diagnostic.Confidence,
			})
		}
		for _, diagnostic := range connectionEngine.ExternalRelationDiagnostics(doc) {
			finding := TypeCheckFinding{
				Code:        diagnostic.Code,
				Source:      diagnostic.Source,
				Severity:    typeCheckSeverityWarning,
				Message:     prefix + diagnostic.Message,
				Scope:       string(authoringdiag.ScopeDocument),
				Confidence:  diagnostic.Confidence,
				Resolutions: externalRelationResolutions(connectionEngine, doc, diagnostic),
			}
			findings = append(findings, finding)
		}
		validation, err := sqlintelligence.ValidateSQL(ctx, sqlintelligence.ValidationRequest{
			URI:                string(doc.URI),
			SQL:                renderedQuery,
			Dialect:            dialect,
			Schema:             snapshot.Schema,
			SchemaConstraints:  snapshot.Constraints,
			RelationConfidence: snapshot.Confidence,
		})
		if err != nil {
			findings = append(findings, TypeCheckFinding{
				Code:       authoringdiag.CodeSQLValidationFailed,
				Source:     authoringdiag.SourceRenart,
				Severity:   typeCheckSeverityError,
				Message:    prefix + "Failed to parse SQL: " + err.Error(),
				Scope:      string(authoringdiag.ScopeDocument),
				Confidence: string(authoringdiag.ConfidenceLow),
			})
			continue
		}
		for _, diagnostic := range validation.Diagnostics {
			findings = append(findings, TypeCheckFinding{
				Code:       diagnostic.Code,
				Source:     diagnostic.Source,
				Severity:   normalizeSeverity(string(diagnostic.Severity)),
				Message:    prefix + diagnostic.Message,
				Scope:      string(diagnostic.Scope),
				Confidence: string(diagnostic.Confidence),
			})
		}
	}
	return findings, dialect
}

func externalRelationResolutions(engine *sqllsp.Engine, doc sqllsp.TextDocumentItem, diagnostic sqllsp.Diagnostic) []TypeCheckResolution {
	if engine == nil || diagnostic.Code != authoringdiag.CodeExternalRelation {
		return nil
	}
	for _, reference := range engine.ExternalRelationReferences(doc) {
		if reference.Range != diagnostic.Range {
			continue
		}
		return []TypeCheckResolution{{
			ID:    "import-external-relation-" + strings.TrimPrefix(reference.RelationID, "relation:remote_catalog:"),
			Title: "Import source asset",
			Action: &TypeCheckResolutionAction{
				Type:       typeCheckResolutionActionImportExternalRelation,
				RelationID: reference.RelationID,
			},
		}}
	}
	return nil
}

func customCheckDialect(asset *pipeline.Asset, pp *pipeline.Pipeline) string {
	if dialect, err := AssetTypeToDialect(asset.Type); err == nil {
		return dialect
	}
	if connectionType, ok := pipeline.AssetTypeConnectionMapping[asset.Type]; ok {
		if queryType, found := queryAssetTypeForConnectionType(connectionType); found {
			if dialect, err := AssetTypeToDialect(queryType); err == nil {
				return dialect
			}
		}
	}
	if pp == nil {
		return "generic"
	}
	connection, _ := targetConnectionNameForAsset(asset, pp)
	for connectionType, connectionName := range pp.DefaultConnections {
		if strings.EqualFold(strings.TrimSpace(connectionName), strings.TrimSpace(connection)) {
			if queryType, ok := queryAssetTypeForConnectionType(connectionType); ok {
				if dialect, err := AssetTypeToDialect(queryType); err == nil {
					return dialect
				}
			}
		}
	}
	return "generic"
}

func declaredAssetOutputColumns(asset *pipeline.Asset) []sqlintelligence.SchemaColumn {
	if asset == nil || len(asset.Columns) == 0 {
		return nil
	}
	columns := make([]sqlintelligence.SchemaColumn, 0, len(asset.Columns))
	for _, column := range asset.Columns {
		name := strings.TrimSpace(column.Name)
		if name == "" {
			continue
		}
		var nullable *bool
		if column.Nullable.Value != nil {
			value := *column.Nullable.Value
			nullable = &value
		}
		columns = append(columns, sqlintelligence.SchemaColumn{Name: name, Type: strings.TrimSpace(column.Type), Nullable: nullable})
	}
	return columns
}

// CheckPipelineAssetFindings runs the typecheck rules that describe an asset
// or its authoring metadata without running semantic SQL validation. The LSP
// uses this pass when a saved workspace revision changes, while per-edit SQL
// diagnostics continue through the shared semantic validator against the
// unsaved document.
func CheckPipelineAssetFindings(
	ctx context.Context,
	fs afero.Fs,
	pp *pipeline.Pipeline,
	workspaceRoot string,
	tw ExecutionTimeWindow,
) []TypeCheckAsset {
	if pp == nil {
		return nil
	}
	now := time.Now().UTC()
	macroContent, _ := jinja.LoadMacros(fs, pp.MacrosPath)
	renderer := jinja.NewRendererWithStartEndDatesAndMacros(
		&tw.Start, &tw.End, &now, pp.Name, typeCheckRunID, jinja.Context(pp.Variables.Value()), macroContent,
	)
	assets := make([]*pipeline.Asset, 0, len(pp.Assets))
	for _, asset := range pp.Assets {
		if asset != nil {
			assets = append(assets, asset)
		}
	}
	sort.SliceStable(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })

	result := make([]TypeCheckAsset, 0, len(assets))
	for _, asset := range assets {
		findings := assetLevelTypeCheckFindings(ctx, asset, pp, false)
		if _, dialectErr := AssetTypeToDialect(asset.Type); dialectErr == nil && strings.TrimSpace(assetSQLSource(asset)) != "" {
			if _, err := renderAssetQueries(ctx, fs, renderer, now, tw, pp, asset); err != nil {
				findings = append(findings, templateRenderFinding(err))
			}
		}
		result = append(result, TypeCheckAsset{
			ID:       assetReportID(workspaceRoot, asset),
			Name:     asset.Name,
			Type:     string(asset.Type),
			Status:   statusFromFindings(findings),
			Findings: findings,
		})
	}
	return result
}

func assetLevelTypeCheckFindings(ctx context.Context, asset *pipeline.Asset, pp *pipeline.Pipeline, includeDocumentFindings bool) []TypeCheckFinding {
	findings := make([]TypeCheckFinding, 0)
	if parseError := pipelineAssetParseError(asset); parseError != "" {
		findings = append(findings, TypeCheckFinding{
			Code:       authoringdiag.CodeAssetDefinitionParseFailed,
			Source:     authoringdiag.SourceRenart,
			Severity:   typeCheckSeverityError,
			Message:    "Asset definition could not be parsed: " + parseError,
			Scope:      string(authoringdiag.ScopeAsset),
			Confidence: string(authoringdiag.ConfidenceHigh),
		})
	}
	findings = append(findings, dependencyTypeCheckFindings(ctx, asset, pp)...)
	findings = append(findings, materializationTypeCheckFindings(asset, pp)...)
	if _, dialectErr := AssetTypeToDialect(asset.Type); dialectErr == nil {
		return findings
	}
	if includeDocumentFindings {
		findings = append(findings, pythonQueryDependencyFindings(ctx, asset, pp)...)
	}
	// A relation whose output cannot be derived from its committed definition
	// needs an explicit schema contract. This is an error: downstream column
	// validation would otherwise be silently disabled for that branch.
	if assetProducesSchemaContract(asset) && len(asset.Columns) == 0 && !assetSchemaAutomaticallyInferable(ctx, pp, asset) {
		findings = append(findings, TypeCheckFinding{
			Code:       authoringdiag.CodeMissingDeclaredColumns,
			Source:     authoringdiag.SourceRenart,
			Severity:   typeCheckSeverityError,
			Message:    "Output schema cannot be inferred from this " + string(asset.Type) + " asset definition. Declare columns so downstream assets can be type checked.",
			Scope:      string(authoringdiag.ScopeAsset),
			Confidence: string(authoringdiag.ConfidenceHigh),
		})
	}
	return findings
}

func pipelineAssetParseError(asset *pipeline.Asset) string {
	if asset == nil || asset.Meta == nil {
		return ""
	}
	return strings.TrimSpace(asset.Meta[parseErrorMetaKey])
}

func templateRenderFinding(err error) TypeCheckFinding {
	return TypeCheckFinding{
		Code:       authoringdiag.CodeTemplateRenderFailed,
		Source:     authoringdiag.SourceRenart,
		Severity:   typeCheckSeverityError,
		Message:    "Failed to render template: " + err.Error(),
		Scope:      string(authoringdiag.ScopeAsset),
		Confidence: string(authoringdiag.ConfidenceHigh),
	}
}

func buildTypeCheckSchemaSnapshot(ctx context.Context, fs afero.Fs, pp *pipeline.Pipeline, workspaceRoot string, renderer *jinja.Renderer, now time.Time, tw ExecutionTimeWindow, assets []*pipeline.Asset) typeCheckSchemaSnapshot {
	snapshot := typeCheckSchemaSnapshot{
		RenderedUnits: map[*pipeline.Asset][]sqllsp.RenderedSQL{},
		RenderErrors:  map[*pipeline.Asset]error{},
		Renderer:      renderer,
	}
	nodes := make([]sqllsp.AssetNode, 0, len(assets))
	columns := make(map[string][]sqllsp.ColumnInfo, len(assets))
	inferenceAssets := make([]sqllsp.InferenceAsset, 0, len(assets))
	definitionSchemas := newAssetDefinitionSchemaResolver(pp)
	for _, asset := range assets {
		if asset == nil || strings.TrimSpace(asset.Name) == "" {
			continue
		}
		connection, _ := targetConnectionNameForAsset(asset, pp)
		dialect, dialectErr := AssetTypeToDialect(asset.Type)
		node := sqllsp.AssetNode{
			ID:         asset.Name,
			Name:       asset.Name,
			Kind:       strings.ToLower(strings.TrimSpace(string(asset.Type))),
			Connection: strings.TrimSpace(connection),
			URI:        typeCheckAssetURI(workspaceRoot, asset),
		}
		if dialectErr == nil {
			node.Kind = "sql_model"
			node.Dialect = dialect
		}
		nodes = append(nodes, node)

		declaredColumns := definitionSchemas.Available(ctx, asset)
		for _, column := range declaredColumns {
			if strings.TrimSpace(column.Name) != "" {
				columns[asset.Name] = append(columns[asset.Name], columnInfoFromPipelineColumn(column))
			}
		}

		sourceText := assetSQLSource(asset)
		if dialectErr != nil || strings.TrimSpace(sourceText) == "" {
			continue
		}
		queries, err := renderAssetQueries(ctx, fs, renderer, now, tw, pp, asset)
		if err != nil {
			snapshot.RenderErrors[asset] = err
			continue
		}
		units := make([]sqllsp.RenderedSQL, 0, len(queries))
		for _, renderedQuery := range queries {
			units = append(units, sqllsp.ProjectRenderedSQL(node.URI, sourceText, renderedQuery))
		}
		snapshot.RenderedUnits[asset] = units
		if len(queries) == 0 {
			continue
		}
		upstreams := make([]string, 0, len(asset.Upstreams))
		for _, upstream := range asset.Upstreams {
			if upstream.Type == "asset" && strings.TrimSpace(upstream.Value) != "" {
				upstreams = append(upstreams, upstream.Value)
			}
		}
		inferenceAssets = append(inferenceAssets, sqllsp.InferenceAsset{
			ID:        asset.Name,
			Name:      asset.Name,
			URI:       node.URI,
			SQL:       units[len(units)-1].RenderedSQL,
			Dialect:   dialect,
			Upstreams: upstreams,
		})
	}

	snapshot.Graph = sqllsp.GraphFromRenartAssets(sqllsp.FileURI(workspaceRoot), nodes, columns)
	snapshot.Graph = resolveAuthoringSchemaGraph(ctx, snapshot.Graph, pp, inferenceAssets)
	snapshot.Schema, snapshot.Constraints, snapshot.Confidence = sqllsp.ValidationSchemaWithConstraints(snapshot.Graph)
	return snapshot
}

func columnInfoFromPipelineColumn(column pipeline.Column) sqllsp.ColumnInfo {
	result := sqllsp.ColumnInfo{
		Name:        column.Name,
		Type:        column.Type,
		Description: column.Description,
		PrimaryKey:  column.PrimaryKey,
	}
	if column.Nullable.Value != nil {
		nullable := *column.Nullable.Value
		result.Nullable = &nullable
	}
	if column.ForeignKey != nil {
		table := strings.TrimSpace(column.ForeignKey.Table)
		targetColumn := strings.TrimSpace(column.ForeignKey.Column)
		if table != "" && targetColumn != "" {
			result.ForeignKey = &sqllsp.ColumnReference{Table: table, Column: targetColumn}
		}
	}
	return result
}

func typeCheckAssetURI(workspaceRoot string, asset *pipeline.Asset) sqllsp.URI {
	path := asset.ExecutableFile.Path
	if path == "" {
		path = asset.DefinitionFile.Path
	}
	if path == "" {
		path = strings.ReplaceAll(asset.Name, ".", "_") + ".sql"
	}
	if workspaceRoot != "" && !filepath.IsAbs(path) {
		path = filepath.Join(workspaceRoot, path)
	}
	return sqllsp.FileURI(path)
}

func materializationTypeCheckFindings(asset *pipeline.Asset, pl ...*pipeline.Pipeline) []TypeCheckFinding {
	if asset == nil {
		return nil
	}
	findings := make([]TypeCheckFinding, 0, 3)
	addError := func(message string) {
		findings = append(findings, TypeCheckFinding{
			Code:       authoringdiag.CodeInvalidMaterialization,
			Source:     authoringdiag.SourceRenart,
			Severity:   typeCheckSeverityError,
			Message:    message,
			Scope:      string(authoringdiag.ScopeAsset),
			Confidence: string(authoringdiag.ConfidenceHigh),
		})
	}
	addWarning := func(message string, resolutions ...TypeCheckResolution) {
		findings = append(findings, TypeCheckFinding{
			Code:        authoringdiag.CodeInactiveMaterialization,
			Source:      authoringdiag.SourceRenart,
			Severity:    typeCheckSeverityWarning,
			Message:     message,
			Scope:       string(authoringdiag.ScopeAsset),
			Confidence:  string(authoringdiag.ConfidenceHigh),
			Resolutions: resolutions,
		})
	}

	var parsedPipeline *pipeline.Pipeline
	if len(pl) > 0 {
		parsedPipeline = pl[0]
	}
	destinationType := materializationDestinationType(asset, parsedPipeline, nil)
	profile := materializationProfileFor(asset, destinationType)
	// Seeds, sensors, and other dedicated runtimes own their write semantics
	// outside the generic SQL/Python/loader materialization contract. An empty
	// capability profile therefore means there is nothing for this validator to
	// check; treating their absent generic block as mode "none" is a false error.
	if len(profile.Modes) == 0 {
		return findings
	}
	capability, capabilityKnown := materializationCapabilityForMode(profile, normalizedMaterializationMode(asset))
	if err := validateMaterializationCapability(asset, destinationType); err != nil {
		addError("Invalid materialization: " + err.Error())
		return findings
	}

	strategy := strings.ToLower(strings.TrimSpace(string(asset.Materialization.Strategy)))
	if isAPIAsset(asset) || isLoadAsset(asset) {
		if err := validateLoaderMaterialization(asset); err != nil {
			addError("Invalid materialization: " + err.Error())
			return findings
		}
	}
	if isLoadAsset(asset) {
		params := loadParamsFromAsset(asset)
		if params.SourceConnection == "" {
			addError("Invalid load asset: source_connection is required")
		}
		if params.SourceTable == "" {
			addError("Invalid load asset: source_table is required")
		}
		if isLocalLoadConnection(asset.Connection) && params.DestinationObject == "" {
			addError("Invalid load asset: a local target requires destination_object")
		}
		for _, removedKey := range []string{"destination_connection", "destination_table", "mode"} {
			if value, ok := asset.Parameters.GetString(removedKey); ok && strings.TrimSpace(value) != "" {
				addError("Invalid load asset: parameters." + removedKey + " was removed; use top-level connection, the asset name, and materialization instead")
			}
		}
	}

	if strategy == "merge" && len(asset.ColumnNamesWithPrimaryKey()) == 0 {
		addError("Invalid materialization: merge needs at least one primary-key column")
	}

	incrementalKey := strings.TrimSpace(asset.Materialization.IncrementalKey)
	keyIsActive := capabilityKnown && (capability.SupportsIncrementalKey || capability.RequiresIncrementalKey)
	if keyIsActive && incrementalKey != "" && !assetHasColumn(asset, incrementalKey) {
		addError("Invalid materialization: incremental/update key " + incrementalKey + " is not declared as a column")
	}
	if capabilityKnown && capability.RequiresIncrementalKey && incrementalKey == "" {
		addError("Invalid materialization: strategy " + strategy + " needs an incremental key")
	}
	if capabilityKnown && capability.RequiresTimeGranularity && strings.TrimSpace(string(asset.Materialization.TimeGranularity)) == "" {
		addError("Invalid materialization: time_interval needs a time granularity")
	}
	granularity := strings.TrimSpace(string(asset.Materialization.TimeGranularity))
	if granularity != "" && granularity != string(pipeline.MaterializationTimeGranularityDate) && granularity != string(pipeline.MaterializationTimeGranularityTimestamp) {
		addError("Invalid materialization: time granularity must be date or timestamp")
	}
	if capabilityKnown && strings.TrimSpace(asset.Materialization.PartitionBy) != "" && !capability.SupportsPartitionBy {
		addWarning(
			"Inactive materialization metadata: partition_by is not used by the selected strategy and destination",
			TypeCheckResolution{
				ID:    "delete-inactive-partition-by",
				Title: "Delete inactive partition setting",
				Transaction: &TypeCheckResolutionTransaction{
					Type: TxMaterializationPartitionByClear,
				},
			},
		)
	}
	if capabilityKnown && len(asset.Materialization.ClusterBy) > 0 && !capability.SupportsClusterBy {
		addWarning(
			"Inactive materialization metadata: cluster_by is not used by the selected strategy and destination",
			TypeCheckResolution{
				ID:    "delete-inactive-cluster-by",
				Title: "Delete inactive clustering settings",
				Transaction: &TypeCheckResolutionTransaction{
					Type: TxMaterializationClusterByClear,
				},
			},
		)
	}

	for _, column := range asset.Columns {
		if strategy != "merge" && (column.UpdateOnMerge || strings.TrimSpace(column.MergeSQL) != "") {
			addWarning(
				"Inactive materialization metadata: column "+column.Name+" keeps merge-only settings that will be used if merge is selected again",
				TypeCheckResolution{
					ID:    "delete-inactive-merge-settings-" + column.Name,
					Title: "Delete inactive merge settings",
					Transaction: &TypeCheckResolutionTransaction{
						Type:   TxColumnMergeSettingsClear,
						Column: column.Name,
					},
				},
			)
		}
	}
	return findings
}

func assetHasColumn(asset *pipeline.Asset, name string) bool {
	for _, column := range asset.Columns {
		if strings.EqualFold(strings.TrimSpace(column.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func renderAssetQueries(ctx context.Context, fs afero.Fs, renderer *jinja.Renderer, now time.Time, tw ExecutionTimeWindow, pp *pipeline.Pipeline, asset *pipeline.Asset) ([]string, error) {
	fetchCtx := context.WithValue(ctx, pipeline.RunConfigStartDate, tw.Start)
	fetchCtx = context.WithValue(fetchCtx, pipeline.RunConfigEndDate, tw.End)
	fetchCtx = context.WithValue(fetchCtx, pipeline.RunConfigExecutionDate, now)
	fetchCtx = context.WithValue(fetchCtx, pipeline.RunConfigRunID, typeCheckRunID)

	extractor := &query.WholeFileExtractor{Fs: fs, Renderer: renderer}
	cloned, err := extractor.CloneForAsset(fetchCtx, pp, asset)
	if err != nil {
		return nil, err
	}
	extracted, err := cloned.ExtractQueriesFromString(assetSQLSource(asset))
	if err != nil {
		return nil, err
	}

	queries := make([]string, 0, len(extracted))
	for _, q := range extracted {
		if strings.TrimSpace(q.Query) != "" {
			queries = append(queries, q.Query)
		}
	}
	return queries, nil
}

// assetSQLSource keeps semantic validation aligned with the editor document.
// Query sensors store their editable SQL in parameters.query rather than in an
// executable file; treating the YAML definition as SQL produces false syntax
// diagnostics and maps ranges to the wrong source text.
func assetSQLSource(asset *pipeline.Asset) string {
	if asset == nil {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".sensor.query") {
		queryText, _ := asset.Parameters.GetString("query")
		return queryText
	}
	return asset.ExecutableFile.Content
}

func findingFromMappedLSPDiagnostic(sourceText string, unit sqllsp.RenderedSQL, renderedSQL string, diagnostic sqllsp.Diagnostic) TypeCheckFinding {
	severity := typeCheckSeverityWarning
	if diagnostic.Severity == 1 {
		severity = typeCheckSeverityError
	}
	finding := TypeCheckFinding{
		Code:       diagnostic.Code,
		Source:     diagnostic.Source,
		Severity:   severity,
		Message:    diagnostic.Message,
		Scope:      diagnostic.Scope,
		Confidence: string(authoringdiag.ConfidenceLow),
	}
	generatedStart := sqllsp.ByteOffset(renderedSQL, diagnostic.Range.Start)
	generatedEnd := sqllsp.ByteOffset(renderedSQL, diagnostic.Range.End)
	templateStart, templateEnd, confidence, ok := unit.TemplateOffsetsForGenerated(generatedStart, generatedEnd)
	if !ok {
		return finding
	}
	start := sqllsp.PositionAt(sourceText, templateStart)
	end := sqllsp.PositionAt(sourceText, templateEnd)
	finding.Line = start.Line + 1
	finding.Column = start.Character + 1
	finding.EndLine = end.Line + 1
	finding.EndColumn = end.Character + 1
	finding.Confidence = lowerDiagnosticConfidence(diagnostic.Confidence, confidence)
	return finding
}

func findingFromMappedAuthoringDiagnostic(sourceText string, unit sqllsp.RenderedSQL, diagnostic authoringdiag.Diagnostic) TypeCheckFinding {
	finding := TypeCheckFinding{
		Code:       diagnostic.Code,
		Source:     diagnostic.Source,
		Severity:   normalizeSeverity(string(diagnostic.Severity)),
		Message:    diagnostic.Message,
		Scope:      string(diagnostic.Scope),
		Confidence: string(diagnostic.Confidence),
	}
	if diagnostic.StartByte == nil || diagnostic.EndByte == nil {
		return finding
	}
	templateStart, templateEnd, confidence, ok := unit.TemplateOffsetsForGenerated(*diagnostic.StartByte, *diagnostic.EndByte)
	if !ok {
		finding.Confidence = string(authoringdiag.ConfidenceLow)
		return finding
	}
	start := sqllsp.PositionAt(sourceText, templateStart)
	end := sqllsp.PositionAt(sourceText, templateEnd)
	finding.Line = start.Line + 1
	finding.Column = start.Character + 1
	finding.EndLine = end.Line + 1
	finding.EndColumn = end.Character + 1
	finding.Confidence = lowerDiagnosticConfidence(string(diagnostic.Confidence), confidence)
	return finding
}

func lowerDiagnosticConfidence(left, right string) string {
	rank := func(value string) int {
		switch value {
		case string(authoringdiag.ConfidenceHigh):
			return 3
		case string(authoringdiag.ConfidenceMedium):
			return 2
		default:
			return 1
		}
	}
	if rank(left) <= rank(right) {
		if left == "" {
			return string(authoringdiag.ConfidenceLow)
		}
		return left
	}
	return right
}

func normalizeSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "error":
		return typeCheckSeverityError
	case "warning", "warn":
		return typeCheckSeverityWarning
	default:
		// Treat anything else (info/hint) as a warning so it is still surfaced
		// without failing the check.
		return typeCheckSeverityWarning
	}
}

func statusFromFindings(findings []TypeCheckFinding) string {
	status := typeCheckStatusOK
	for _, finding := range findings {
		if finding.Severity == typeCheckSeverityError {
			return typeCheckStatusError
		}
		if finding.Severity == typeCheckSeverityWarning {
			status = typeCheckStatusWarning
		}
	}
	return status
}

func assetReportID(workspaceRoot string, asset *pipeline.Asset) string {
	if strings.TrimSpace(workspaceRoot) == "" {
		return ""
	}
	path := asset.ExecutableFile.Path
	if path == "" {
		path = asset.DefinitionFile.Path
	}
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(workspaceRoot, path)
	if err != nil {
		rel = path
	}
	return EncodeID(filepath.ToSlash(rel))
}
