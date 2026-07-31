package service

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	polyglot "github.com/tobilg/polyglot/packages/go"

	"renart/internal/authoringdiag"
	"renart/internal/sqlformat"
	"renart/internal/sqllsp"
	"renart/internal/web/model"
)

type SQLLSPRequest struct {
	AssetID            string                   `json:"asset_id"`
	Content            string                   `json:"content"`
	Connection         string                   `json:"connection,omitempty"`
	DocumentContext    string                   `json:"document_context,omitempty"`
	Position           sqllsp.Position          `json:"position,omitempty"`
	IncludeDeclaration bool                     `json:"include_declaration,omitempty"`
	NewName            string                   `json:"new_name,omitempty"`
	FormattingOptions  sqllsp.FormattingOptions `json:"formatting_options,omitempty"`
}

type SQLLSPResponse struct {
	Status      string                       `json:"status"`
	Diagnostics []sqllsp.Diagnostic          `json:"diagnostics,omitempty"`
	Completions []sqllsp.CompletionItem      `json:"completions,omitempty"`
	Locations   []sqllsp.Location            `json:"locations,omitempty"`
	Hover       *sqllsp.Hover                `json:"hover,omitempty"`
	Edit        *sqllsp.WorkspaceEdit        `json:"edit,omitempty"`
	CodeActions []sqllsp.CodeAction          `json:"code_actions,omitempty"`
	Tokens      *sqllsp.SemanticTokens       `json:"tokens,omitempty"`
	TokenLegend *sqllsp.SemanticTokensLegend `json:"token_legend,omitempty"`
	Symbols     []sqllsp.DocumentSymbol      `json:"symbols,omitempty"`
	Signature   *sqllsp.SignatureHelp        `json:"signature,omitempty"`
	Error       string                       `json:"error,omitempty"`
}

type SQLLSPDependencies struct {
	WorkspaceRoot           string
	DisableFilesystemAccess bool
	CurrentState            func() model.WorkspaceState
	ResolveAssetByID        func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error)
	// PolyglotClient returns a shared SQL validation client, or nil when one is
	// not (yet) available. It is consulted on every request so an
	// asynchronously-loaded client is picked up as soon as it is ready. May be
	// nil, in which case diagnostics fall back to the regex-based checks.
	PolyglotClient func() *polyglot.Client
}

type SQLLSPService struct {
	deps SQLLSPDependencies

	// graphForState is derived purely from the workspace state, so it is cached
	// by the state's monotonic Revision to avoid rebuilding the graph (and
	// re-inferring every SQL asset's columns) on every keystroke request.
	cacheMu          sync.Mutex
	cachedRevision   int64
	cachedGraph      sqllsp.CanonicalGraph
	cachedGraphReady bool
	buildCount       atomic.Int64

	assetCacheMu       sync.Mutex
	assetCacheRevision int64
	assetCacheEntries  map[string]*assetDiagnosticCacheEntry
	duckDBFileSchemas  *sqllsp.DuckDBFileSchemaCache
}

type assetDiagnosticCacheEntry struct {
	done     chan struct{}
	findings map[string][]TypeCheckFinding
	valid    bool
}

func NewSQLLSPService(deps SQLLSPDependencies) *SQLLSPService {
	return &SQLLSPService{
		deps:              deps,
		assetCacheEntries: map[string]*assetDiagnosticCacheEntry{},
		duckDBFileSchemas: sqllsp.NewDuckDBFileSchemaCache(),
	}
}

