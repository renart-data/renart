package sqllsp

import "renart/internal/authoringdiag"

type URI string

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI     URI    `json:"uri"`
	Range   Range  `json:"range"`
	AssetID string `json:"asset_id,omitempty"`
}

type Diagnostic struct {
	Range      Range  `json:"range"`
	Severity   int    `json:"severity"`
	Code       string `json:"code,omitempty"`
	Source     string `json:"source,omitempty"`
	Message    string `json:"message"`
	Scope      string `json:"scope,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type CompletionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	InsertText    string `json:"insertText,omitempty"`
	SortText      string `json:"sortText,omitempty"`
}

type Hover struct {
	Contents string `json:"contents"`
	Range    *Range `json:"range,omitempty"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type WorkspaceEdit struct {
	Changes map[URI][]TextEdit `json:"changes,omitempty"`
}

type CodeAction struct {
	Title       string            `json:"title"`
	Kind        string            `json:"kind,omitempty"`
	Diagnostics []Diagnostic      `json:"diagnostics,omitempty"`
	Edit        WorkspaceEdit     `json:"edit,omitempty"`
	IsPreferred bool              `json:"isPreferred,omitempty"`
	Action      *CodeActionAction `json:"action,omitempty"`
}

// CodeActionAction describes a reviewed Renart workflow that cannot be
// represented as an in-document workspace edit. LSP clients that do not know
// the action can safely ignore it.
type CodeActionAction struct {
	Type       string `json:"type"`
	RelationID string `json:"relation_id,omitempty"`
}

type SemanticTokens struct {
	Data []uint32 `json:"data"`
}

type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

type FormattingOptions struct {
	TabSize      int  `json:"tabSize,omitempty"`
	InsertSpaces bool `json:"insertSpaces,omitempty"`
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature,omitempty"`
	ActiveParameter int                    `json:"activeParameter,omitempty"`
}

type SignatureInformation struct {
	Label           string               `json:"label"`
	Documentation   string               `json:"documentation,omitempty"`
	Parameters      []SignatureParameter `json:"parameters,omitempty"`
	ActiveParameter int                  `json:"activeParameter,omitempty"`
}

type SignatureParameter struct {
	Label         string `json:"label"`
	Documentation string `json:"documentation,omitempty"`
}

type TextDocumentItem struct {
	URI        URI    `json:"uri"`
	LanguageID string `json:"languageId,omitempty"`
	Version    int    `json:"version,omitempty"`
	Text       string `json:"text"`
	// Projection is an optional, request-local rendering of Text. It is never
	// serialized over LSP; HTTP integrations that can render the document with
	// the pipeline's real Jinja context attach it before invoking the engine.
	Projection *RenderedSQL `json:"-"`
}

type CanonicalGraph struct {
	Version      int                `json:"version"`
	WorkspaceURI URI                `json:"workspace_uri,omitempty"`
	Assets       []AssetNode        `json:"assets"`
	Relations    []RelationNode     `json:"relations"`
	Schemas      []SchemaLayer      `json:"schemas"`
	Lineage      []LineageEdge      `json:"lineage,omitempty"`
	Renderings   []RenderedSQL      `json:"rendered_sql,omitempty"`
	References   []LogicalReference `json:"references,omitempty"`
	// AssetDiagnostics are provider-specific authoring findings computed when
	// the saved workspace graph is loaded. They are intentionally not part of
	// the provider-neutral serialized graph contract.
	AssetDiagnostics []AssetDiagnostic `json:"-"`
}

type AssetDiagnostic struct {
	AssetID    string
	URI        URI
	Diagnostic authoringdiag.Diagnostic
}

type AssetNode struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Kind            string       `json:"kind,omitempty"`
	Dialect         string       `json:"dialect,omitempty"`
	Connection      string       `json:"connection,omitempty"`
	URI             URI          `json:"uri,omitempty"`
	Range           *Range       `json:"range,omitempty"`
	OutputRelations []string     `json:"output_relations,omitempty"`
	InputRelations  []string     `json:"input_relations,omitempty"`
	Provenance      []Provenance `json:"provenance,omitempty"`
}

