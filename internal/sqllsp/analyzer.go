package sqllsp

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	polyglot "github.com/tobilg/polyglot/packages/go"

	"renart/internal/authoringdiag"
	"renart/internal/sqlcatalog"
	"renart/internal/sqlintelligence"
)

// ErrRenameTemplated reports that rename is unavailable because the document
// contains template calls: edits are computed against the rendered SQL and
// cannot be mapped back to the template source safely.
var ErrRenameTemplated = errors.New("rename is not available in SQL documents that use templating")

const (
	diagnosticSeverityError = 1
	diagnosticSeverityWarn  = 2
	completionKindText      = 1
	completionKindMethod    = 2
	completionKindField     = 5
	completionKindReference = 18
	symbolKindField         = 8
	symbolKindVariable      = 13
	symbolKindObject        = 19
)

var semanticTokenTypes = []string{"schema", "table", "column", "alias"}

func SemanticTokenTypes() []string {
	return append([]string(nil), semanticTokenTypes...)
}

const (
	semanticTokenSchema = 0
	semanticTokenTable  = 1
	semanticTokenColumn = 2
	semanticTokenAlias  = 3
)

var (
	wordPattern         = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_$]*`)
	relationPattern     = regexp.MustCompile(`(?i)\b(from|join|into|update|table|describe)\s+((?:"[^"]+"|'(?:''|[^'])+'|` + "`" + `[^` + "`" + `]+` + "`" + `|[A-Za-z_][\w$-]*)(?:\s*\.\s*(?:"[^"]+"|` + "`" + `[^` + "`" + `]+` + "`" + `|[A-Za-z_][\w$-]*))*)(?:\s+as\s+([A-Za-z_][\w$]*))?`)
	insertValuesPattern = regexp.MustCompile(`(?is)\binsert\s+into\s+((?:"[^"]+"|` + "`" + `[^` + "`" + `]+` + "`" + `|[A-Za-z_][\w$-]*)(?:\s*\.\s*(?:"[^"]+"|` + "`" + `[^` + "`" + `]+` + "`" + `|[A-Za-z_][\w$-]*))*)\s*(?:\(([^)]*)\))?\s*values\s*\(`)
	dotColumnPattern    = regexp.MustCompile(`([A-Za-z_][\w$]*)\s*\.\s*([A-Za-z_][\w$]*)`)
	refCallPattern      = regexp.MustCompile(`(?is)\{\{\s*(ref|source)\s*\(\s*['"]([^'"]*)`)
)

type scopeResolver interface {
	resolveRelation(name string) *RelationNode
	columnsForRelation(relationID string) []ColumnInfo
}

type Engine struct {
	graph                         CanonicalGraph
	index                         graphIndex
	poly                          *polyglot.Client
	disableDuckDBFilesystemAccess bool
}

type EngineOptions struct {
	DisableDuckDBFilesystemAccess bool
}

type graphIndex struct {
	assetsByName        map[string]AssetNode
	relationsByName     map[string]RelationNode
	assetByRelationID   map[string]AssetNode
	columnsByRelationID map[string][]ColumnInfo
}

func NewEngine(graph CanonicalGraph) *Engine {
	return NewEngineWithOptions(graph, EngineOptions{})
}

func NewEngineWithOptions(graph CanonicalGraph, options EngineOptions) *Engine {
	idx := graphIndex{
		assetsByName:        map[string]AssetNode{},
		relationsByName:     map[string]RelationNode{},
		assetByRelationID:   map[string]AssetNode{},
		columnsByRelationID: map[string][]ColumnInfo{},
	}
	assetsByID := map[string]AssetNode{}
	for _, asset := range graph.Assets {
		assetsByID[asset.ID] = asset
		idx.assetsByName[strings.ToLower(asset.Name)] = asset
		idx.assetsByName[strings.ToLower(path.Base(asset.Name))] = asset
	}
	for _, relation := range graph.Relations {
		key := strings.ToLower(relation.Name)
		if current, exists := idx.relationsByName[key]; !exists || (isRemoteCatalogRelation(current) && !isRemoteCatalogRelation(relation)) {
			idx.relationsByName[key] = relation
		}
		if asset, ok := assetsByID[relation.AssetID]; ok {
			idx.assetByRelationID[relation.ID] = asset
		}
	}
	shortCandidates := make(map[string][]RelationNode)
	for _, relation := range graph.Relations {
		key := strings.ToLower(shortName(relation.Name))
		duplicateAlias := false
		for _, current := range shortCandidates[key] {
			if relation.ID != "" && current.ID == relation.ID {
				duplicateAlias = true
				break
			}
		}
		if !duplicateAlias {
			shortCandidates[key] = append(shortCandidates[key], relation)
		}
	}
	for key, candidates := range shortCandidates {
		// A relation whose full name is already this key is the least ambiguous
		// interpretation. Otherwise a unique authored relation wins over live
		// catalog entries. Ambiguous remote short names require qualification.
		if _, exact := idx.relationsByName[key]; exact {
			continue
		}
		var authored []RelationNode
		for _, candidate := range candidates {
			if !isRemoteCatalogRelation(candidate) {
				authored = append(authored, candidate)
			}
		}
		switch {
		case len(authored) == 1:
			idx.relationsByName[key] = authored[0]
		case len(authored) == 0 && len(candidates) == 1:
			idx.relationsByName[key] = candidates[0]
		}
	}
	for _, schema := range graph.Schemas {
		idx.columnsByRelationID[schema.RelationID] = schema.Columns
	}
	return &Engine{
		graph:                         graph,
		index:                         idx,
		disableDuckDBFilesystemAccess: options.DisableDuckDBFilesystemAccess,
	}
}

func NewEngineWithPolyglot(graph CanonicalGraph, client *polyglot.Client) *Engine {
	return NewEngineWithPolyglotOptions(graph, client, EngineOptions{})
}

func NewEngineWithPolyglotOptions(graph CanonicalGraph, client *polyglot.Client, options EngineOptions) *Engine {
	engine := NewEngineWithOptions(graph, options)
	engine.poly = client
	return engine
}

func (e *Engine) Diagnostics(doc TextDocumentItem) []Diagnostic {
	return e.DiagnosticsContext(context.Background(), doc)
}

func (e *Engine) DiagnosticsContext(ctx context.Context, doc TextDocumentItem) []Diagnostic {
	projection := e.renderDocument(doc)
	analysis := analyzeSQLWithResolver(projection.doc.Text, e)
	diagnostics, semanticOK := e.semanticDiagnostics(ctx, projection.doc)
	if !semanticOK {
		diagnostics = e.polyglotDiagnostics(projection.doc)
	}
	duckDBFileRelations := e.duckDBFileRelationUses(projection.doc, analysis)
	diagnostics = withoutUnresolvedDuckDBFileRelations(projection.doc.Text, diagnostics, duckDBFileRelations)
	currentAsset := e.assetForURI(doc.URI)
	diagnostics = appendUniqueDiagnostics(diagnostics, e.crossConnectionDiagnostics(projection.doc.Text, analysis, currentAsset)...)
	for _, use := range analysis.relations {
		if _, isCTE := analysis.ctes[strings.ToLower(use.name)]; isCTE || strings.HasPrefix(use.name, "{{") {
			continue
		}
		if e.isDuckDBFileRelation(projection.doc, use.name) {
			if e.disableDuckDBFilesystemAccess {
				diagnostics = appendUniqueDiagnostics(diagnostics, Diagnostic{
					Range:    RangeFromOffsets(projection.doc.Text, use.start, use.end),
					Severity: diagnosticSeverityError,
					Code:     authoringdiag.CodeDuckDBFilesystemAccessDisabled,
					Source:   authoringdiag.SourceRenart,
					Message:  "DuckDB filesystem access is disabled; file-backed relation " + strconv.Quote(use.name) + " is unavailable.",
				})
			}
			continue
		}
		resolved := e.resolveRelation(use.name)
		if resolved == nil {
			diagnostics = appendUniqueDiagnostics(diagnostics, Diagnostic{
				Range:    RangeFromOffsets(projection.doc.Text, use.start, use.end),
				Severity: diagnosticSeverityError,
				Code:     authoringdiag.CodeUnresolvedRelation,
				Source:   authoringdiag.SourceRenart,
				Message:  "Unresolved relation: " + use.name,
			})
			continue
		}
		if currentAsset != nil && resolved.AssetID != "" && resolved.AssetID == currentAsset.ID {
			diagnostics = appendUniqueDiagnostics(diagnostics, Diagnostic{
				Range:    RangeFromOffsets(projection.doc.Text, use.start, use.end),
				Severity: diagnosticSeverityError,
				Code:     "circular-dependency",
				Source:   authoringdiag.SourceRenart,
				Message:  "Circular dependency: asset '" + currentAsset.Name + "' references itself.",
			})
		}
	}
	for _, col := range analysis.columns {
		ref := analysis.resolveAliasRef(col.qualifier)
		if ref == nil {
			diagnostics = appendUniqueDiagnostics(diagnostics, Diagnostic{
				Range:    RangeFromOffsets(projection.doc.Text, col.qualStart, col.qualEnd),
				Severity: diagnosticSeverityError,
				Code:     authoringdiag.CodeUnresolvedAlias,
				Source:   authoringdiag.SourceRenart,
				Message:  "Unresolved table or alias: " + col.qualifier,
			})
			continue
		}
		columns := e.columnsForAliasRef(*ref)
		if len(columns) == 0 {
			continue
		}
		if !hasColumn(columns, col.name) {
			diagnostics = appendUniqueDiagnostics(diagnostics, Diagnostic{
				Range:    RangeFromOffsets(projection.doc.Text, col.nameStart, col.nameEnd),
				Severity: diagnosticSeverityError,
				Code:     authoringdiag.CodeUnresolvedColumn,
				Source:   authoringdiag.SourceRenart,
				Message:  "Unresolved column: " + col.name,
			})
		}
	}
	if projection.changed {
		for i := range diagnostics {
			diagnostics[i].Range = projection.rendered.TemplateRangeForGenerated(doc.Text, diagnostics[i].Range)
			diagnostics[i].Confidence = string(authoringdiag.ConfidenceMedium)
		}
	}
	for i := range diagnostics {
		if diagnostics[i].Scope == "" {
			diagnostics[i].Scope = string(authoringdiag.ScopeDocument)
		}
		if diagnostics[i].Confidence == "" {
			diagnostics[i].Confidence = string(authoringdiag.ConfidenceHigh)
		}
	}
	diagnostics = appendUniqueAssetDiagnostics(diagnostics, e.assetDiagnostics(doc)...)
	return diagnostics
}

func (e *Engine) assetDiagnostics(doc TextDocumentItem) []Diagnostic {
	currentAsset := e.assetForURI(doc.URI)
	result := make([]Diagnostic, 0)
	for _, candidate := range e.graph.AssetDiagnostics {
		if candidate.URI != "" && candidate.URI != doc.URI {
			continue
		}
		if candidate.URI == "" && (currentAsset == nil || candidate.AssetID == "" || candidate.AssetID != currentAsset.ID) {
			continue
		}
		result = append(result, diagnosticFromAuthoring(doc.Text, candidate.Diagnostic))
	}
	return result
}

func (e *Engine) isDuckDBFileRelation(doc TextDocumentItem, relation string) bool {
	return strings.EqualFold(e.dialectForDocument(doc), "duckdb") && IsDuckDBLocalFileRelation(relation)
}

func (e *Engine) duckDBFileRelationUses(doc TextDocumentItem, analysis sqlAnalysis) []relationUse {
	if !strings.EqualFold(e.dialectForDocument(doc), "duckdb") {
		return nil
	}
	uses := make([]relationUse, 0)
	for _, use := range analysis.relations {
		if IsDuckDBLocalFileRelation(use.name) {
			uses = append(uses, use)
		}
	}
	return uses
}

func withoutUnresolvedDuckDBFileRelations(text string, diagnostics []Diagnostic, uses []relationUse) []Diagnostic {
	if len(uses) == 0 {
		return diagnostics
	}
	filtered := make([]Diagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != authoringdiag.CodeUnresolvedRelation {
			filtered = append(filtered, diagnostic)
			continue
		}
		start := ByteOffset(text, diagnostic.Range.Start)
		end := ByteOffset(text, diagnostic.Range.End)
		matchesFileRelation := false
		for _, use := range uses {
			if rangesOverlap(start, end, use.start, use.end) || strings.Contains(strings.ToLower(diagnostic.Message), strings.ToLower(use.name)) {
				matchesFileRelation = true
				break
			}
		}
		if !matchesFileRelation {
			filtered = append(filtered, diagnostic)
		}
	}
	return filtered
}

func rangesOverlap(leftStart, leftEnd, rightStart, rightEnd int) bool {
	return leftStart < rightEnd && rightStart < leftEnd
}

func appendUniqueAssetDiagnostics(existing []Diagnostic, candidates ...Diagnostic) []Diagnostic {
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

func (e *Engine) semanticDiagnostics(ctx context.Context, doc TextDocumentItem) ([]Diagnostic, bool) {
	schema, constraints, confidence := e.semanticValidationSchema()
	result, err := sqlintelligence.ValidateSQL(ctx, sqlintelligence.ValidationRequest{
		URI:                string(doc.URI),
		SQL:                doc.Text,
		Dialect:            e.dialectForDocument(doc),
		Schema:             schema,
		SchemaConstraints:  constraints,
		RelationConfidence: confidence,
		ExpectedOutput:     e.declaredOutputColumns(doc.URI),
	})
	if err != nil {
		return nil, false
	}
	diagnostics := make([]Diagnostic, 0, len(result.Diagnostics))
	for _, diagnostic := range result.Diagnostics {
		diagnostics = append(diagnostics, diagnosticFromAuthoring(doc.Text, diagnostic))
	}
	return diagnostics, true
}

func (e *Engine) semanticValidationSchema() (sqlintelligence.Schema, sqlintelligence.SchemaConstraints, map[string]sqlintelligence.RelationConfidence) {
	return ValidationSchemaWithConstraints(e.graph)
}

func (e *Engine) declaredOutputColumns(uri URI) []sqlintelligence.SchemaColumn {
	asset := e.assetForURI(uri)
	if asset == nil {
		return nil
	}
	relationIDs := map[string]struct{}{}
	for _, relation := range e.graph.Relations {
		if relation.AssetID == asset.ID {
			relationIDs[relation.ID] = struct{}{}
		}
	}
	var result []sqlintelligence.SchemaColumn
	for _, layer := range e.graph.Schemas {
		if !strings.EqualFold(layer.SourceKind, "declared") {
			continue
		}
		if _, ok := relationIDs[layer.RelationID]; !ok {
			continue
		}
		for _, column := range layer.Columns {
			result = append(result, sqlintelligence.SchemaColumn{Name: column.Name, Type: column.Type, Nullable: column.Nullable})
		}
	}
	return result
}

func diagnosticFromAuthoring(text string, diagnostic authoringdiag.Diagnostic) Diagnostic {
	severity := diagnosticSeverityWarn
	if diagnostic.Severity == authoringdiag.SeverityError {
		severity = diagnosticSeverityError
	}
	start, end := 0, min(1, len(text))
	if diagnostic.StartByte != nil {
		start = *diagnostic.StartByte
	}
	if diagnostic.EndByte != nil {
		end = *diagnostic.EndByte
	}
	return Diagnostic{
		Range:      RangeFromOffsets(text, start, end),
		Severity:   severity,
		Code:       diagnostic.Code,
		Source:     diagnostic.Source,
		Message:    diagnostic.Message,
		Scope:      string(diagnostic.Scope),
		Confidence: string(diagnostic.Confidence),
	}
}

func appendUniqueDiagnostics(existing []Diagnostic, candidates ...Diagnostic) []Diagnostic {
	for _, candidate := range candidates {
		duplicate := false
		for _, current := range existing {
			if current.Code == candidate.Code && current.Range == candidate.Range {
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

// CrossConnectionDiagnostics reports asset references that cannot be assumed
// to be queryable by the current asset's connection. It is public so pipeline
// type-checking can reuse the exact LSP diagnostic without also duplicating the
// LSP's parser/type diagnostics.
func (e *Engine) CrossConnectionDiagnostics(doc TextDocumentItem) []Diagnostic {
	projection := e.renderDocument(doc)
	analysis := analyzeSQLWithResolver(projection.doc.Text, e)
	diagnostics := e.crossConnectionDiagnostics(
		projection.doc.Text,
		analysis,
		e.assetForURI(doc.URI),
	)
	if projection.changed {
		for i := range diagnostics {
			diagnostics[i].Range = projection.rendered.TemplateRangeForGenerated(doc.Text, diagnostics[i].Range)
		}
	}
	return diagnostics
}

func (e *Engine) crossConnectionDiagnostics(sql string, analysis sqlAnalysis, currentAsset *AssetNode) []Diagnostic {
	if currentAsset == nil || strings.TrimSpace(currentAsset.Connection) == "" {
		return nil
	}
	var diagnostics []Diagnostic
	for _, use := range analysis.relations {
		if _, isCTE := analysis.ctes[strings.ToLower(use.name)]; isCTE || strings.HasPrefix(use.name, "{{") {
			continue
		}
		resolved := e.resolveRelation(use.name)
		if resolved == nil || resolved.AssetID == "" || resolved.AssetID == currentAsset.ID {
			continue
		}
		upstream, ok := e.index.assetByRelationID[resolved.ID]
		if !ok || strings.TrimSpace(upstream.Connection) == "" || strings.EqualFold(upstream.Connection, currentAsset.Connection) {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Range:      RangeFromOffsets(sql, use.start, use.end),
			Severity:   diagnosticSeverityWarn,
			Code:       authoringdiag.CodeCrossConnectionReference,
			Source:     authoringdiag.SourceRenart,
			Scope:      string(authoringdiag.ScopeDocument),
			Confidence: string(authoringdiag.ConfidenceHigh),
			Message: fmt.Sprintf(
				"Cross-connection reference: asset %q uses connection %q, while this query uses %q.",
				upstream.Name,
				upstream.Connection,
				currentAsset.Connection,
			),
		})
	}
	return diagnostics
}

func (e *Engine) CodeActions(doc TextDocumentItem) []CodeAction {
	diagnostics := e.Diagnostics(doc)
	analysis := analyzeSQLWithResolver(e.renderDocument(doc).doc.Text, e)
	var actions []CodeAction
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case "unresolved-relation":
			if action, ok := e.unresolvedRelationAction(doc, diagnostic); ok {
				actions = append(actions, action)
			}
		case "unresolved-alias":
			if action, ok := e.unresolvedAliasAction(doc, diagnostic, analysis); ok {
				actions = append(actions, action)
			}
		case "unresolved-column":
			if action, ok := e.unresolvedColumnAction(doc, diagnostic, analysis); ok {
				actions = append(actions, action)
			}
		}
	}
	return actions
}

func (e *Engine) SemanticTokens(doc TextDocumentItem) SemanticTokens {
	projection := e.renderDocument(doc)
	analysis := analyzeSQLWithResolver(projection.doc.Text, e)
	var raw []semanticToken
	for _, relation := range analysis.relations {
		raw = append(raw, relationSemanticTokens(projection.doc.Text, relation)...)
	}
	for _, ref := range analysis.aliases {
		if ref.start >= 0 && ref.end > ref.start {
			raw = append(raw, semanticToken{rng: byteRange{start: ref.start, end: ref.end}, tokenType: semanticTokenAlias})
		}
	}
	for _, ref := range analysis.ctes {
		if ref.start >= 0 && ref.end > ref.start {
			raw = append(raw, semanticToken{rng: byteRange{start: ref.start, end: ref.end}, tokenType: semanticTokenAlias})
		}
		for _, rng := range ref.columnRanges {
			raw = append(raw, semanticToken{rng: rng, tokenType: semanticTokenColumn})
		}
	}
	for _, column := range analysis.columns {
		raw = append(raw, semanticToken{rng: byteRange{start: column.nameStart, end: column.nameEnd}, tokenType: semanticTokenColumn})
	}
	raw = append(raw, e.unqualifiedColumnSemanticTokens(projection.doc.Text, analysis)...)
	if projection.changed {
		for i := range raw {
			mapped := projection.rendered.TemplateRangeForGenerated(doc.Text, RangeFromOffsets(projection.doc.Text, raw[i].rng.start, raw[i].rng.end))
			raw[i].rng = byteRange{start: ByteOffset(doc.Text, mapped.Start), end: ByteOffset(doc.Text, mapped.End)}
		}
		return SemanticTokens{Data: encodeSemanticTokens(doc.Text, raw)}
	}
	return SemanticTokens{Data: encodeSemanticTokens(doc.Text, raw)}
}

func (e *Engine) DocumentSymbols(doc TextDocumentItem) []DocumentSymbol {
	projection := e.renderDocument(doc)
	analysis := analyzeSQLWithResolver(projection.doc.Text, e)
	var symbols []DocumentSymbol
	for _, ref := range analysis.ctes {
		rng := RangeFromOffsets(projection.doc.Text, ref.start, ref.end)
		symbol := DocumentSymbol{
			Name:           ref.name,
			Detail:         "CTE",
			Kind:           symbolKindVariable,
			Range:          rng,
			SelectionRange: rng,
		}
		for _, column := range ref.columns {
			if colRange, ok := ref.columnRanges[strings.ToLower(column.Name)]; ok {
				childRange := RangeFromOffsets(projection.doc.Text, colRange.start, colRange.end)
				symbol.Children = append(symbol.Children, DocumentSymbol{
					Name:           column.Name,
					Detail:         column.Type,
					Kind:           symbolKindField,
					Range:          childRange,
					SelectionRange: childRange,
				})
			}
		}
		symbols = append(symbols, symbol)
	}
	seenRelations := map[string]struct{}{}
	for _, relation := range analysis.relations {
		key := strings.ToLower(relation.name)
		if _, ok := seenRelations[key]; ok {
			continue
		}
		seenRelations[key] = struct{}{}
		rng := RangeFromOffsets(projection.doc.Text, relation.start, relation.end)
		symbols = append(symbols, DocumentSymbol{
			Name:           relation.name,
			Detail:         "relation",
			Kind:           symbolKindObject,
			Range:          rng,
			SelectionRange: rng,
		})
	}
	if projection.changed {
		for i := range symbols {
			mapDocumentSymbolRanges(doc.Text, projection, &symbols[i])
		}
	}
	return symbols
}

func (e *Engine) WorkspaceSymbols(query string) []SymbolInformation {
	query = strings.ToLower(strings.TrimSpace(query))
	var symbols []SymbolInformation
	for _, asset := range e.graph.Assets {
		if query != "" && !strings.Contains(strings.ToLower(asset.Name), query) && !strings.Contains(strings.ToLower(asset.ID), query) {
			continue
		}
		symbols = append(symbols, SymbolInformation{
			Name: asset.Name,
			Kind: symbolKindObject,
			Location: Location{
				URI:     asset.URI,
				Range:   safeAssetRange(asset),
				AssetID: asset.ID,
			},
			ContainerName: asset.Kind,
		})
	}
	assetIDs := map[string]struct{}{}
	for _, asset := range e.graph.Assets {
		assetIDs[asset.ID] = struct{}{}
	}
	for _, relation := range e.graph.Relations {
		if _, backedByAsset := assetIDs[relation.AssetID]; backedByAsset {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(relation.Name), query) {
			continue
		}
		symbols = append(symbols, SymbolInformation{
			Name: relation.Name,
			Kind: symbolKindObject,
			Location: Location{
				URI:   e.graph.WorkspaceURI,
				Range: Range{},
			},
			ContainerName: "relation",
		})
	}
	sort.Slice(symbols, func(i, j int) bool {
		return strings.ToLower(symbols[i].Name) < strings.ToLower(symbols[j].Name)
	})
	return symbols
}

func (e *Engine) unresolvedRelationAction(doc TextDocumentItem, diagnostic Diagnostic) (CodeAction, bool) {
	unknown := textInRange(doc.Text, diagnostic.Range)
	var candidates []string
	for _, relation := range e.graph.Relations {
		if strings.Contains(unknown, ".") {
			candidates = append(candidates, relation.Name)
		} else {
			candidates = append(candidates, shortName(relation.Name))
		}
	}
	replacement, ok := nearestName(unknown, candidates)
	if !ok {
		return CodeAction{}, false
	}
	return replacementAction(doc.URI, diagnostic, unknown, replacement), true
}

func (e *Engine) unresolvedAliasAction(doc TextDocumentItem, diagnostic Diagnostic, analysis sqlAnalysis) (CodeAction, bool) {
	unknown := textInRange(doc.Text, diagnostic.Range)
	var candidates []string
	for alias := range analysis.aliases {
		candidates = append(candidates, alias)
	}
	for alias := range analysis.ctes {
		candidates = append(candidates, alias)
	}
	for _, relation := range e.graph.Relations {
		candidates = append(candidates, shortName(relation.Name), relation.Name)
	}
	replacement, ok := nearestName(unknown, candidates)
	if !ok {
		return CodeAction{}, false
	}
	return replacementAction(doc.URI, diagnostic, unknown, replacement), true
}

func (e *Engine) unresolvedColumnAction(doc TextDocumentItem, diagnostic Diagnostic, analysis sqlAnalysis) (CodeAction, bool) {
	unknown := textInRange(doc.Text, diagnostic.Range)
	start := ByteOffset(doc.Text, diagnostic.Range.Start)
	end := ByteOffset(doc.Text, diagnostic.Range.End)
	col := columnUseAt(analysis.columns, start, end)
	if col == nil {
		return CodeAction{}, false
	}
	ref := analysis.resolveAliasRef(col.qualifier)
	if ref == nil {
		return CodeAction{}, false
	}
	var candidates []string
	for _, column := range e.columnsForAliasRef(*ref) {
		candidates = append(candidates, column.Name)
	}
	replacement, ok := nearestName(unknown, candidates)
	if !ok {
		return CodeAction{}, false
	}
	return replacementAction(doc.URI, diagnostic, unknown, replacement), true
}

func replacementAction(uri URI, diagnostic Diagnostic, unknown, replacement string) CodeAction {
	return CodeAction{
		Title:       fmt.Sprintf("Change '%s' to '%s'", unknown, replacement),
		Kind:        "quickfix",
		Diagnostics: []Diagnostic{diagnostic},
		Edit: WorkspaceEdit{Changes: map[URI][]TextEdit{
			uri: []TextEdit{{Range: diagnostic.Range, NewText: replacement}},
		}},
		IsPreferred: true,
	}
}

func (e *Engine) polyglotDiagnostics(doc TextDocumentItem) []Diagnostic {
	if e.poly == nil {
		return nil
	}
	dialect := e.dialectForDocument(doc)
	result, err := e.poly.Validate(doc.Text, dialect)
	if err != nil {
		return nil
	}
	diagnostics := make([]Diagnostic, 0, len(result.Errors))
	for _, validationErr := range result.Errors {
		start, end := 0, 1
		if validationErr.Start != nil {
			start = *validationErr.Start
			end = start + 1
		}
		if validationErr.End != nil {
			end = *validationErr.End
		}
		if validationErr.Start == nil && validationErr.Line != nil && validationErr.Column != nil {
			start = ByteOffset(doc.Text, Position{Line: max(*validationErr.Line-1, 0), Character: max(*validationErr.Column-1, 0)})
			end = start + 1
		}
		diagnostics = append(diagnostics, Diagnostic{
			Range:    RangeFromOffsets(doc.Text, start, end),
			Severity: diagnosticSeverityError,
			Code:     validationErr.Code,
			Source:   "polyglot",
			Message:  validationErr.Message,
		})
	}
	return diagnostics
}

func (e *Engine) dialectForDocument(doc TextDocumentItem) string {
	for _, asset := range e.graph.Assets {
		if asset.URI == doc.URI && asset.Dialect != "" {
			return asset.Dialect
		}
	}
	return "generic"
}

func (e *Engine) assetForURI(uri URI) *AssetNode {
	for i := range e.graph.Assets {
		if e.graph.Assets[i].URI == uri {
			return &e.graph.Assets[i]
		}
	}
	return nil
}

func (e *Engine) Complete(doc TextDocumentItem, pos Position) []CompletionItem {
	offset := ByteOffset(doc.Text, pos)
	if inTemplateRef(doc.Text, offset) {
		return e.assetCompletions()
	}
	if offsetInSingleQuotedString(doc.Text, offset) {
		return nil
	}
	projection := e.renderDocument(doc)
	renderedOffset := offset
	if projection.changed {
		renderedOffset = projection.rendered.GeneratedOffsetForTemplateOffset(offset)
	}
	fullAnalysis := analyzeSQLWithResolver(projection.doc.Text, e)
	analysis := e.analysisForCurrentSelect(projection.doc.Text, renderedOffset, fullAnalysis)
	if qualifier, ok := qualifierBeforeDot(projection.doc.Text, renderedOffset); ok {
		// `from schema.` (or join/into/update) is a relation position, not an
		// alias.column one: offer the relations in that schema rather than the
		// columns of a table literally named `schema`.
		if relationDotPosition(projection.doc.Text, renderedOffset) {
			if items := e.relationCompletionsInSchema(qualifier); len(items) > 0 {
				return items
			}
		}
		return columnCompletions(e.columnsForQualifier(analysis, qualifier))
	}
	if relationPosition(projection.doc.Text, renderedOffset) {
		return e.relationCompletions()
	}
	if joinConditionPosition(projection.doc.Text, renderedOffset) {
		return joinQualifierCompletions(analysis)
	}
	if columnCompletionPosition(projection.doc.Text, renderedOffset) {
		return columnCompletions(e.columnsForSelectAnalysis(analysis))
	}
	return append(e.keywordCompletions(), e.relationCompletions()...)
}

// offsetInSingleQuotedString reports whether offset is inside a SQL string
// literal. Double quotes and backticks are identifiers in the dialects Renart
// supports, so they intentionally do not suppress identifier completion.
func offsetInSingleQuotedString(sql string, offset int) bool {
	offset = min(max(offset, 0), len(sql))
	inSingleQuote := false
	inIdentifierQuote := byte(0)
	lineComment := false
	blockComment := false
	for i := 0; i < offset; i++ {
		ch := sql[i]
		next := byte(0)
		if i+1 < offset {
			next = sql[i+1]
		}
		if lineComment {
			if ch == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if ch == '*' && next == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if inSingleQuote {
			if ch == '\'' {
				if next == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inIdentifierQuote != 0 {
			if ch == inIdentifierQuote {
				if next == inIdentifierQuote {
					i++
					continue
				}
				inIdentifierQuote = 0
			}
			continue
		}
		if ch == '-' && next == '-' {
			lineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			blockComment = true
			i++
			continue
		}
		switch ch {
		case '\'':
			inSingleQuote = true
		case '"', '`':
			inIdentifierQuote = ch
		}
	}
	return inSingleQuote
}

func (e *Engine) Definition(doc TextDocumentItem, pos Position) []Location {
	offset := ByteOffset(doc.Text, pos)
	if locations := e.templateReferenceDefinition(doc, offset); len(locations) > 0 {
		return locations
	}
	projection := e.renderDocument(doc)
	if projection.changed {
		offset = projection.rendered.GeneratedOffsetForTemplateOffset(offset)
		doc = projection.doc
		pos = PositionAt(doc.Text, offset)
	}
	word, start, end := wordAt(doc.Text, offset)
	if word == "" {
		return nil
	}
	analysis := analyzeSQLWithResolver(doc.Text, e)
	if col := columnUseAt(analysis.columns, start, end); col != nil {
		if start >= col.qualStart && end <= col.qualEnd {
			if ref := analysis.resolveAliasRef(col.qualifier); ref != nil && ref.start >= 0 && ref.end > ref.start {
				return []Location{{URI: doc.URI, Range: RangeFromOffsets(doc.Text, ref.start, ref.end)}}
			}
		}
		if ref := analysis.resolveAliasRef(col.qualifier); ref != nil {
			if rng, ok := ref.columnRanges[strings.ToLower(col.name)]; ok {
				return []Location{{URI: doc.URI, Range: RangeFromOffsets(doc.Text, rng.start, rng.end)}}
			}
		}
	}
	if rng, ok := e.unqualifiedColumnDefinition(doc.Text, offset, word, analysis); ok {
		return []Location{{URI: doc.URI, Range: RangeFromOffsets(doc.Text, rng.start, rng.end)}}
	}
	if relName := relationAt(analysis.relations, start, end); relName != "" {
		if ref := analysis.resolveAliasRef(relName); ref != nil && ref.kind == "cte" && ref.start >= 0 && ref.end > ref.start {
			return []Location{{URI: doc.URI, Range: RangeFromOffsets(doc.Text, ref.start, ref.end)}}
		}
		if relation := e.resolveRelation(relName); relation != nil {
			if asset, ok := e.index.assetByRelationID[relation.ID]; ok && asset.URI != "" {
				return []Location{{URI: asset.URI, Range: safeAssetRange(asset), AssetID: asset.ID}}
			}
		}
	}
	if ref := analysis.resolveAliasRef(word); ref != nil && ref.start >= 0 && ref.end > ref.start {
		return []Location{{URI: doc.URI, Range: RangeFromOffsets(doc.Text, ref.start, ref.end)}}
	}
	name := word
	if rel := relationAt(analysis.relations, start, end); rel != "" {
		name = rel
	}
	if relation := e.resolveRelation(name); relation != nil {
		if asset, ok := e.index.assetByRelationID[relation.ID]; ok && asset.URI != "" {
			return []Location{{URI: asset.URI, Range: safeAssetRange(asset), AssetID: asset.ID}}
		}
	}
	if asset, ok := e.index.assetsByName[strings.ToLower(word)]; ok && asset.URI != "" {
		return []Location{{URI: asset.URI, Range: safeAssetRange(asset), AssetID: asset.ID}}
	}
	return nil
}

func (e *Engine) References(doc TextDocumentItem, pos Position, includeDeclaration bool) []Location {
	offset := ByteOffset(doc.Text, pos)
	if target := e.templateReferenceTargetAt(doc.Text, offset); target != "" {
		return dedupeLocations(e.templateReferenceLocations(doc, target, includeDeclaration))
	}
	projection := e.renderDocument(doc)
	renderedDoc := doc
	renderedOffset := offset
	if projection.changed {
		renderedOffset = projection.rendered.GeneratedOffsetForTemplateOffset(offset)
		renderedDoc = projection.doc
	}
	word, start, end := wordAt(renderedDoc.Text, renderedOffset)
	if word == "" {
		return nil
	}
	analysis := analyzeSQLWithResolver(renderedDoc.Text, e)
	locations := e.localReferences(renderedDoc, start, end, word, analysis, includeDeclaration)
	if projection.changed {
		for i := range locations {
			locations[i].URI = doc.URI
			locations[i].Range = projection.rendered.TemplateRangeForGenerated(doc.Text, locations[i].Range)
		}
	}
	return dedupeLocations(locations)
}

func (e *Engine) WorkspaceReferences(doc TextDocumentItem, pos Position, docs []TextDocumentItem, includeDeclaration bool) []Location {
	offset := ByteOffset(doc.Text, pos)
	if target := e.templateReferenceTargetAt(doc.Text, offset); target != "" {
		if relation := e.resolveRelation(target); relation != nil {
			return e.referencesForRelationAcrossDocuments(*relation, docs, includeDeclaration)
		}
	}
	projection := e.renderDocument(doc)
	renderedDoc := doc
	renderedOffset := offset
	if projection.changed {
		renderedOffset = projection.rendered.GeneratedOffsetForTemplateOffset(offset)
		renderedDoc = projection.doc
	}
	word, start, end := wordAt(renderedDoc.Text, renderedOffset)
	if word == "" {
		return e.References(doc, pos, includeDeclaration)
	}
	analysis := analyzeSQLWithResolver(renderedDoc.Text, e)
	if relName := relationAt(analysis.relations, start, end); relName != "" {
		if ref := analysis.resolveAliasRef(relName); ref != nil && ref.kind == "cte" {
			return e.References(doc, pos, includeDeclaration)
		}
		if relation := e.resolveRelation(relName); relation != nil {
			return e.referencesForRelationAcrossDocuments(*relation, docs, includeDeclaration)
		}
	}
	if relation := e.resolveRelation(word); relation != nil {
		return e.referencesForRelationAcrossDocuments(*relation, docs, includeDeclaration)
	}
	return e.References(doc, pos, includeDeclaration)
}

func (e *Engine) Rename(doc TextDocumentItem, pos Position, newName string) (*WorkspaceEdit, error) {
	newName = strings.TrimSpace(newName)
	if !validRenameIdentifier(newName) {
		return nil, nil
	}
	offset := ByteOffset(doc.Text, pos)
	if e.templateReferenceTargetAt(doc.Text, offset) != "" {
		return nil, nil
	}
	projection := e.renderDocument(doc)
	if projection.changed {
		return nil, ErrRenameTemplated
	}
	word, start, end := wordAt(doc.Text, offset)
	if word == "" {
		return nil, nil
	}
	analysis := analyzeSQLWithResolver(doc.Text, e)
	locations := dedupeLocations(e.localRenameLocations(doc, start, end, word, analysis))
	if len(locations) == 0 {
		return nil, nil
	}
	edits := make([]TextEdit, 0, len(locations))
	for _, location := range locations {
		if location.URI != doc.URI || location.AssetID != "" {
			return nil, nil
		}
		edits = append(edits, TextEdit{Range: location.Range, NewText: newName})
	}
	return &WorkspaceEdit{Changes: map[URI][]TextEdit{doc.URI: edits}}, nil
}

func (e *Engine) localRenameLocations(doc TextDocumentItem, start, end int, word string, analysis sqlAnalysis) []Location {
	if col := columnUseAt(analysis.columns, start, end); col != nil {
		if start >= col.qualStart && end <= col.qualEnd {
			return explicitAliasReferences(doc, analysis, col.qualifier)
		}
		if ref := analysis.resolveAliasRef(col.qualifier); ref != nil {
			if _, ok := ref.columnRanges[strings.ToLower(col.name)]; ok {
				return columnReferences(doc, analysis, *ref, col.name, true)
			}
		}
		return nil
	}
	if relName := relationAt(analysis.relations, start, end); relName != "" {
		if ref := analysis.resolveAliasRef(relName); ref != nil && ref.kind == "cte" {
			return cteReferences(doc, analysis, *ref, true)
		}
		return nil
	}
	for _, ref := range analysis.ctes {
		if _, ok := columnRangeAt(ref.columnRanges, start, end); ok {
			return columnReferences(doc, analysis, ref, word, true)
		}
	}
	for _, ref := range analysis.aliases {
		if _, ok := columnRangeAt(ref.columnRanges, start, end); ok {
			return columnReferences(doc, analysis, ref, word, true)
		}
	}
	if ref := analysis.resolveAliasRef(word); ref != nil {
		if ref.kind == "cte" {
			return cteReferences(doc, analysis, *ref, true)
		}
		return explicitAliasReferences(doc, analysis, word)
	}
	return nil
}

func (e *Engine) localReferences(doc TextDocumentItem, start, end int, word string, analysis sqlAnalysis, includeDeclaration bool) []Location {
	if col := columnUseAt(analysis.columns, start, end); col != nil {
		if start >= col.qualStart && end <= col.qualEnd {
			return aliasReferences(doc, analysis, col.qualifier, includeDeclaration)
		}
		if ref := analysis.resolveAliasRef(col.qualifier); ref != nil {
			return columnReferences(doc, analysis, *ref, col.name, includeDeclaration)
		}
	}
	if relName := relationAt(analysis.relations, start, end); relName != "" {
		if ref := analysis.resolveAliasRef(relName); ref != nil && ref.kind == "cte" {
			return cteReferences(doc, analysis, *ref, includeDeclaration)
		}
		if relation := e.resolveRelation(relName); relation != nil {
			return relationReferences(doc, analysis, e, relation.ID)
		}
	}
	for _, ref := range analysis.ctes {
		if _, ok := columnRangeAt(ref.columnRanges, start, end); ok {
			return columnReferences(doc, analysis, ref, word, includeDeclaration)
		}
	}
	for _, ref := range analysis.aliases {
		if _, ok := columnRangeAt(ref.columnRanges, start, end); ok {
			return columnReferences(doc, analysis, ref, word, includeDeclaration)
		}
	}
	if ref := analysis.resolveAliasRef(word); ref != nil {
		if ref.kind == "cte" {
			return cteReferences(doc, analysis, *ref, includeDeclaration)
		}
		return aliasReferences(doc, analysis, word, includeDeclaration)
	}
	if relation := e.resolveRelation(word); relation != nil {
		return relationReferences(doc, analysis, e, relation.ID)
	}
	return nil
}

func (e *Engine) Hover(doc TextDocumentItem, pos Position) *Hover {
	if hover := e.diagnosticHover(doc, pos); hover != nil {
		return hover
	}
	offset := ByteOffset(doc.Text, pos)
	projection := e.renderDocument(doc)
	if projection.changed {
		offset = projection.rendered.GeneratedOffsetForTemplateOffset(offset)
		doc = projection.doc
		pos = PositionAt(doc.Text, offset)
	}
	word, start, end := wordAt(doc.Text, offset)
	if word == "" {
		return nil
	}
	analysis := analyzeSQLWithResolver(doc.Text, e)
	if relationName := relationAt(analysis.relations, start, end); relationName != "" {
		if relation := e.resolveRelation(analysis.resolveAliasOrName(relationName)); relation != nil {
			return &Hover{Contents: e.relationHover(*relation), Range: ptr(RangeFromOffsets(doc.Text, start, end))}
		}
	}
	if ref := analysis.resolveAliasRef(word); ref != nil {
		return &Hover{Contents: e.aliasHover(word, *ref), Range: ptr(RangeFromOffsets(doc.Text, start, end))}
	}
	for _, col := range analysis.columns {
		if start >= col.nameStart && end <= col.nameEnd {
			ref := analysis.resolveAliasRef(col.qualifier)
			if ref == nil {
				continue
			}
			for _, column := range e.columnsForAliasRef(*ref) {
				if strings.EqualFold(column.Name, col.name) {
					return &Hover{Contents: columnHover(ref.name, column), Range: ptr(RangeFromOffsets(doc.Text, col.nameStart, col.nameEnd))}
				}
			}
		}
	}
	if column, sourceName, ok := e.unqualifiedColumnAt(doc.Text, offset, word, analysis); ok {
		return &Hover{Contents: columnHover(sourceName, column), Range: ptr(RangeFromOffsets(doc.Text, start, end))}
	}
	return nil
}

func (e *Engine) SignatureHelp(doc TextDocumentItem, pos Position) *SignatureHelp {
	offset := ByteOffset(doc.Text, pos)
	projection := e.renderDocument(doc)
	if projection.changed {
		offset = projection.rendered.GeneratedOffsetForTemplateOffset(offset)
		doc = projection.doc
	}
	ctx, ok := insertValuesSignatureContext(doc.Text, offset)
	if !ok {
		return nil
	}
	relation := e.resolveRelation(ctx.relation)
	if relation == nil {
		return nil
	}
	columns := ctx.columns
	if len(columns) == 0 {
		columns = e.columnsForRelation(relation.ID)
	}
	if len(columns) == 0 {
		return nil
	}
	active := ctx.activeParameter
	if active >= len(columns) {
		active = len(columns) - 1
	}
	parameters := make([]SignatureParameter, 0, len(columns))
	labels := make([]string, 0, len(columns))
	for _, column := range columns {
		label := column.Name
		if column.Type != "" {
			label += " " + column.Type
		}
		parameters = append(parameters, SignatureParameter{Label: label, Documentation: column.Description})
		labels = append(labels, label)
	}
	signature := SignatureInformation{
		Label:           fmt.Sprintf("insert into %s (%s) values (...)", relation.Name, strings.Join(labels, ", ")),
		Parameters:      parameters,
		ActiveParameter: active,
	}
	return &SignatureHelp{
		Signatures:      []SignatureInformation{signature},
		ActiveSignature: 0,
		ActiveParameter: active,
	}
}

func (e *Engine) diagnosticHover(doc TextDocumentItem, pos Position) *Hover {
	for _, action := range e.CodeActions(doc) {
		if len(action.Diagnostics) == 0 {
			continue
		}
		diagnostic := action.Diagnostics[0]
		if !positionInRange(doc.Text, pos, diagnostic.Range) {
			continue
		}
		contents := fmt.Sprintf("**%s**", diagnostic.Message)
		if action.Title != "" {
			contents += "\n\n" + action.Title
		}
		return &Hover{Contents: contents, Range: &diagnostic.Range}
	}
	return nil
}

func (e *Engine) templateReferenceDefinition(doc TextDocumentItem, offset int) []Location {
	for _, match := range jinjaSQLCallPattern.FindAllStringSubmatchIndex(doc.Text, -1) {
		if offset < match[0] || offset > match[1] {
			continue
		}
		kind := strings.ToLower(doc.Text[match[2]:match[3]])
		args := quotedArgs(doc.Text[match[4]:match[5]])
		var target string
		switch kind {
		case "ref":
			if len(args) >= 1 {
				target = args[0]
			}
		case "source":
			if len(args) >= 2 {
				target = args[0] + "." + args[1]
			}
		}
		if target == "" {
			return nil
		}
		if relation := e.resolveRelation(target); relation != nil {
			if asset, ok := e.index.assetByRelationID[relation.ID]; ok && asset.URI != "" {
				return []Location{{URI: asset.URI, Range: safeAssetRange(asset), AssetID: asset.ID}}
			}
		}
	}
	return nil
}

func (e *Engine) templateReferenceTargetAt(sql string, offset int) string {
	for _, match := range jinjaSQLCallPattern.FindAllStringSubmatchIndex(sql, -1) {
		if offset < match[0] || offset > match[1] {
			continue
		}
		kind := strings.ToLower(sql[match[2]:match[3]])
		args := quotedArgs(sql[match[4]:match[5]])
		switch kind {
		case "ref":
			if len(args) >= 1 {
				return args[0]
			}
		case "source":
			if len(args) >= 2 {
				return args[0] + "." + args[1]
			}
		}
	}
	return ""
}

func (e *Engine) templateReferenceLocations(doc TextDocumentItem, target string, includeDeclaration bool) []Location {
	var locations []Location
	if includeDeclaration {
		if relation := e.resolveRelation(target); relation != nil {
			if asset, ok := e.index.assetByRelationID[relation.ID]; ok && asset.URI != "" {
				locations = append(locations, Location{URI: asset.URI, Range: safeAssetRange(asset), AssetID: asset.ID})
			}
		}
	}
	for _, match := range jinjaSQLCallPattern.FindAllStringSubmatchIndex(doc.Text, -1) {
		kind := strings.ToLower(doc.Text[match[2]:match[3]])
		args := quotedArgs(doc.Text[match[4]:match[5]])
		refTarget := ""
		switch kind {
		case "ref":
			if len(args) >= 1 {
				refTarget = args[0]
			}
		case "source":
			if len(args) >= 2 {
				refTarget = args[0] + "." + args[1]
			}
		}
		if strings.EqualFold(refTarget, target) {
			locations = append(locations, Location{URI: doc.URI, Range: RangeFromOffsets(doc.Text, match[0], match[1])})
		}
	}
	return locations
}

func cteReferences(doc TextDocumentItem, analysis sqlAnalysis, ref aliasRef, includeDeclaration bool) []Location {
	var locations []Location
	if includeDeclaration && ref.start >= 0 && ref.end > ref.start {
		locations = append(locations, Location{URI: doc.URI, Range: RangeFromOffsets(doc.Text, ref.start, ref.end)})
	}
	for _, relation := range analysis.relations {
		if strings.EqualFold(relation.name, ref.name) {
			locations = append(locations, Location{URI: doc.URI, Range: RangeFromOffsets(doc.Text, relation.start, relation.end)})
		}
	}
	return locations
}

func aliasReferences(doc TextDocumentItem, analysis sqlAnalysis, alias string, includeDeclaration bool) []Location {
	ref := analysis.resolveAliasRef(alias)
	if ref == nil {
		return nil
	}
	var locations []Location
	if includeDeclaration && ref.start >= 0 && ref.end > ref.start {
		locations = append(locations, Location{URI: doc.URI, Range: RangeFromOffsets(doc.Text, ref.start, ref.end)})
	}
	for _, column := range analysis.columns {
		if strings.EqualFold(column.qualifier, alias) {
			locations = append(locations, Location{URI: doc.URI, Range: RangeFromOffsets(doc.Text, column.qualStart, column.qualEnd)})
		}
	}
	return locations
}

func explicitAliasReferences(doc TextDocumentItem, analysis sqlAnalysis, alias string) []Location {
	ref := analysis.resolveAliasRef(alias)
	if ref == nil {
		return nil
	}
	for _, relation := range analysis.relations {
		if strings.EqualFold(relation.name, alias) && ref.start == relation.start && ref.end == relation.end {
			return nil
		}
	}
	return aliasReferences(doc, analysis, alias, true)
}

func columnReferences(doc TextDocumentItem, analysis sqlAnalysis, ref aliasRef, name string, includeDeclaration bool) []Location {
	var locations []Location
	normalized := strings.ToLower(name)
	if includeDeclaration {
		if rng, ok := ref.columnRanges[normalized]; ok {
			locations = append(locations, Location{URI: doc.URI, Range: RangeFromOffsets(doc.Text, rng.start, rng.end)})
		}
	}
	for _, column := range analysis.columns {
		if !strings.EqualFold(column.name, name) {
			continue
		}
		columnRef := analysis.resolveAliasRef(column.qualifier)
		if columnRef == nil || !sameAliasTarget(*columnRef, ref) {
			continue
		}
		locations = append(locations, Location{URI: doc.URI, Range: RangeFromOffsets(doc.Text, column.nameStart, column.nameEnd)})
	}
	return locations
}

// relationReferences lists in-document uses of a relation. The declaration (the
// asset that defines the relation) lives in another file, so it is handled by
// referencesForRelationAcrossDocuments rather than here.
func relationReferences(doc TextDocumentItem, analysis sqlAnalysis, resolver scopeResolver, relationID string) []Location {
	var locations []Location
	for _, use := range analysis.relations {
		relation := resolver.resolveRelation(use.name)
		if relation == nil || relation.ID != relationID {
			continue
		}
		locations = append(locations, Location{URI: doc.URI, Range: RangeFromOffsets(doc.Text, use.start, use.end)})
	}
	return locations
}

func (e *Engine) referencesForRelationAcrossDocuments(relation RelationNode, docs []TextDocumentItem, includeDeclaration bool) []Location {
	var locations []Location
	if includeDeclaration {
		if asset, ok := e.index.assetByRelationID[relation.ID]; ok && asset.URI != "" {
			locations = append(locations, Location{URI: asset.URI, Range: safeAssetRange(asset), AssetID: asset.ID})
		}
	}
	for _, doc := range docs {
		locations = append(locations, e.relationReferencesInDocument(doc, relation)...)
	}
	return dedupeLocations(locations)
}

func (e *Engine) relationReferencesInDocument(doc TextDocumentItem, target RelationNode) []Location {
	var locations []Location
	locations = append(locations, e.templateReferenceLocations(doc, target.Name, false)...)
	projection := e.renderDocument(doc)
	analysis := analyzeSQLWithResolver(projection.doc.Text, e)
	for _, use := range analysis.relations {
		relation := e.resolveRelation(use.name)
		if relation == nil || relation.ID != target.ID {
			continue
		}
		locationRange := RangeFromOffsets(projection.doc.Text, use.start, use.end)
		if projection.changed {
			locationRange = projection.rendered.TemplateRangeForGenerated(doc.Text, locationRange)
		}
		locations = append(locations, Location{URI: doc.URI, Range: locationRange})
	}
	return locations
}

func sameAliasTarget(left, right aliasRef) bool {
	return strings.EqualFold(left.kind, right.kind) && strings.EqualFold(left.name, right.name)
}

func columnRangeAt(ranges map[string]byteRange, start, end int) (byteRange, bool) {
	for _, rng := range ranges {
		if start >= rng.start && end <= rng.end {
			return rng, true
		}
	}
	return byteRange{}, false
}

func dedupeLocations(locations []Location) []Location {
	seen := map[string]struct{}{}
	result := make([]Location, 0, len(locations))
	for _, location := range locations {
		key := fmt.Sprintf("%s:%d:%d:%d:%d:%s", location.URI, location.Range.Start.Line, location.Range.Start.Character, location.Range.End.Line, location.Range.End.Character, location.AssetID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, location)
	}
	return result
}

func validRenameIdentifier(name string) bool {
	return wordPattern.MatchString(name) && wordPattern.FindString(name) == name
}

func textInRange(text string, rng Range) string {
	start := ByteOffset(text, rng.Start)
	end := ByteOffset(text, rng.End)
	if start < 0 || end < start || end > len(text) {
		return ""
	}
	return text[start:end]
}

func positionInRange(text string, pos Position, rng Range) bool {
	offset := ByteOffset(text, pos)
	start := ByteOffset(text, rng.Start)
	end := ByteOffset(text, rng.End)
	return offset >= start && offset <= end
}

type insertValuesContext struct {
	relation        string
	columns         []ColumnInfo
	activeParameter int
}

func insertValuesSignatureContext(sql string, offset int) (insertValuesContext, bool) {
	if offset < 0 || offset > len(sql) {
		return insertValuesContext{}, false
	}
	prefix := sql[:offset]
	matches := insertValuesPattern.FindAllStringSubmatchIndex(prefix, -1)
	if len(matches) == 0 {
		return insertValuesContext{}, false
	}
	match := matches[len(matches)-1]
	valuesStart := match[1]
	if valuesStart > len(prefix) {
		return insertValuesContext{}, false
	}
	relation := normalizeRelationIdentifier(prefix[match[2]:match[3]])
	if relation == "" {
		return insertValuesContext{}, false
	}
	var columns []ColumnInfo
	if match[4] >= 0 && match[5] >= match[4] {
		columns = parseInsertColumnList(prefix[match[4]:match[5]])
	}
	return insertValuesContext{
		relation:        relation,
		columns:         columns,
		activeParameter: activeValuesParameter(prefix[valuesStart:]),
	}, true
}

func parseInsertColumnList(list string) []ColumnInfo {
	var columns []ColumnInfo
	for _, part := range splitTopLevel(list, ',') {
		name := cleanIdentifier(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		columns = append(columns, ColumnInfo{Name: name})
	}
	return columns
}

func activeValuesParameter(valuePrefix string) int {
	depth := 0
	active := 0
	inQuote := byte(0)
	for i := 0; i < len(valuePrefix); i++ {
		ch := valuePrefix[i]
		if inQuote != 0 {
			if ch == inQuote {
				if i+1 < len(valuePrefix) && valuePrefix[i+1] == inQuote {
					i++
					continue
				}
				inQuote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			inQuote = ch
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return active
			}
			depth--
		case ',':
			if depth == 0 {
				active++
			}
		}
	}
	return active
}

func normalizeRelationIdentifier(value string) string {
	parts := strings.Split(value, ".")
	for i := range parts {
		parts[i] = cleanIdentifier(strings.TrimSpace(parts[i]))
	}
	var clean []string
	for _, part := range parts {
		if part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, ".")
}

func nearestName(name string, candidates []string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	seen := map[string]struct{}{}
	best := ""
	bestScore := 1 << 30
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		key := strings.ToLower(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		score := levenshtein(strings.ToLower(name), key)
		if score < bestScore {
			best = candidate
			bestScore = score
		}
	}
	if best == "" {
		return "", false
	}
	limit := max(2, len(name)/3)
	return best, bestScore <= limit
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			current[j] = min(min(prev[j]+1, current[j-1]+1), prev[j-1]+cost)
		}
		prev = current
	}
	return prev[len(b)]
}

func encodeSemanticTokens(text string, tokens []semanticToken) []uint32 {
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].rng.start == tokens[j].rng.start {
			return tokens[i].rng.end < tokens[j].rng.end
		}
		return tokens[i].rng.start < tokens[j].rng.start
	})
	var data []uint32
	lastLine := 0
	lastStart := 0
	for _, token := range tokens {
		if token.rng.end <= token.rng.start || token.rng.start < 0 || token.rng.end > len(text) {
			continue
		}
		start := PositionAt(text, token.rng.start)
		end := PositionAt(text, token.rng.end)
		if start.Line != end.Line {
			continue
		}
		deltaLine := start.Line - lastLine
		deltaStart := start.Character
		if deltaLine == 0 {
			deltaStart = start.Character - lastStart
		}
		if deltaStart < 0 {
			continue
		}
		data = append(data,
			uint32(deltaLine),
			uint32(deltaStart),
			uint32(max(end.Character-start.Character, 1)),
			uint32(token.tokenType),
			0,
		)
		lastLine = start.Line
		lastStart = start.Character
	}
	return data
}

func mapDocumentSymbolRanges(templateText string, projection renderProjection, symbol *DocumentSymbol) {
	symbol.Range = projection.rendered.TemplateRangeForGenerated(templateText, symbol.Range)
	symbol.SelectionRange = projection.rendered.TemplateRangeForGenerated(templateText, symbol.SelectionRange)
	for i := range symbol.Children {
		mapDocumentSymbolRanges(templateText, projection, &symbol.Children[i])
	}
}

func (e *Engine) columnsForQualifier(analysis sqlAnalysis, qualifier string) []ColumnInfo {
	if ref := analysis.resolveAliasRef(qualifier); ref != nil {
		return e.columnsForAliasRef(*ref)
	}
	if relation := e.resolveRelation(qualifier); relation != nil {
		return e.index.columnsByRelationID[relation.ID]
	}
	return nil
}

func (e *Engine) columnsForAliasRef(ref aliasRef) []ColumnInfo {
	if len(ref.columns) > 0 {
		return ref.columns
	}
	if relation := e.resolveRelation(ref.name); relation != nil {
		return e.index.columnsByRelationID[relation.ID]
	}
	return nil
}

func (e *Engine) assetCompletions() []CompletionItem {
	items := make([]CompletionItem, 0, len(e.graph.Assets))
	for _, asset := range e.graph.Assets {
		items = append(items, CompletionItem{Label: asset.Name, Kind: completionKindReference, Detail: "pipeline asset", InsertText: asset.Name})
	}
	sortCompletionItems(items)
	return items
}

func (e *Engine) relationCompletions() []CompletionItem {
	items := make([]CompletionItem, 0, len(e.graph.Relations))
	for _, relation := range e.graph.Relations {
		detail := "relation"
		sortPrefix := "0"
		if isRemoteCatalogRelation(relation) {
			detail = "remote warehouse relation"
			sortPrefix = "1"
		}
		items = append(items, CompletionItem{
			Label:      relation.Name,
			Kind:       completionKindReference,
			Detail:     detail,
			InsertText: relation.Name,
			SortText:   sortPrefix + strings.ToLower(relation.Name),
		})
	}
	sortCompletionItems(items)
	return items
}

// relationCompletionsInSchema returns the relations qualified by schema (e.g.
// `analytics.` -> analytics.customers, analytics.orders). InsertText is only the
// part after the schema dot so completing after an already-typed `schema.` does
// not duplicate the qualifier.
func (e *Engine) relationCompletionsInSchema(schema string) []CompletionItem {
	trimmed := strings.TrimSpace(schema)
	if trimmed == "" {
		return nil
	}
	prefix := strings.ToLower(trimmed) + "."
	items := make([]CompletionItem, 0)
	for _, relation := range e.graph.Relations {
		if !strings.HasPrefix(strings.ToLower(relation.Name), prefix) {
			continue
		}
		detail := "relation"
		sortPrefix := "0"
		if isRemoteCatalogRelation(relation) {
			detail = "remote warehouse relation"
			sortPrefix = "1"
		}
		items = append(items, CompletionItem{
			Label:      relation.Name,
			Kind:       completionKindReference,
			Detail:     detail,
			InsertText: relation.Name[len(prefix):],
			SortText:   sortPrefix + strings.ToLower(relation.Name),
		})
	}
	sortCompletionItems(items)
	return items
}

// relationDotPosition reports whether the cursor sits right after `<ident>.` in a
// relation position (from/join/into/update <ident>.), so a schema qualifier
// should complete to relations rather than a table's columns.
func relationDotPosition(text string, offset int) bool {
	prefix := strings.ToLower(text[:min(offset, len(text))])
	tokens := wordPattern.FindAllString(prefix, -1)
	if len(tokens) < 2 {
		return false
	}
	keyword := tokens[len(tokens)-2]
	return keyword == "from" || keyword == "join" || keyword == "into" || keyword == "update"
}

// sqlKeywordCompletionLabels are the clause and operator keywords offered in a
// general statement position. They sort after columns and relations (see the
// "z" SortText prefix) so schema-aware suggestions always win. Kept lowercase to
// match the project's SQL style; functions are intentionally excluded (they are
// served through hover/signature help, not keyword completion).
var sqlKeywordCompletionLabels = []string{
	"select", "distinct", "from", "where", "group by", "having", "order by",
	"limit", "offset", "as", "on", "using", "with",
	"join", "inner join", "left join", "right join", "full join", "cross join",
	"lateral", "union", "union all", "intersect", "except",
	"and", "or", "not", "in", "exists", "between", "like", "ilike",
	"is null", "is not null", "case", "when", "then", "else", "end",
	"asc", "desc", "nulls first", "nulls last",
	"over", "partition by", "qualify",
}

func (e *Engine) keywordCompletions() []CompletionItem {
	items := make([]CompletionItem, 0, len(sqlKeywordCompletionLabels))
	for i, label := range sqlKeywordCompletionLabels {
		items = append(items, CompletionItem{Label: label, Kind: completionKindMethod, SortText: fmt.Sprintf("z%02d", i)})
	}
	return items
}

func (e *Engine) resolveRelation(name string) *RelationNode {
	key := strings.ToLower(strings.TrimSpace(name))
	if relation, ok := e.index.relationsByName[key]; ok {
		return &relation
	}
	if asset, ok := e.index.assetsByName[key]; ok && len(asset.OutputRelations) > 0 {
		for _, relationID := range asset.OutputRelations {
			for _, relation := range e.graph.Relations {
				if relation.ID == relationID {
					return &relation
				}
			}
		}
	}
	return nil
}

func (e *Engine) columnsForRelation(relationID string) []ColumnInfo {
	return e.index.columnsByRelationID[relationID]
}

// SetRelationColumns replaces a relation's indexed columns in place so freshly
// inferred columns are visible to subsequent analyses on the same engine
// without a rebuild. It does not touch the graph's schema layers — callers
// that hand the graph on (rather than the engine) must update those
// themselves. Not safe to call concurrently with analyses on the same engine.
func (e *Engine) SetRelationColumns(relationID string, columns []ColumnInfo) {
	e.index.columnsByRelationID[relationID] = columns
}

func (e *Engine) InferOutputColumns(sql string) []ColumnInfo {
	analysis := analyzeSQLWithResolver(sql, e)
	return outputColumns(sql, analysis, e)
}

func (e *Engine) analysisForCurrentSelect(sql string, offset int, fullAnalysis sqlAnalysis) sqlAnalysis {
	segment, _ := currentSelectStatementAt(sql, offset)
	analysis := analyzeSQLWithResolver(segment, e)
	scopes := selectScopeRangesAt(sql, offset)
	visibleFrom := 0
	for index := 1; index < len(scopes); index++ {
		if isCTESelectScope(sql, scopes[index], scopes[:index]) {
			// A CTE body can read earlier CTEs, but aliases from the query that
			// consumes it are not an enclosing SQL scope.
			visibleFrom = index
		}
	}
	// Correlated subqueries inherit aliases from each real ancestor query.
	// Analyze those scopes separately because the full-document analysis masks
	// nested queries and intentionally does not retain their local aliases.
	for index := visibleFrom; index < len(scopes)-1; index++ {
		scope := scopes[index]
		scopeText := sql[scope.start:scope.end]
		localOffset := min(max(offset-scope.start, 0), len(scopeText))
		ancestor := analyzeSQLWithResolver(currentTopLevelSelectStatement(scopeText, localOffset), e)
		for key, ref := range ancestor.aliases {
			if _, shadowed := analysis.aliases[key]; !shadowed {
				analysis.aliases[key] = ref
			}
		}
	}
	for key, cte := range fullAnalysis.ctes {
		analysis.ctes[key] = cte
	}
	for key, ref := range analysis.aliases {
		if cte, ok := analysis.ctes[strings.ToLower(ref.name)]; ok {
			analysis.aliases[key] = cte
		}
	}
	return analysis
}

func (e *Engine) columnsForSelectAnalysis(analysis sqlAnalysis) []ColumnInfo {
	var columns []ColumnInfo
	for _, ref := range analysis.aliases {
		columns = mergeColumns(columns, e.columnsForAliasRef(ref))
	}
	return columns
}

func (e *Engine) unqualifiedColumnDefinition(sql string, offset int, name string, fullAnalysis sqlAnalysis) (byteRange, bool) {
	segment, segmentStart := currentSelectStatementAt(sql, offset)
	// Analyze the segment with its absolute start as the base offset so the
	// column ranges it produces are document-absolute. Passing baseOffset 0
	// (as analyzeSQLWithResolver does) would yield segment-relative ranges that
	// the caller then misreads as absolute for statements after a top-level
	// `;` or `UNION`.
	analysis := analyzeSQLWithParent(segment, e, nil, segmentStart)
	for key, cte := range fullAnalysis.ctes {
		analysis.ctes[key] = cte
	}
	for key, ref := range analysis.aliases {
		if cte, ok := analysis.ctes[strings.ToLower(ref.name)]; ok {
			analysis.aliases[key] = cte
		}
	}
	var found byteRange
	matches := 0
	for _, ref := range analysis.aliases {
		if rng, ok := ref.columnRanges[strings.ToLower(name)]; ok {
			found = rng
			matches++
		}
	}
	return found, matches == 1
}

func (e *Engine) unqualifiedColumnAt(sql string, offset int, name string, fullAnalysis sqlAnalysis) (ColumnInfo, string, bool) {
	segment, _ := currentSelectStatementAt(sql, offset)
	analysis := analyzeSQLWithResolver(segment, e)
	for key, cte := range fullAnalysis.ctes {
		analysis.ctes[key] = cte
	}
	for key, ref := range analysis.aliases {
		if cte, ok := analysis.ctes[strings.ToLower(ref.name)]; ok {
			analysis.aliases[key] = cte
		}
	}
	var matched ColumnInfo
	var matchedSource string
	matches := 0
	for _, ref := range analysis.aliases {
		for _, column := range e.columnsForAliasRef(ref) {
			if strings.EqualFold(column.Name, name) {
				matched = column
				matchedSource = ref.name
				matches++
			}
		}
	}
	return matched, matchedSource, matches == 1
}

func (e *Engine) aliasHover(alias string, ref aliasRef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**\n\nAlias for `%s`", alias, ref.name)
	if len(ref.columns) > 0 {
		b.WriteString("\n\n**Columns**")
		for i, column := range ref.columns {
			if i >= 8 {
				break
			}
			fmt.Fprintf(&b, "\n- `%s`", column.Name)
			if column.Type != "" {
				fmt.Fprintf(&b, ": %s", column.Type)
			}
		}
	}
	return b.String()
}

func (e *Engine) relationHover(relation RelationNode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**", relation.Name)
	if isRemoteCatalogRelation(relation) {
		connection := remoteCatalogConnection(relation)
		if connection == "" {
			b.WriteString("\n\nRemote warehouse catalog")
		} else {
			fmt.Fprintf(&b, "\n\nRemote warehouse catalog on `%s`", connection)
		}
	}
	columns := e.columnsForRelation(relation.ID)
	if len(columns) > 0 {
		b.WriteString("\n\n**Columns**")
		for _, column := range columns {
			fmt.Fprintf(&b, "\n- `%s`", column.Name)
			if column.Type != "" {
				fmt.Fprintf(&b, ": %s", column.Type)
			}
		}
	}
	return b.String()
}

func isRemoteCatalogRelation(relation RelationNode) bool {
	for _, provenance := range relation.Provenance {
		if strings.EqualFold(strings.TrimSpace(provenance.Provider), "remote_catalog") {
			return true
		}
	}
	return false
}

func remoteCatalogConnection(relation RelationNode) string {
	for _, provenance := range relation.Provenance {
		if strings.EqualFold(strings.TrimSpace(provenance.Provider), "remote_catalog") {
			return strings.TrimSpace(provenance.ProviderID)
		}
	}
	return ""
}

func columnHover(source string, column ColumnInfo) string {
	var b strings.Builder
	if source != "" {
		fmt.Fprintf(&b, "**%s.%s**", source, column.Name)
	} else {
		fmt.Fprintf(&b, "**%s**", column.Name)
	}
	if column.Type != "" {
		fmt.Fprintf(&b, "\n\nType: `%s`", column.Type)
	}
	if column.Description != "" {
		fmt.Fprintf(&b, "\n\n%s", column.Description)
	}
	return b.String()
}

type sqlAnalysis struct {
	relations    []relationUse
	columns      []columnUse
	aliases      map[string]aliasRef
	ctes         map[string]aliasRef
	localAliases map[string]struct{}
}

type aliasRef struct {
	alias        string
	name         string
	columns      []ColumnInfo
	columnRanges map[string]byteRange
	kind         string
	start, end   int
}

func (a sqlAnalysis) resolveAlias(name string) string {
	if ref := a.resolveAliasRef(name); ref != nil {
		return ref.name
	}
	return ""
}

func (a sqlAnalysis) resolveAliasOrName(name string) string {
	if resolved := a.resolveAlias(name); resolved != "" {
		return resolved
	}
	return name
}

func (a sqlAnalysis) resolveAliasRef(name string) *aliasRef {
	key := strings.ToLower(strings.TrimSpace(name))
	if ref, ok := a.aliases[key]; ok {
		return &ref
	}
	if ref, ok := a.ctes[key]; ok {
		return &ref
	}
	return nil
}

func (a sqlAnalysis) localAliasRefs() []aliasRef {
	refs := make([]aliasRef, 0, len(a.localAliases))
	for key := range a.localAliases {
		if ref, ok := a.aliases[key]; ok {
			refs = append(refs, ref)
		}
	}
	return refs
}

type relationUse struct {
	name       string
	start, end int
}

type columnUse struct {
	qualifier          string
	name               string
	qualStart, qualEnd int
	nameStart, nameEnd int
}

type semanticToken struct {
	rng       byteRange
	tokenType int
}

func relationSemanticTokens(sql string, relation relationUse) []semanticToken {
	// Derive the schema/table token spans from the original source slice, not
	// from relation.name: normalizeRelation strips spaces and surrounding
	// quotes, so its offsets no longer line up with source coordinates for
	// qualifiers written as `"schema"."table"` or `schema . table`.
	if relation.start < 0 || relation.end > len(sql) || relation.end <= relation.start {
		return []semanticToken{{rng: byteRange{start: relation.start, end: relation.end}, tokenType: semanticTokenTable}}
	}
	raw := sql[relation.start:relation.end]
	parts := splitQualifiedIdentifier(raw)
	if len(parts) <= 1 {
		return []semanticToken{{rng: byteRange{start: relation.start, end: relation.end}, tokenType: semanticTokenTable}}
	}
	tokens := make([]semanticToken, 0, len(parts))
	for index, part := range parts {
		tokenType := semanticTokenSchema
		if index == len(parts)-1 {
			tokenType = semanticTokenTable
		}
		tokens = append(tokens, semanticToken{
			rng:       byteRange{start: relation.start + part.start, end: relation.start + part.end},
			tokenType: tokenType,
		})
	}
	return tokens
}

// splitQualifiedIdentifier splits a raw relation reference on its top-level
// dots (ignoring dots inside quotes/backticks) and returns the byte range of
// each dot-separated segment, trimmed of surrounding whitespace, relative to
// raw.
func splitQualifiedIdentifier(raw string) []byteRange {
	var parts []byteRange
	inQuote := byte(0)
	segStart := 0
	flush := func(end int) {
		s, e := segStart, end
		for s < e && isSpace(raw[s]) {
			s++
		}
		for e > s && isSpace(raw[e-1]) {
			e--
		}
		if e <= s {
			s, e = segStart, end
		}
		parts = append(parts, byteRange{start: s, end: e})
	}
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
			continue
		}
		if ch == '.' {
			flush(i)
			segStart = i + 1
		}
	}
	flush(len(raw))
	return parts
}

func (e *Engine) unqualifiedColumnSemanticTokens(sql string, analysis sqlAnalysis) []semanticToken {
	existing := map[string]struct{}{}
	for _, column := range analysis.columns {
		existing[fmt.Sprintf("%d:%d", column.nameStart, column.nameEnd)] = struct{}{}
	}
	for _, relation := range analysis.relations {
		existing[fmt.Sprintf("%d:%d", relation.start, relation.end)] = struct{}{}
	}
	var tokens []semanticToken
	for _, match := range wordPattern.FindAllStringIndex(sql, -1) {
		start, end := match[0], match[1]
		key := fmt.Sprintf("%d:%d", start, end)
		if _, ok := existing[key]; ok {
			continue
		}
		if !offsetInSQLCode(sql, start) {
			continue
		}
		word := sql[start:end]
		if isKeyword(word) || unqualifiedTokenHasQualifier(sql, start, end) || rangeInsideRelationUse(analysis.relations, start, end) || tokenLooksLikeFunctionCall(sql, end) {
			continue
		}
		if _, _, ok := e.unqualifiedColumnAt(sql, start, word, analysis); !ok {
			continue
		}
		tokens = append(tokens, semanticToken{rng: byteRange{start: start, end: end}, tokenType: semanticTokenColumn})
	}
	return tokens
}

func unqualifiedTokenHasQualifier(sql string, start, end int) bool {
	before := start - 1
	for before >= 0 && (sql[before] == ' ' || sql[before] == '\t') {
		before--
	}
	if before >= 0 && sql[before] == '.' {
		return true
	}
	after := end
	for after < len(sql) && (sql[after] == ' ' || sql[after] == '\t') {
		after++
	}
	return after < len(sql) && sql[after] == '.'
}

func tokenLooksLikeFunctionCall(sql string, end int) bool {
	i := skipSpace(sql, end)
	return i < len(sql) && sql[i] == '('
}

func offsetInSQLCode(sql string, offset int) bool {
	inQuote := byte(0)
	lineComment := false
	blockComment := false
	for i := 0; i < len(sql) && i < offset; i++ {
		ch := sql[i]
		next := byte(0)
		if i+1 < len(sql) {
			next = sql[i+1]
		}
		if lineComment {
			if ch == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if ch == '*' && next == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if inQuote != 0 {
			if ch == inQuote {
				if i+1 < len(sql) && sql[i+1] == inQuote {
					i++
					continue
				}
				inQuote = 0
			}
			continue
		}
		if ch == '-' && next == '-' {
			lineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			blockComment = true
			i++
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
		}
	}
	return inQuote == 0 && !lineComment && !blockComment
}

func analyzeSQL(sql string) sqlAnalysis {
	return analyzeSQLWithResolver(sql, nil)
}

func analyzeSQLWithResolver(sql string, resolver scopeResolver) sqlAnalysis {
	return analyzeSQLWithParent(sql, resolver, nil, 0)
}

func analyzeSQLWithParent(sql string, resolver scopeResolver, parent *sqlAnalysis, baseOffset int) sqlAnalysis {
	sql = sqlForScopeAnalysis(sql)
	analysis := sqlAnalysis{aliases: map[string]aliasRef{}, ctes: map[string]aliasRef{}, localAliases: map[string]struct{}{}}
	if parent != nil {
		for key, ref := range parent.aliases {
			analysis.aliases[key] = ref
		}
		for key, ref := range parent.ctes {
			analysis.ctes[key] = ref
		}
	}
	for _, cte := range extractCTEDefs(sql) {
		absoluteBodyStart := baseOffset + cte.bodyStart
		bodyAnalysis := analyzeSQLWithParent(cte.body, resolver, &analysis, absoluteBodyStart)
		columns, columnRanges := outputColumnsWithRanges(cte.body, bodyAnalysis, resolver, absoluteBodyStart)
		analysis.ctes[strings.ToLower(cte.name)] = aliasRef{
			alias:        cte.name,
			name:         cte.name,
			columns:      columns,
			columnRanges: columnRanges,
			kind:         "cte",
			start:        baseOffset + cte.nameStart,
			end:          baseOffset + cte.nameEnd,
		}
	}
	scanSQL := maskNestedQueries(sql)
	for _, match := range relationPattern.FindAllStringSubmatchIndex(scanSQL, -1) {
		if !offsetInSQLCode(sql, match[4]) {
			continue
		}
		name := normalizeRelation(sql[match[4]:match[5]])
		if name == "" || isKeyword(name) {
			continue
		}
		if columns, ok := tableFunctionColumns(name, sql, match[5]); ok {
			alias := shortName(name)
			analysis.aliases[strings.ToLower(alias)] = aliasRef{
				alias:   alias,
				name:    name,
				columns: columns,
				kind:    "table-function",
				start:   baseOffset + match[4],
				end:     baseOffset + match[5],
			}
			analysis.localAliases[strings.ToLower(alias)] = struct{}{}
			continue
		}
		analysis.relations = append(analysis.relations, relationUse{name: name, start: baseOffset + match[4], end: baseOffset + match[5]})
		alias := shortName(name)
		aliasStart, aliasEnd := match[4], match[5]
		if len(match) >= 8 && match[6] >= 0 {
			candidate := sql[match[6]:match[7]]
			if !isKeyword(candidate) {
				alias = candidate
				aliasStart, aliasEnd = match[6], match[7]
			}
		} else if candidate, candidateStart, candidateEnd := implicitRelationAlias(sql, match[5]); candidate != "" {
			alias = candidate
			aliasStart, aliasEnd = candidateStart, candidateEnd
		}
		ref := aliasRef{alias: alias, name: name, kind: "relation", start: baseOffset + aliasStart, end: baseOffset + aliasEnd}
		if cte, ok := analysis.ctes[strings.ToLower(name)]; ok {
			ref = cte
			ref.alias = alias
			if aliasStart != match[4] || aliasEnd != match[5] {
				ref.start, ref.end = baseOffset+aliasStart, baseOffset+aliasEnd
			}
		} else if resolver != nil {
			if relation := resolver.resolveRelation(name); relation != nil {
				ref.columns = resolver.columnsForRelation(relation.ID)
			}
		}
		analysis.aliases[strings.ToLower(alias)] = ref
		analysis.localAliases[strings.ToLower(alias)] = struct{}{}
	}
	for _, subquery := range extractSubqueries(sql) {
		absoluteBodyStart := baseOffset + subquery.bodyStart
		bodyAnalysis := analyzeSQLWithParent(subquery.body, resolver, &analysis, absoluteBodyStart)
		analysis.relations = append(analysis.relations, bodyAnalysis.relations...)
		// Only surface column references the subquery's own scope could not
		// resolve. Aliases defined inside the subquery (e.g. `b` in
		// `(select b.x from base b) sub`) are invisible to the parent, so
		// re-checking those columns against the parent scope would raise a
		// bogus "unresolved-alias". Genuinely unresolved references still
		// bubble up because the child scope already inherits every ancestor
		// alias.
		for _, col := range bodyAnalysis.columns {
			if bodyAnalysis.resolveAliasRef(col.qualifier) != nil {
				continue
			}
			analysis.columns = append(analysis.columns, col)
		}
		if subquery.alias == "" {
			continue
		}
		columns, columnRanges := outputColumnsWithRanges(subquery.body, bodyAnalysis, resolver, absoluteBodyStart)
		if len(subquery.columnAliases) > 0 {
			aliasedColumns := make([]ColumnInfo, 0, len(subquery.columnAliases))
			columnRanges = make(map[string]byteRange, len(subquery.columnAliases))
			for index, columnAlias := range subquery.columnAliases {
				column := ColumnInfo{Name: columnAlias.name}
				if index < len(columns) {
					column = columns[index]
					column.Name = columnAlias.name
				}
				aliasedColumns = append(aliasedColumns, column)
				columnRanges[strings.ToLower(columnAlias.name)] = byteRange{
					start: baseOffset + columnAlias.start,
					end:   baseOffset + columnAlias.end,
				}
			}
			columns = aliasedColumns
		}
		analysis.aliases[strings.ToLower(subquery.alias)] = aliasRef{
			alias:        subquery.alias,
			name:         subquery.alias,
			columns:      columns,
			columnRanges: columnRanges,
			kind:         "subquery",
			start:        baseOffset + subquery.aliasStart,
			end:          baseOffset + subquery.aliasEnd,
		}
		analysis.localAliases[strings.ToLower(subquery.alias)] = struct{}{}
	}
	for _, match := range dotColumnPattern.FindAllStringSubmatchIndex(scanSQL, -1) {
		if !offsetInSQLCode(sql, match[2]) {
			continue
		}
		if rangeInsideRelationUse(analysis.relations, baseOffset+match[0], baseOffset+match[1]) {
			continue
		}
		analysis.columns = append(analysis.columns, columnUse{
			qualifier: sql[match[2]:match[3]],
			name:      sql[match[4]:match[5]],
			qualStart: baseOffset + match[2], qualEnd: baseOffset + match[3],
			nameStart: baseOffset + match[4], nameEnd: baseOffset + match[5],
		})
	}
	return analysis
}

// implicitRelationAlias reads an alias without letting the relation matcher
// consume the next clause keyword. Keeping that keyword outside the match is
// important: in `FROM first JOIN second`, consuming JOIN as a rejected alias
// prevents the scanner from discovering `second` at all.
func implicitRelationAlias(sql string, relationEnd int) (string, int, int) {
	start := skipSpace(sql, relationEnd)
	alias, end := readIdentifier(sql, start)
	if alias == "" || isKeyword(alias) {
		return "", 0, 0
	}
	return alias, start, end
}

func tableFunctionColumns(name, sql string, end int) ([]ColumnInfo, bool) {
	if !isTableFunctionCall(sql, end) {
		return nil, false
	}
	columns, known := sqlcatalog.DuckDBTableFunctionColumns(name)
	if !known {
		return nil, true
	}
	result := make([]ColumnInfo, 0, len(columns))
	for _, column := range columns {
		result = append(result, ColumnInfo{Name: column.Name, Type: column.Type})
	}
	return result, true
}

func isTableFunctionCall(sql string, end int) bool {
	i := skipSpace(sql, end)
	return i < len(sql) && sql[i] == '('
}

func sqlForScopeAnalysis(sql string) string {
	return replaceJinjaCallsPreservingLength(sql, func(call string) string {
		if match := dbtRefPattern.FindStringSubmatch(call); len(match) >= 2 {
			return match[1]
		}
		if match := dbtSourcePattern.FindStringSubmatch(call); len(match) >= 3 {
			return match[1] + "." + match[2]
		}
		return "__template__"
	})
}

func replaceJinjaCallsPreservingLength(sql string, replacement func(string) string) string {
	result := []byte(sql)
	pattern := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	for _, match := range pattern.FindAllStringIndex(sql, -1) {
		value := replacement(sql[match[0]:match[1]])
		if value == "" {
			value = "__template__"
		}
		width := match[1] - match[0]
		padded := value
		if len(padded) > width {
			padded = padded[:width]
		}
		for len(padded) < width {
			padded += " "
		}
		copy(result[match[0]:match[1]], padded)
	}
	return string(result)
}

type cteBody struct {
	name               string
	body               string
	nameStart, nameEnd int
	bodyStart          int
}

type subqueryBody struct {
	alias                string
	body                 string
	aliasStart, aliasEnd int
	bodyStart            int
	columnAliases        []subqueryColumnAlias
}

type subqueryColumnAlias struct {
	name       string
	start, end int
}

func extractCTEDefs(sql string) []cteBody {
	var result []cteBody
	i := skipSpace(sql, 0)
	if !hasWordAt(sql, i, "with") {
		return result
	}
	i += len("with")
	for i < len(sql) {
		i = skipSpace(sql, i)
		if hasWordAt(sql, i, "recursive") {
			i += len("recursive")
			continue
		}
		name, nameEnd := readIdentifier(sql, i)
		if name == "" {
			return result
		}
		nameStart := skipSpace(sql, i)
		i = skipSpace(sql, nameEnd)
		if i < len(sql) && sql[i] == '(' {
			close := findMatchingParen(sql, i)
			if close < 0 {
				return result
			}
			i = skipSpace(sql, close+1)
		}
		if !hasWordAt(sql, i, "as") {
			return result
		}
		i = skipSpace(sql, i+len("as"))
		if i >= len(sql) || sql[i] != '(' {
			return result
		}
		close := findMatchingParen(sql, i)
		if close < 0 {
			return result
		}
		result = append(result, cteBody{name: name, body: sql[i+1 : close], nameStart: nameStart, nameEnd: nameEnd, bodyStart: i + 1})
		i = skipSpace(sql, close+1)
		if i >= len(sql) || sql[i] != ',' {
			return result
		}
		i++
	}
	return result
}

func extractSubqueries(sql string) []subqueryBody {
	var result []subqueryBody
	for i := 0; i < len(sql); i++ {
		if sql[i] != '(' {
			continue
		}
		bodyStart := skipSpace(sql, i+1)
		if !isQueryBodyStart(sql, bodyStart) {
			continue
		}
		close := findMatchingParen(sql, i)
		if close < 0 {
			continue
		}
		aliasStart := skipSpace(sql, close+1)
		if hasWordAt(sql, aliasStart, "as") {
			aliasStart = skipSpace(sql, aliasStart+len("as"))
		}
		alias, aliasEnd := readIdentifier(sql, aliasStart)
		if alias != "" && isKeyword(alias) {
			alias = ""
		}
		var columnAliases []subqueryColumnAlias
		if alias != "" {
			columnAliases = readSubqueryColumnAliases(sql, aliasEnd)
		}
		result = append(result, subqueryBody{
			alias:         alias,
			body:          sql[i+1 : close],
			aliasStart:    aliasStart,
			aliasEnd:      aliasEnd,
			bodyStart:     i + 1,
			columnAliases: columnAliases,
		})
	}
	return result
}

func isQueryBodyStart(sql string, start int) bool {
	return hasWordAt(sql, start, "select") ||
		hasWordAt(sql, start, "with") ||
		hasWordAt(sql, start, "values") ||
		hasWordAt(sql, start, "describe")
}

func readSubqueryColumnAliases(sql string, aliasEnd int) []subqueryColumnAlias {
	open := skipSpace(sql, aliasEnd)
	if open >= len(sql) || sql[open] != '(' {
		return nil
	}
	close := findMatchingParen(sql, open)
	if close < 0 {
		return nil
	}
	list := sql[open+1 : close]
	aliases := make([]subqueryColumnAlias, 0)
	for _, itemRange := range splitTopLevelRanges(list, ',') {
		start := open + 1 + itemRange.start
		start = skipSpace(sql, start)
		name, end := readIdentifier(sql, start)
		if name == "" {
			continue
		}
		aliases = append(aliases, subqueryColumnAlias{name: name, start: start, end: end})
	}
	return aliases
}

func outputColumns(sql string, analysis sqlAnalysis, resolver scopeResolver) []ColumnInfo {
	columns, _ := outputColumnsWithRanges(sql, analysis, resolver, 0)
	return columns
}

func outputColumnsWithRanges(sql string, analysis sqlAnalysis, resolver scopeResolver, baseOffset int) ([]ColumnInfo, map[string]byteRange) {
	if columns, ok := describeOutputColumns(sql); ok {
		return columns, map[string]byteRange{}
	}
	selectBody, selectStart, ok := firstSelectListRange(sql)
	if !ok {
		return nil, nil
	}
	var columns []ColumnInfo
	columnRanges := map[string]byteRange{}
	for _, itemRange := range splitTopLevelRanges(selectBody, ',') {
		rawItem := selectBody[itemRange.start:itemRange.end]
		leading := len(rawItem) - len(strings.TrimLeft(rawItem, " \t\r\n"))
		item := strings.TrimSpace(rawItem)
		itemStart := baseOffset + selectStart + itemRange.start + leading
		if item == "" {
			continue
		}
		if item == "*" {
			for _, ref := range analysis.localAliasRefs() {
				columns = mergeColumns(columns, ref.columns)
				for name, rng := range ref.columnRanges {
					columnRanges[strings.ToLower(name)] = rng
				}
			}
			continue
		}
		if strings.HasSuffix(item, ".*") {
			qualifier := strings.TrimSpace(strings.TrimSuffix(item, ".*"))
			if ref := analysis.resolveAliasRef(qualifier); ref != nil {
				columns = mergeColumns(columns, ref.columns)
				for name, rng := range ref.columnRanges {
					columnRanges[strings.ToLower(name)] = rng
				}
			}
			continue
		}
		if alias, aliasStart, aliasEnd := selectAliasRange(item); alias != "" {
			columns = mergeColumns(columns, []ColumnInfo{{Name: alias}})
			columnRanges[strings.ToLower(alias)] = byteRange{start: itemStart + aliasStart, end: itemStart + aliasEnd}
			continue
		}
		name, nameStart, nameEnd := projectionNameRange(item)
		if name != "" {
			columns = mergeColumns(columns, []ColumnInfo{{Name: name}})
			columnRanges[strings.ToLower(name)] = byteRange{start: itemStart + nameStart, end: itemStart + nameEnd}
		}
	}
	return columns, columnRanges
}

func describeOutputColumns(sql string) ([]ColumnInfo, bool) {
	start := skipSpace(sql, 0)
	if !hasWordAt(sql, start, "describe") {
		return nil, false
	}
	names := []string{"column_name", "column_type", "null", "key", "default", "extra"}
	columns := make([]ColumnInfo, 0, len(names))
	for _, name := range names {
		columns = append(columns, ColumnInfo{Name: name, Type: "VARCHAR"})
	}
	return columns, true
}

func firstSelectListRange(sql string) (string, int, bool) {
	lower := strings.ToLower(sql)
	for i := 0; i < len(lower); i++ {
		if !hasWordAt(lower, i, "select") {
			continue
		}
		start := i + len("select")
		depth := 0
		inQuote := byte(0)
		for j := start; j < len(sql); j++ {
			ch := sql[j]
			if inQuote != 0 {
				if ch == inQuote {
					inQuote = 0
				}
				continue
			}
			if ch == '\'' || ch == '"' || ch == '`' {
				inQuote = ch
				continue
			}
			switch ch {
			case '(':
				depth++
			case ')':
				if depth > 0 {
					depth--
				}
			default:
				if depth == 0 && hasWordAt(lower, j, "from") {
					return sql[start:j], start, true
				}
			}
		}
		return sql[start:], start, true
	}
	return "", 0, false
}

func splitTopLevel(text string, sep byte) []string {
	ranges := splitTopLevelRanges(text, sep)
	var parts []string
	for _, rng := range ranges {
		parts = append(parts, text[rng.start:rng.end])
	}
	return parts
}

func splitTopLevelRanges(text string, sep byte) []byteRange {
	var parts []byteRange
	start := 0
	depth := 0
	inQuote := byte(0)
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if ch == sep && depth == 0 {
				parts = append(parts, byteRange{start: start, end: i})
				start = i + 1
			}
		}
	}
	parts = append(parts, byteRange{start: start, end: len(text)})
	return parts
}

func selectAliasRange(item string) (string, int, int) {
	if match := regexp.MustCompile(`(?i)\bas\s+([A-Za-z_][\w$]*)\s*$`).FindStringSubmatchIndex(item); len(match) >= 4 {
		return cleanIdentifier(item[match[2]:match[3]]), match[2], match[3]
	}
	fields := strings.Fields(item)
	if len(fields) >= 2 {
		matches := wordPattern.FindAllStringIndex(item, -1)
		if len(matches) > 0 {
			lastRange := matches[len(matches)-1]
			last := cleanIdentifier(item[lastRange[0]:lastRange[1]])
			if last != "" && !isKeyword(last) && !strings.ContainsAny(fields[len(fields)-2], "+-*/") {
				return last, lastRange[0], lastRange[1]
			}
		}
	}
	return "", 0, 0
}

func projectionNameRange(item string) (string, int, int) {
	item = strings.TrimSpace(item)
	if strings.ContainsAny(item, "()+-*/'\"") {
		return "", 0, 0
	}
	parts := strings.Split(item, ".")
	name := cleanIdentifier(parts[len(parts)-1])
	if name == "" {
		return "", 0, 0
	}
	start := strings.LastIndex(item, parts[len(parts)-1])
	return name, start, start + len(parts[len(parts)-1])
}

func cleanIdentifier(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`\"")
	value = strings.TrimRight(value, ",;")
	if wordPattern.MatchString(value) && wordPattern.FindString(value) == value {
		return value
	}
	return ""
}

func maskNestedQueries(sql string) string {
	result := []byte(sql)
	for _, subquery := range extractSubqueryRanges(sql) {
		for i := subquery.start; i < subquery.end && i < len(result); i++ {
			if result[i] != '\n' && result[i] != '\r' {
				result[i] = ' '
			}
		}
	}
	return string(result)
}

type byteRange struct {
	start int
	end   int
}

func extractSubqueryRanges(sql string) []byteRange {
	var ranges []byteRange
	for i := 0; i < len(sql); i++ {
		if sql[i] != '(' {
			continue
		}
		bodyStart := skipSpace(sql, i+1)
		if !isQueryBodyStart(sql, bodyStart) {
			continue
		}
		close := findMatchingParen(sql, i)
		if close < 0 {
			continue
		}
		ranges = append(ranges, byteRange{start: i + 1, end: close})
		i = close
	}
	return ranges
}

func findMatchingParen(sql string, open int) int {
	depth := 0
	inQuote := byte(0)
	for i := open; i < len(sql); i++ {
		ch := sql[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
			continue
		}
		if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func skipSpace(text string, i int) int {
	for i < len(text) && isSpace(text[i]) {
		i++
	}
	return i
}

func hasWordAt(text string, i int, word string) bool {
	if i < 0 || i+len(word) > len(text) || !strings.EqualFold(text[i:i+len(word)], word) {
		return false
	}
	beforeOK := i == 0 || !isIdentByte(text[i-1])
	after := i + len(word)
	afterOK := after >= len(text) || !isIdentByte(text[after])
	return beforeOK && afterOK
}

func readIdentifier(text string, i int) (string, int) {
	i = skipSpace(text, i)
	if i >= len(text) {
		return "", i
	}
	if text[i] == '"' || text[i] == '`' {
		quote := text[i]
		end := i + 1
		for end < len(text) && text[end] != quote {
			end++
		}
		if end < len(text) {
			return text[i+1 : end], end + 1
		}
		return "", i
	}
	start := i
	for i < len(text) && isIdentByte(text[i]) {
		i++
	}
	return text[start:i], i
}

func columnCompletions(columns []ColumnInfo) []CompletionItem {
	items := make([]CompletionItem, 0, len(columns))
	for _, column := range columns {
		items = append(items, CompletionItem{Label: column.Name, Kind: completionKindField, Detail: column.Type, InsertText: column.Name})
	}
	sortCompletionItems(items)
	return items
}

func joinQualifierCompletions(analysis sqlAnalysis) []CompletionItem {
	items := make([]CompletionItem, 0, len(analysis.aliases))
	seen := make(map[string]struct{}, len(analysis.aliases))
	for key, ref := range analysis.aliases {
		alias := strings.TrimSpace(ref.alias)
		if alias == "" {
			alias = key
		}
		normalized := strings.ToLower(alias)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		items = append(items, CompletionItem{
			Label:      alias + ".*",
			Kind:       completionKindField,
			Detail:     "columns from " + ref.name,
			InsertText: alias + ".",
		})
	}
	sortCompletionItems(items)
	return items
}

func qualifierBeforeDot(text string, offset int) (string, bool) {
	if offset > len(text) {
		offset = len(text)
	}
	i := offset - 1
	for i >= 0 && isSpace(text[i]) {
		i--
	}
	if i < 0 || text[i] != '.' {
		return "", false
	}
	i--
	for i >= 0 && isSpace(text[i]) {
		i--
	}
	end := i + 1
	for i >= 0 && isIdentByte(text[i]) {
		i--
	}
	if end <= i+1 {
		return "", false
	}
	return text[i+1 : end], true
}

func relationPosition(text string, offset int) bool {
	prefix := strings.ToLower(text[:min(offset, len(text))])
	tokens := wordPattern.FindAllString(prefix, -1)
	if len(tokens) == 0 {
		return false
	}
	last := tokens[len(tokens)-1]
	return last == "from" || last == "join" || last == "into" || last == "update"
}

func selectListPosition(text string, offset int) bool {
	segment := currentSelectSegmentAt(text, offset)
	localOffset := len(segment)
	selectIndex := lastTopLevelKeywordBefore(segment, "select", localOffset)
	if selectIndex < 0 {
		return false
	}
	fromIndex := lastTopLevelKeywordBefore(segment, "from", localOffset)
	return fromIndex < selectIndex
}

func columnCompletionPosition(text string, offset int) bool {
	if selectListPosition(text, offset) {
		return true
	}
	segment := currentSelectSegmentAt(text, offset)
	localOffset := len(segment)
	clause, ok := lastColumnClauseBefore(segment, localOffset)
	if !ok {
		return false
	}
	return clause == "where" ||
		clause == "having" ||
		clause == "group by" ||
		clause == "order by" ||
		clause == "qualify" ||
		clause == "set" ||
		clause == "values"
}

func joinConditionPosition(text string, offset int) bool {
	segment := currentSelectSegmentAt(text, offset)
	clause, ok := lastColumnClauseBefore(segment, len(segment))
	return ok && clause == "on"
}

func lastColumnClauseBefore(text string, offset int) (string, bool) {
	if offset > len(text) {
		offset = len(text)
	}
	lower := strings.ToLower(text)
	depth := 0
	inQuote := byte(0)
	clause := ""
	previousWord := ""
	for i := 0; i < offset; i++ {
		ch := text[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
			continue
		}
		switch ch {
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || !isWordStart(ch) {
			continue
		}
		word := wordPattern.FindString(lower[i:])
		if word == "" {
			continue
		}
		switch word {
		case "group", "order":
			previousWord = word
			i += len(word) - 1
			continue
		case "by":
			if previousWord == "group" {
				clause = "group by"
			} else if previousWord == "order" {
				clause = "order by"
			}
		case "select", "from", "join", "on", "where", "having", "qualify", "into", "update", "set", "values", "limit":
			clause = word
		}
		previousWord = word
		i += len(word) - 1
	}
	return clause, clause != ""
}

func isWordStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

// currentSelectSegmentAt and currentSelectStatementAt scope completion and
// column lookup to the innermost SELECT/CTE body containing the cursor. The
// older top-level helpers deliberately ignore parenthesized queries, which is
// correct for UNION splitting but left completion inside CTEs with no local
// FROM aliases or columns.
func currentSelectSegmentAt(text string, offset int) string {
	scope := currentSelectScopeRange(text, offset)
	localOffset := min(max(offset-scope.start, 0), scope.end-scope.start)
	return currentTopLevelSelectSegment(text[scope.start:scope.end], localOffset)
}

func currentSelectStatementAt(text string, offset int) (string, int) {
	scope := currentSelectScopeRange(text, offset)
	scopeText := text[scope.start:scope.end]
	localOffset := min(max(offset-scope.start, 0), len(scopeText))
	localStart := currentTopLevelSelectStart(scopeText, localOffset)
	return currentTopLevelSelectStatement(scopeText, localOffset), scope.start + localStart
}

func currentSelectScopeRange(text string, offset int) byteRange {
	scopes := selectScopeRangesAt(text, offset)
	return scopes[len(scopes)-1]
}

func selectScopeRangesAt(text string, offset int) []byteRange {
	offset = min(max(offset, 0), len(text))
	scopes := []byteRange{{start: 0, end: len(text)}}
	openParens := make([]int, 0, 4)
	inQuote := byte(0)
	lineComment := false
	blockComment := false
	for i := 0; i < offset; i++ {
		ch := text[i]
		next := byte(0)
		if i+1 < offset {
			next = text[i+1]
		}
		if lineComment {
			if ch == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if ch == '*' && next == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if inQuote != 0 {
			if ch == inQuote {
				if next == inQuote {
					i++
					continue
				}
				inQuote = 0
			}
			continue
		}
		if ch == '-' && next == '-' {
			lineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			blockComment = true
			i++
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
			continue
		}
		switch ch {
		case '(':
			openParens = append(openParens, i)
		case ')':
			if len(openParens) > 0 {
				openParens = openParens[:len(openParens)-1]
			}
		}
	}

	for _, open := range openParens {
		bodyStart := skipSpace(text, open+1)
		if !isQueryBodyStart(text, bodyStart) {
			continue
		}
		end := findMatchingParen(text, open)
		if end < 0 {
			end = len(text)
		}
		scopes = append(scopes, byteRange{start: open + 1, end: end})
	}
	return scopes
}

func isCTESelectScope(text string, target byteRange, parents []byteRange) bool {
	for _, parent := range parents {
		if parent.start >= target.start || parent.end < target.end {
			continue
		}
		for _, cte := range extractCTEDefs(text[parent.start:parent.end]) {
			start := parent.start + cte.bodyStart
			if start == target.start && start+len(cte.body) == target.end {
				return true
			}
		}
	}
	return false
}

func currentTopLevelSelectSegment(text string, offset int) string {
	if offset > len(text) {
		offset = len(text)
	}
	start := 0
	lower := strings.ToLower(text)
	depth := 0
	inQuote := byte(0)
	for i := 0; i < offset; i++ {
		ch := text[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				start = i + 1
			}
		default:
			if depth == 0 && hasWordAt(lower, i, "union") {
				start = i + len("union")
				if j := skipSpace(text, start); hasWordAt(lower, j, "all") || hasWordAt(lower, j, "distinct") {
					start = j + len(wordPattern.FindString(lower[j:]))
				}
			}
		}
	}
	return text[start:offset]
}

func currentTopLevelSelectStatement(text string, offset int) string {
	if offset > len(text) {
		offset = len(text)
	}
	start := currentTopLevelSelectStart(text, offset)
	end := len(text)
	lower := strings.ToLower(text)
	depth := 0
	inQuote := byte(0)
	for i := offset; i < len(text); i++ {
		ch := text[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				end = i
				return text[start:end]
			}
		default:
			if depth == 0 && hasWordAt(lower, i, "union") {
				end = i
				return text[start:end]
			}
		}
	}
	return text[start:end]
}

func currentTopLevelSelectStart(text string, offset int) int {
	if offset > len(text) {
		offset = len(text)
	}
	start := 0
	lower := strings.ToLower(text)
	depth := 0
	inQuote := byte(0)
	for i := 0; i < offset; i++ {
		ch := text[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				start = i + 1
			}
		default:
			if depth == 0 && hasWordAt(lower, i, "union") {
				start = i + len("union")
				if j := skipSpace(text, start); hasWordAt(lower, j, "all") || hasWordAt(lower, j, "distinct") {
					start = j + len(wordPattern.FindString(lower[j:]))
				}
			}
		}
	}
	return start
}