// NewLazyPolyglotClient returns a getter suitable for
// SQLLSPDependencies.PolyglotClient. The first call kicks off a background load
// of the native validation library and returns nil; once the library is open,
// subsequent calls return the shared client. Loading never blocks the request
// path, and a load failure simply leaves the getter returning nil (regex-only
// diagnostics).
func NewLazyPolyglotClient() func() *polyglot.Client {
	var (
		once sync.Once
		mu   sync.RWMutex
		poly *polyglot.Client
	)
	return func() *polyglot.Client {
		once.Do(func() {
			go func() {
				client, _, err := sqllsp.OpenPolyglotClient(context.Background(), sqllsp.PolyglotFFIOptions{})
				if err != nil {
					return
				}
				mu.Lock()
				poly = client
				mu.Unlock()
			}()
		})
		mu.RLock()
		defer mu.RUnlock()
		return poly
	}
}

func (s *SQLLSPService) polyglotClient() *polyglot.Client {
	if s.deps.PolyglotClient == nil {
		return nil
	}
	return s.deps.PolyglotClient()
}

func (s *SQLLSPService) Diagnostics(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	graph, doc, apiErr := s.graphAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	engine := s.newEngine(graph)
	diagnostics := engine.DiagnosticsContext(ctx, doc)
	documentContext := strings.ToLower(strings.TrimSpace(req.DocumentContext))
	if documentContext == "adhoc" || documentContext == "custom_check" {
		diagnostics = diagnosticsWithoutCode(diagnostics, "circular-dependency")
	} else {
		diagnostics = appendUniqueServiceDiagnostics(diagnostics, s.assetDiagnostics(ctx, req.AssetID, doc)...)
	}
	return SQLLSPResponse{Status: "ok", Diagnostics: diagnostics}, nil
}

func diagnosticsWithoutCode(diagnostics []sqllsp.Diagnostic, code string) []sqllsp.Diagnostic {
	filtered := make([]sqllsp.Diagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if strings.EqualFold(strings.TrimSpace(diagnostic.Code), code) {
			continue
		}
		filtered = append(filtered, diagnostic)
	}
	return filtered
}

// assetDiagnostics adds the non-SQL authoring checks that pipeline type-check
// owns (dependencies, materialization metadata, missing declared output
// columns, and template rendering). They are computed once per saved
// workspace revision and pipeline rather than on every editor keystroke.
//
// Document-scoped SQL findings are deliberately excluded here: the shared
// semantic validator above already evaluates the current unsaved buffer. A
// range-less asset finding is attached to the beginning of the document
// without pretending that an arbitrary SQL token caused a metadata problem.
func (s *SQLLSPService) assetDiagnostics(ctx context.Context, assetID string, doc sqllsp.TextDocumentItem) []sqllsp.Diagnostic {
	if s.deps.ResolveAssetByID == nil {
		return nil
	}

	state := s.deps.CurrentState()
	pipelineID, ok := pipelineIDForAsset(state, assetID)
	if !ok {
		// Notebook cells use a synthetic pipeline and do not have a stable
		// pipeline revision/cache identity. Their SQL is still validated by
		// the shared semantic path above.
		return nil
	}

	findings, ok := s.cachedAssetFindings(ctx, state.Revision, pipelineID, assetID)
	if !ok {
		return nil
	}
	return lspAssetDiagnostics(doc.Text, findings)
}

func pipelineIDForAsset(state model.WorkspaceState, assetID string) (string, bool) {
	for _, candidate := range state.Pipelines {
		for _, asset := range candidate.Assets {
			if asset.ID == assetID {
				pipelineKey := candidate.ID
				if pipelineKey == "" {
					pipelineKey = candidate.Path
				}
				if pipelineKey == "" {
					pipelineKey = candidate.Name
				}
				return pipelineKey, true
			}
		}
	}
	return "", false
}