type RelationNode struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	AssetID    string       `json:"asset_id,omitempty"`
	Provenance []Provenance `json:"provenance,omitempty"`
}

type SchemaLayer struct {
	RelationID   string       `json:"relation_id"`
	SourceKind   string       `json:"source_kind,omitempty"`
	Completeness string       `json:"completeness,omitempty"`
	Confidence   string       `json:"confidence,omitempty"`
	Columns      []ColumnInfo `json:"columns"`
	Provenance   []Provenance `json:"provenance,omitempty"`
}

type ColumnInfo struct {
	Name        string           `json:"name"`
	Type        string           `json:"type,omitempty"`
	Description string           `json:"description,omitempty"`
	Nullable    *bool            `json:"nullable,omitempty"`
	PrimaryKey  bool             `json:"primary_key,omitempty"`
	ForeignKey  *ColumnReference `json:"foreign_key,omitempty"`
}

type ColumnReference struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

type LineageEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind,omitempty"`
}

type RenderedSQL struct {
	ID          string         `json:"id"`
	AssetID     string         `json:"asset_id,omitempty"`
	Dialect     string         `json:"dialect,omitempty"`
	TemplateURI URI            `json:"template_uri"`
	RenderedSQL string         `json:"rendered_sql"`
	SourceMap   SQLSourceMap   `json:"source_map"`
	Invocations []TemplateCall `json:"template_invocations,omitempty"`
	Provenance  []Provenance   `json:"provenance,omitempty"`
}

type SQLSourceMap struct {
	TemplateURI URI             `json:"template_uri"`
	Segments    []SourceSegment `json:"segments"`
}

type SourceSegment struct {
	GeneratedStart int    `json:"generated_start"`
	GeneratedEnd   int    `json:"generated_end"`
	TemplateStart  *int   `json:"template_start,omitempty"`
	TemplateEnd    *int   `json:"template_end,omitempty"`
	Kind           string `json:"kind"`
	Confidence     string `json:"confidence,omitempty"`
}

type TemplateCall struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Range Range  `json:"range"`
}

type LogicalReference struct {
	ID             string       `json:"id"`
	Kind           string       `json:"kind"`
	SourceAssetID  string       `json:"source_asset_id,omitempty"`
	TargetAssetID  string       `json:"target_asset_id,omitempty"`
	TargetRelation string       `json:"target_relation_id,omitempty"`
	TemplateRange  *Range       `json:"template_range,omitempty"`
	RenderedText   string       `json:"rendered_text,omitempty"`
	OriginalSyntax string       `json:"original_syntax,omitempty"`
	Provenance     []Provenance `json:"provenance,omitempty"`
}

type Provenance struct {
	Provider   string `json:"provider"`
	ProviderID string `json:"provider_id,omitempty"`
	URI        URI    `json:"uri,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}

type Request struct {
	TextDocument TextDocumentItem `json:"text_document"`
	Position     Position         `json:"position,omitempty"`
	Graph        *CanonicalGraph  `json:"graph,omitempty"`
}

type Response struct {
	Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
	Completions []CompletionItem    `json:"completions,omitempty"`
	Locations   []Location          `json:"locations,omitempty"`
	Hover       *Hover              `json:"hover,omitempty"`
	Edit        *WorkspaceEdit      `json:"edit,omitempty"`
	CodeActions []CodeAction        `json:"code_actions,omitempty"`
	Tokens      *SemanticTokens     `json:"tokens,omitempty"`
	Symbols     []DocumentSymbol    `json:"symbols,omitempty"`
	SymbolInfos []SymbolInformation `json:"symbol_infos,omitempty"`
	Signature   *SignatureHelp      `json:"signature,omitempty"`
}