func lastTopLevelKeywordBefore(text, keyword string, offset int) int {
	if offset > len(text) {
		offset = len(text)
	}
	lower := strings.ToLower(text)
	depth := 0
	inQuote := byte(0)
	last := -1
	for i := 0; i < offset; i++ {
		ch := text[i]
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' || ch == '`' {
			inQuote = ch
			continue
		}
		switch ch {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && hasWordAt(lower, i, keyword) {
				last = i
			}
		}
	}
	return last
}

func rangeInsideRelationUse(relations []relationUse, start, end int) bool {
	for _, relation := range relations {
		if start >= relation.start && end <= relation.end {
			return true
		}
	}
	return false
}

func inTemplateRef(text string, offset int) bool {
	for _, match := range refCallPattern.FindAllStringSubmatchIndex(text[:min(offset, len(text))], -1) {
		if match[0] >= 0 && offset >= match[0] {
			if close := strings.Index(text[match[0]:min(offset, len(text))], "}}"); close < 0 {
				return true
			}
		}
	}
	return false
}

func wordAt(text string, offset int) (string, int, int) {
	if offset > len(text) {
		offset = len(text)
	}
	start := offset
	for start > 0 && isIdentByte(text[start-1]) {
		start--
	}
	end := offset
	for end < len(text) && isIdentByte(text[end]) {
		end++
	}
	if start == end {
		return "", start, end
	}
	return text[start:end], start, end
}

func relationAt(relations []relationUse, start, end int) string {
	for _, relation := range relations {
		if start >= relation.start && end <= relation.end {
			return relation.name
		}
	}
	return ""
}

func columnUseAt(columns []columnUse, start, end int) *columnUse {
	for i := range columns {
		column := &columns[i]
		if start >= column.qualStart && end <= column.nameEnd {
			return column
		}
	}
	return nil
}

func hasColumn(columns []ColumnInfo, name string) bool {
	for _, column := range columns {
		if strings.EqualFold(column.Name, name) {
			return true
		}
	}
	return false
}

func sortCompletionItems(items []CompletionItem) {
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label) })
	for i := range items {
		if items[i].SortText == "" {
			items[i].SortText = fmt.Sprintf("%04d", i)
		}
	}
}

func normalizeRelation(value string) string {
	value = strings.ReplaceAll(value, " ", "")
	value = strings.Trim(value, "`\"'")
	value = strings.ReplaceAll(value, "''", "'")
	return value
}

func shortName(value string) string {
	parts := strings.Split(value, ".")
	return strings.Trim(parts[len(parts)-1], "`\"'")
}

func isKeyword(value string) bool {
	switch strings.ToLower(value) {
	case "on", "where", "join", "left", "right", "inner", "outer", "full", "cross", "group", "order", "limit", "select", "from", "as":
		return true
	default:
		return false
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isIdentByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '$'
}

func safeAssetRange(asset AssetNode) Range {
	if asset.Range != nil {
		return *asset.Range
	}
	return Range{}
}

func ptr[T any](v T) *T { return &v }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