func (s *SQLLSPService) cachedAssetFindings(ctx context.Context, revision int64, pipelineID, assetID string) ([]TypeCheckFinding, bool) {
	if revision <= 0 {
		findings := s.buildAssetFindings(ctx, assetID)
		result, ok := findings[assetID]
		return result, ok
	}

	s.assetCacheMu.Lock()
	if revision < s.assetCacheRevision {
		s.assetCacheMu.Unlock()
		return nil, false
	}
	if s.assetCacheRevision != revision {
		s.assetCacheRevision = revision
		s.assetCacheEntries = map[string]*assetDiagnosticCacheEntry{}
	}
	entry, exists := s.assetCacheEntries[pipelineID]
	if !exists {
		entry = &assetDiagnosticCacheEntry{done: make(chan struct{})}
		s.assetCacheEntries[pipelineID] = entry
	}
	s.assetCacheMu.Unlock()

	if exists {
		select {
		case <-ctx.Done():
			return nil, false
		case <-entry.done:
			if !entry.valid {
				return nil, false
			}
			result, ok := entry.findings[assetID]
			return result, ok
		}
	}

	entry.findings = s.buildAssetFindings(ctx, assetID)
	entry.valid = ctx.Err() == nil
	if ctx.Err() != nil {
		// Do not make an abandoned editor request the cached answer for the
		// rest of this workspace revision. Wake current waiters, then let the
		// next request retry the pipeline-level checks.
		s.assetCacheMu.Lock()
		if current := s.assetCacheEntries[pipelineID]; current == entry {
			delete(s.assetCacheEntries, pipelineID)
		}
		s.assetCacheMu.Unlock()
	}
	close(entry.done)
	if !entry.valid {
		return nil, false
	}
	result, ok := entry.findings[assetID]
	return result, ok
}

func (s *SQLLSPService) buildAssetFindings(ctx context.Context, assetID string) map[string][]TypeCheckFinding {
	result := map[string][]TypeCheckFinding{}
	_, parsed, _, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil || parsed == nil {
		return result
	}
	tw, err := ResolveExecutionTimeWindow(string(parsed.Schedule), "", "", time.Now().UTC())
	if err != nil {
		return result
	}
	for _, asset := range CheckPipelineAssetFindings(ctx, afero.NewOsFs(), parsed, s.deps.WorkspaceRoot, tw) {
		if asset.ID == "" {
			continue
		}
		result[asset.ID] = append([]TypeCheckFinding(nil), asset.Findings...)
	}
	return result
}

func lspAssetDiagnostics(text string, findings []TypeCheckFinding) []sqllsp.Diagnostic {
	result := make([]sqllsp.Diagnostic, 0, len(findings))
	end := sqllsp.Position{}
	if text != "" {
		end = sqllsp.PositionAt(text, min(1, len(text)))
	}
	for _, finding := range findings {
		delivery, registered := authoringdiag.TypeCheckDelivery(finding.Code)
		if !registered || delivery != authoringdiag.DeliveryAssetHeader {
			continue
		}
		severity := 2
		if finding.Severity == typeCheckSeverityError {
			severity = 1
		}
		result = append(result, sqllsp.Diagnostic{
			Range:      sqllsp.Range{Start: sqllsp.Position{}, End: end},
			Severity:   severity,
			Code:       finding.Code,
			Source:     finding.Source,
			Message:    finding.Message,
			Scope:      string(authoringdiag.ScopeAsset),
			Confidence: finding.Confidence,
		})
	}
	return result
}

func appendUniqueServiceDiagnostics(existing []sqllsp.Diagnostic, candidates ...sqllsp.Diagnostic) []sqllsp.Diagnostic {
	for _, candidate := range candidates {
		duplicate := false
		for _, current := range existing {
			if current.Code == candidate.Code && current.Message == candidate.Message && current.Range == candidate.Range {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, candidate)
		}
	}
	return existing
}

func (s *SQLLSPService) Completions(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	engine, doc, apiErr := s.engineAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	return SQLLSPResponse{Status: "ok", Completions: engine.Complete(doc, req.Position)}, nil
}

func (s *SQLLSPService) Definition(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	engine, doc, apiErr := s.engineAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	return SQLLSPResponse{Status: "ok", Locations: engine.Definition(doc, req.Position)}, nil
}

