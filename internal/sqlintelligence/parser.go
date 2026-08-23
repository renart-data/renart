package sqlintelligence

import "context"

type Schema map[string]map[string]string

// SchemaConstraints carries authoritative column metadata that cannot be
// represented by Schema's compact name/type map. Callers should only populate
// fields that were explicitly declared by the project; omitted values stay
// unknown instead of being guessed from names or types.
type SchemaConstraints map[string]SchemaTableConstraints

type SchemaTableConstraints struct {
	Columns map[string]SchemaColumnConstraints
}

type SchemaColumnConstraints struct {
	Nullable   *bool
	PrimaryKey bool
	ForeignKey *SchemaColumnReference
}

type SchemaColumnReference struct {
	Table  string
	Column string
}

type SchemaColumnSourceMethods map[string]map[string][]string

type ParseContextRange struct {
	Start   int `json:"start"`
	End     int `json:"end"`
	Line    int `json:"line"`
	Col     int `json:"col"`
	EndLine int `json:"end_line"`
	EndCol  int `json:"end_col"`
}

type ParseContextPart struct {
	Name  string            `json:"name"`
	Kind  string            `json:"kind"`
	Range ParseContextRange `json:"range"`
}

type ParseContextTable struct {
	Name         string                       `json:"name"`
	SourceKind   string                       `json:"source_kind,omitempty"`
	ResolvedName string                       `json:"resolved_name,omitempty"`
	Alias        string                       `json:"alias"`
	Columns      []SchemaColumn               `json:"columns,omitempty"`
	ColumnRanges map[string]ParseContextRange `json:"column_ranges,omitempty"`
	Parts        []ParseContextPart           `json:"parts"`
	AliasRange   *ParseContextRange           `json:"alias_range,omitempty"`
	ScopeRange   *ParseContextRange           `json:"scope_range,omitempty"`
}

type SchemaColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Nullable *bool  `json:"nullable,omitempty"`
}

type ParseContextColumn struct {
	Name          string             `json:"name"`
	Qualifier     string             `json:"qualifier"`
	ResolvedTable string             `json:"resolved_table,omitempty"`
	Parts         []ParseContextPart `json:"parts"`
}

type ParseContextDiagnostic struct {
	Code     string             `json:"code,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
	Severity string             `json:"severity"`
	Range    *ParseContextRange `json:"range,omitempty"`
}

type ParseContext struct {
	QueryKind      string `json:"query_kind"`
	IsSingleSelect bool   `json:"is_single_select"`
	// IsReadOnlyResult is true for a single statement that yields a result set
	// with no side effects — a plain SELECT, a CTE-wrapped SELECT, or a set
	// operation (UNION/INTERSECT/EXCEPT). Looser than IsSingleSelect (which is
	// SELECT-only); used to decide what is safe to auto-recompute.
	IsReadOnlyResult bool                     `json:"is_read_only_result"`
	Tables           []ParseContextTable      `json:"tables"`
	Columns          []ParseContextColumn     `json:"columns"`
	Diagnostics      []ParseContextDiagnostic `json:"diagnostics"`
	Errors           []string                 `json:"errors"`
}

type parseContextRequest struct {
	Query               string                    `json:"query"`
	Dialect             string                    `json:"dialect"`
	Schema              Schema                    `json:"schema,omitempty"`
	ColumnSourceMethods SchemaColumnSourceMethods `json:"column_source_methods,omitempty"`
}

type parseContextResponse struct {
	ParseContext
	Error string `json:"error,omitempty"`
}

func ParseContextWithSchema(query, dialect string, schema Schema, columnSourceMethods ...SchemaColumnSourceMethods) (*ParseContext, error) {
	return ParseContextWithSchemaContext(context.Background(), query, dialect, schema, columnSourceMethods...)
}

func ParseContextWithSchemaContext(ctx context.Context, query, dialect string, schema Schema, columnSourceMethods ...SchemaColumnSourceMethods) (*ParseContext, error) {
	return ParseContextWithSchemaGolyglotContext(ctx, query, dialect, schema, columnSourceMethods...)
}