func (s *SQLLSPService) References(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	state := s.deps.CurrentState()
	asset, notebook, ok := s.selectedAsset(state, req.AssetID)
	if !ok {
		return SQLLSPResponse{}, &APIError{Status: 400, Code: "asset_not_found", Message: "asset not found"}
	}
	content := req.Content
	if strings.TrimSpace(content) == "" {
		content, _ = sqlLSPDocumentContent(asset)
	}
	doc := sqllsp.TextDocumentItem{URI: assetURI(s.deps.WorkspaceRoot, asset), LanguageID: "sql", Text: content}
	graph := s.graphForRequest(ctx, state, notebook)
	graph = s.enrichDuckDBFileRelations(ctx, graph, doc, asset.Type)
	engine := s.newEngine(graph)
	docs := s.documentsForState(state, notebook, req.AssetID, content)
	return SQLLSPResponse{Status: "ok", Locations: engine.WorkspaceReferences(doc, req.Position, docs, req.IncludeDeclaration)}, nil
}

func (s *SQLLSPService) Rename(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	engine, doc, apiErr := s.engineAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	edit, err := engine.Rename(doc, req.Position, req.NewName)
	if err != nil {
		// Not a request failure: the rename is simply unavailable here (e.g.
		// templated SQL). Report the reason so the editor can show it.
		return SQLLSPResponse{Status: "error", Error: err.Error()}, nil
	}
	return SQLLSPResponse{Status: "ok", Edit: edit}, nil
}

func (s *SQLLSPService) CodeActions(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	engine, doc, apiErr := s.engineAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	return SQLLSPResponse{Status: "ok", CodeActions: engine.CodeActions(doc)}, nil
}

func (s *SQLLSPService) Hover(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	engine, doc, apiErr := s.engineAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	return SQLLSPResponse{Status: "ok", Hover: engine.Hover(doc, req.Position)}, nil
}

func (s *SQLLSPService) SemanticTokens(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	engine, doc, apiErr := s.engineAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	tokens := engine.SemanticTokens(doc)
	return SQLLSPResponse{
		Status:      "ok",
		Tokens:      &tokens,
		TokenLegend: &sqllsp.SemanticTokensLegend{TokenTypes: sqllsp.SemanticTokenTypes(), TokenModifiers: []string{}},
	}, nil
}

func (s *SQLLSPService) DocumentSymbols(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	engine, doc, apiErr := s.engineAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	return SQLLSPResponse{Status: "ok", Symbols: engine.DocumentSymbols(doc)}, nil
}

func (s *SQLLSPService) Formatting(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	_, doc, apiErr := s.engineAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	formatted, err := sqlformat.Format(ctx, doc.Text, s.dialectForDocument(doc))
	if err != nil {
		return SQLLSPResponse{Status: "error", Error: err.Error()}, nil
	}
	return SQLLSPResponse{
		Status: "ok",
		Edit: &sqllsp.WorkspaceEdit{Changes: map[sqllsp.URI][]sqllsp.TextEdit{
			doc.URI: {
				{Range: sqllsp.Range{Start: sqllsp.Position{}, End: sqllsp.PositionAt(doc.Text, len(doc.Text))}, NewText: formatted},
			},
		}},
	}, nil
}

func (s *SQLLSPService) SignatureHelp(ctx context.Context, req SQLLSPRequest) (SQLLSPResponse, *APIError) {
	engine, doc, apiErr := s.engineAndDocument(ctx, req)
	if apiErr != nil {
		return SQLLSPResponse{}, apiErr
	}
	return SQLLSPResponse{Status: "ok", Signature: engine.SignatureHelp(doc, req.Position)}, nil
}

func (s *SQLLSPService) dialectForDocument(doc sqllsp.TextDocumentItem) string {
	state := s.deps.CurrentState()
	for _, pipeline := range state.Pipelines {
		for _, asset := range pipeline.Assets {
			if assetURI(s.deps.WorkspaceRoot, asset) == doc.URI {
				return sqllsp.DialectFromAssetType(asset.Type)
			}
		}
	}
	for _, notebook := range state.Notebooks {
		for _, cell := range notebook.Cells {
			if assetURI(s.deps.WorkspaceRoot, cell) == doc.URI {
				return sqllsp.DialectFromAssetType(cell.Type)
			}
		}
	}
	return sqlformat.DialectGeneric
}

func (s *SQLLSPService) engineAndDocument(ctx context.Context, req SQLLSPRequest) (*sqllsp.Engine, sqllsp.TextDocumentItem, *APIError) {
	graph, doc, apiErr := s.graphAndDocument(ctx, req)
	if apiErr != nil {
		return nil, sqllsp.TextDocumentItem{}, apiErr
	}
	return s.newEngine(graph), doc, nil
}

func (s *SQLLSPService) graphAndDocument(ctx context.Context, req SQLLSPRequest) (sqllsp.CanonicalGraph, sqllsp.TextDocumentItem, *APIError) {
	state := s.deps.CurrentState()
	asset, notebook, ok := s.selectedAsset(state, req.AssetID)
	if !ok {
		return sqllsp.CanonicalGraph{}, sqllsp.TextDocumentItem{}, &APIError{Status: 400, Code: "asset_not_found", Message: "asset not found"}
	}
	content := req.Content
	if strings.TrimSpace(content) == "" {
		content, _ = sqlLSPDocumentContent(asset)
	}
	doc := sqllsp.TextDocumentItem{URI: assetURI(s.deps.WorkspaceRoot, asset), LanguageID: "sql", Text: content}
	doc = s.withJinjaProjection(ctx, req.AssetID, doc)
	graph := s.graphForRequest(ctx, state, notebook)
	if strings.EqualFold(strings.TrimSpace(req.DocumentContext), "custom_check") {
		graph = graphWithCustomCheckDialect(graph, doc.URI, asset, state.Connections)
	}
	if connection := strings.TrimSpace(req.Connection); connection != "" {
		graph = graphWithDocumentConnection(graph, doc.URI, connection)
	}
	graph = s.enrichDuckDBFileRelations(ctx, graph, doc, asset.Type)
	return graph, doc, nil
}

func (s *SQLLSPService) newEngine(graph sqllsp.CanonicalGraph) *sqllsp.Engine {
	return sqllsp.NewEngineWithPolyglotOptions(graph, s.polyglotClient(), sqllsp.EngineOptions{
		DisableDuckDBFilesystemAccess: s.deps.DisableFilesystemAccess,
	})
}

func (s *SQLLSPService) enrichDuckDBFileRelations(
	ctx context.Context,
	graph sqllsp.CanonicalGraph,
	doc sqllsp.TextDocumentItem,
	assetType string,
) sqllsp.CanonicalGraph {
	if s.deps.DisableFilesystemAccess || !strings.EqualFold(sqllsp.DialectFromAssetType(assetType), "duckdb") {
		return graph
	}
	return sqllsp.EnrichDuckDBFileRelations(ctx, graph, doc, s.deps.WorkspaceRoot, s.duckDBFileSchemas)
}

// withJinjaProjection gives the live HTTP LSP fully rendered SQL from the same
// asset-scoped renderer used by preview and type-check. The projection retains
// an honest source map back to the unsaved Monaco buffer, so diagnostics and
// language features stay in template coordinates while Polyglot never sees raw
// Jinja delimiters.
//
// Rendering is best-effort while a template is being edited. If the current
// buffer is incomplete, the engine falls back to its lightweight ref/source
// expansion instead of making every LSP feature unavailable.
func (s *SQLLSPService) withJinjaProjection(
	ctx context.Context,
	assetID string,
	doc sqllsp.TextDocumentItem,
) sqllsp.TextDocumentItem {
	if s.deps.ResolveAssetByID == nil || !containsJinjaTemplate(doc.Text) {
		return doc
	}
	_, parsed, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil || parsed == nil || asset == nil {
		return doc
	}
	renderer, err := buildJinjaPreviewRenderer(ctx, parsed, asset, "", "")
	if err != nil {
		return doc
	}
	rendered, err := renderer.Render(doc.Text)
	if err != nil {
		return doc
	}
	projection := sqllsp.ProjectRenderedSQL(doc.URI, doc.Text, rendered)
	projection.ID = string(doc.URI) + "#jinja-preview"
	projection.AssetID = assetID
	doc.Projection = &projection
	return doc
}

func containsJinjaTemplate(content string) bool {
	return strings.Contains(content, "{{") ||
		strings.Contains(content, "{%") ||
		strings.Contains(content, "{#")
}

func graphWithCustomCheckDialect(
	graph sqllsp.CanonicalGraph,
	documentURI sqllsp.URI,
	asset model.Asset,
	connectionTypes map[string]string,
) sqllsp.CanonicalGraph {
	dialect := sqllsp.DialectFromAssetType(asset.Type)
	if dialect == "generic" {
		connectionType := strings.TrimSpace(connectionTypes[asset.Connection])
		if queryType, ok := queryAssetTypeForConnectionType(connectionType); ok {
			dialect = sqllsp.DialectFromAssetType(string(queryType))
		}
	}
	if dialect == "" {
		dialect = "generic"
	}
	assets := append([]sqllsp.AssetNode(nil), graph.Assets...)
	for index := range assets {
		if assets[index].URI == documentURI {
			assets[index].Dialect = dialect
			break
		}
	}
	graph.Assets = assets
	return graph
}

// graphWithDocumentConnection gives an embedded SQL query its runtime-selected
// connection without mutating the revision-cached workspace graph. Python's
// renart.query(..., connection="...") is the current caller; ordinary SQL
// assets omit the override and retain their saved connection identity.
func graphWithDocumentConnection(
	graph sqllsp.CanonicalGraph,
	documentURI sqllsp.URI,
	connection string,
) sqllsp.CanonicalGraph {
	assets := append([]sqllsp.AssetNode(nil), graph.Assets...)
	for index := range assets {
		if assets[index].URI == documentURI {
			assets[index].Connection = connection
			break
		}
	}
	graph.Assets = assets
	return graph
}

// selectedAsset finds the asset an LSP request targets: a pipeline asset or a
// notebook cell. For a cell the containing notebook is returned too, so the
// graph can be scoped to its sibling cells.
func (s *SQLLSPService) selectedAsset(state model.WorkspaceState, assetID string) (model.Asset, *model.Notebook, bool) {
	for _, pipeline := range state.Pipelines {
		for _, asset := range pipeline.Assets {
			if asset.ID == assetID {
				return asset, nil, true
			}
		}
	}
	for i := range state.Notebooks {
		for _, cell := range state.Notebooks[i].Cells {
			if cell.ID == assetID {
				return cell, &state.Notebooks[i], true
			}
		}
	}
	return model.Asset{}, nil, false
}

// documentsForState collects the SQL documents reference search runs over:
// every pipeline SQL asset and query sensor, plus — when the request targets a
// notebook cell — the sibling cells of that notebook.
func (s *SQLLSPService) documentsForState(state model.WorkspaceState, notebook *model.Notebook, selectedAssetID, selectedContent string) []sqllsp.TextDocumentItem {
	assets := make([]model.Asset, 0, 16)
	for _, pipeline := range state.Pipelines {
		assets = append(assets, pipeline.Assets...)
	}
	if notebook != nil {
		assets = append(assets, notebook.Cells...)
	}
	var docs []sqllsp.TextDocumentItem
	for _, asset := range assets {
		content, isSQLDocument := sqlLSPDocumentContent(asset)
		if !isSQLDocument {
			continue
		}
		if asset.ID == selectedAssetID {
			content = selectedContent
		}
		docs = append(docs, sqllsp.TextDocumentItem{
			URI:        assetURI(s.deps.WorkspaceRoot, asset),
			LanguageID: "sql",
			Text:       content,
		})
	}
	return docs
}

func sqlLSPDocumentContent(asset model.Asset) (string, bool) {
	normalizedType := strings.ToLower(strings.TrimSpace(asset.Type))
	if strings.HasSuffix(normalizedType, ".sql") {
		return asset.Content, true
	}
	if strings.HasSuffix(normalizedType, ".sensor.query") {
		return asset.Parameters["query"], true
	}
	return "", false
}

// graphForState returns the canonical graph for the given workspace state. The
// graph depends only on the state, so it is cached by the state's monotonic
// Revision: during editing every keystroke issues LSP requests against the same
// saved state, and rebuilding the graph each time is wasted work. A Revision of
// 0 (an unmanaged/initial state) is never cached so callers always see fresh
// results.
func (s *SQLLSPService) graphForState(ctx context.Context, state model.WorkspaceState) sqllsp.CanonicalGraph {
	revision := state.Revision
	if revision > 0 {
		s.cacheMu.Lock()
		if s.cachedGraphReady && s.cachedRevision == revision {
			graph := s.cachedGraph
			s.cacheMu.Unlock()
			return graph
		}
		s.cacheMu.Unlock()
	}

	graph := s.buildGraph(ctx, state)

	if revision > 0 {
		s.cacheMu.Lock()
		if !s.cachedGraphReady || revision >= s.cachedRevision {
			s.cachedRevision = revision
			s.cachedGraph = graph
			s.cachedGraphReady = true
		}
		s.cacheMu.Unlock()
	}
	return graph
}

func (s *SQLLSPService) buildGraph(ctx context.Context, state model.WorkspaceState) sqllsp.CanonicalGraph {
	s.buildCount.Add(1)
	var pipelineAssets []model.Asset
	for _, pipeline := range state.Pipelines {
		pipelineAssets = append(pipelineAssets, pipeline.Assets...)
	}
	nodes, columns := s.graphAssetNodes(pipelineAssets)
	graph := sqllsp.GraphFromRenartAssets(sqllsp.FileURI(s.deps.WorkspaceRoot), nodes, columns)
	return sqllsp.InferSchemaSnapshot(ctx, graph, inferenceAssetsFromModels(s.deps.WorkspaceRoot, pipelineAssets))
}

// graphForRequest returns the revision-cached pipeline graph, extended with the
// requesting notebook's cells when the request targets one. Cells are scoped to
// their notebook — pipeline assets and other notebooks never see them —
// mirroring the per-notebook DuckDB session.
func (s *SQLLSPService) graphForRequest(ctx context.Context, state model.WorkspaceState, notebook *model.Notebook) sqllsp.CanonicalGraph {
	graph := s.graphForState(ctx, state)
	if notebook == nil {
		return graph
	}
	return s.graphWithNotebookCells(ctx, graph, *notebook)
}

// graphWithNotebookCells extends base with the notebook's cells (relations with
// declared or inferred columns) and the cells' external table references (bare
// relations that resolve without claiming any columns), so sibling reads and
// warehouse tables do not surface as unresolved relations. base is the shared
// cached graph, so the extension builds fresh slices instead of appending in
// place.
func (s *SQLLSPService) graphWithNotebookCells(ctx context.Context, base sqllsp.CanonicalGraph, notebook model.Notebook) sqllsp.CanonicalGraph {
	nodes, columns := s.graphAssetNodes(notebook.Cells)
	cellGraph := sqllsp.GraphFromRenartAssets(base.WorkspaceURI, nodes, columns)

	merged := base
	merged.Assets = append(append([]sqllsp.AssetNode{}, base.Assets...), cellGraph.Assets...)
	merged.Relations = append(append([]sqllsp.RelationNode{}, base.Relations...), cellGraph.Relations...)
	merged.Schemas = append(append([]sqllsp.SchemaLayer{}, base.Schemas...), cellGraph.Schemas...)

	relationNames := make(map[string]bool, len(merged.Relations))
	for _, relation := range merged.Relations {
		relationNames[strings.ToLower(strings.TrimSpace(relation.Name))] = true
	}
	for _, cell := range notebook.Cells {
		for _, ref := range cell.ExternalRefs {
			key := strings.ToLower(strings.TrimSpace(ref))
			if key == "" || relationNames[key] {
				continue
			}
			relationNames[key] = true
			merged.Relations = append(merged.Relations, sqllsp.RelationNode{
				ID:   "relation:external:" + key,
				Name: ref,
			})
		}
	}

	return sqllsp.InferSchemaSnapshot(ctx, merged, inferenceAssetsFromModels(s.deps.WorkspaceRoot, notebook.Cells))
}

// graphAssetNodes converts workspace assets (pipeline assets or notebook cells)
// into graph nodes plus their declared columns, keyed by asset ID.
func (s *SQLLSPService) graphAssetNodes(modelAssets []model.Asset) ([]sqllsp.AssetNode, map[string][]sqllsp.ColumnInfo) {
	var nodes []sqllsp.AssetNode
	columns := map[string][]sqllsp.ColumnInfo{}
	for _, asset := range modelAssets {
		if strings.TrimSpace(asset.Name) == "" {
			continue
		}
		isSQLAsset := strings.HasSuffix(strings.ToLower(asset.Type), ".sql")
		isQuerySensor := strings.HasSuffix(strings.ToLower(asset.Type), ".sensor.query")
		kind := strings.ToLower(strings.TrimSpace(asset.Type))
		dialect := ""
		if kind == "" {
			kind = "asset"
		}
		if isSQLAsset {
			kind = "sql_model"
		}
		if isSQLAsset || isQuerySensor {
			dialect = sqllsp.DialectFromAssetType(asset.Type)
		}
		nodes = append(nodes, sqllsp.AssetNode{
			ID:         asset.ID,
			Name:       asset.Name,
			Kind:       kind,
			Dialect:    dialect,
			Connection: asset.Connection,
			URI:        assetURI(s.deps.WorkspaceRoot, asset),
		})
		for _, column := range asset.Columns {
			columns[asset.ID] = append(columns[asset.ID], columnInfoFromModelColumn(column))
		}
	}
	return nodes, columns
}

func columnInfoFromModelColumn(column model.Column) sqllsp.ColumnInfo {
	result := sqllsp.ColumnInfo{
		Name:        column.Name,
		Type:        column.Type,
		Description: column.Description,
		Nullable:    cloneBool(column.Nullable),
		PrimaryKey:  column.PrimaryKey,
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

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func inferenceAssetsFromModels(root string, assets []model.Asset) []sqllsp.InferenceAsset {
	result := make([]sqllsp.InferenceAsset, 0, len(assets))
	for _, asset := range assets {
		content, isSQL := sqlLSPDocumentContent(asset)
		if !isSQL || strings.TrimSpace(content) == "" {
			continue
		}
		result = append(result, sqllsp.InferenceAsset{
			ID:        asset.ID,
			Name:      asset.Name,
			URI:       assetURI(root, asset),
			SQL:       content,
			Dialect:   sqllsp.DialectFromAssetType(asset.Type),
			Upstreams: append([]string(nil), asset.Upstreams...),
		})
	}
	return result
}

func assetURI(root string, asset model.Asset) sqllsp.URI {
	if filepath.IsAbs(asset.Path) {
		return sqllsp.FileURI(asset.Path)
	}
	return sqllsp.FileURI(filepath.Join(root, asset.Path))
}
