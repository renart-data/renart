package sqlintelligence

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/renart-data/golyglot/pkg/golyglot"
)

// QueryAnalysis is the compact, schema-aware query result returned by
// Golyglot. It intentionally keeps the facts useful to schema inference and
// lineage without retaining the much larger annotated AST.
type QueryAnalysis struct {
	Shape           string                `json:"shape"`
	CTEs            []string              `json:"ctes"`
	CTEFacts        []QueryCTEFact        `json:"cteFacts"`
	Projections     []QueryProjection     `json:"projections"`
	Relations       []QueryRelation       `json:"relations"`
	BaseTables      []QueryRelation       `json:"baseTables"`
	StarProjections []QueryStarProjection `json:"starProjections"`
	SetOperations   []QuerySetOperation   `json:"setOperations"`

	OutputColumns       []SchemaColumn `json:"-"`
	OutputNamesComplete bool           `json:"-"`
	OutputTypesComplete bool           `json:"-"`
}

type QueryProjection struct {
	Index             int                     `json:"index"`
	Name              *string                 `json:"name"`
	IsStar            bool                    `json:"isStar"`
	StarTable         *string                 `json:"starTable"`
	TransformKind     string                  `json:"transformKind"`
	TransformFunction *QueryTransformFunction `json:"transformFunction,omitempty"`
	CastType          *string                 `json:"castType"`
	TypeHint          *string                 `json:"typeHint"`
	Nullability       string                  `json:"nullability"`
	Upstream          []QueryColumnReference  `json:"upstream"`
}

type QueryTransformFunction struct {
	Name        string                 `json:"name"`
	LiteralArgs []string               `json:"literalArgs"`
	ColumnArgs  []QueryColumnReference `json:"columnArgs"`
}

type QueryCTEFact struct {
	Name          string   `json:"name"`
	Columns       []string `json:"columns"`
	BodySQL       string   `json:"bodySql"`
	OutputColumns []string `json:"outputColumns"`
}

type QueryStarProjection struct {
	Index           int      `json:"index"`
	Table           *string  `json:"table"`
	ExpandedColumns []string `json:"expandedColumns"`
}

type QueryColumnReference struct {
	SourceName  *string `json:"sourceName"`
	SourceAlias *string `json:"sourceAlias"`
	SourceKind  string  `json:"sourceKind"`
	Table       *string `json:"table"`
	Column      string  `json:"column"`
	Unqualified bool    `json:"unqualified"`
	Confidence  string  `json:"confidence"`
}

type QueryRelation struct {
	Name    string   `json:"name"`
	Alias   *string  `json:"alias"`
	Kind    string   `json:"kind"`
	Columns []string `json:"columns"`
	Catalog *string  `json:"catalog"`
	Schema  *string  `json:"schema"`
	Table   *string  `json:"table"`
}

type QuerySetOperation struct {
	Kind          string                    `json:"kind"`
	All           bool                      `json:"all"`
	Distinct      bool                      `json:"distinct"`
	OutputColumns []string                  `json:"outputColumns"`
	Branches      []QuerySetOperationBranch `json:"branches"`
}

type QuerySetOperationBranch struct {
	Index       int               `json:"index"`
	Projections []QueryProjection `json:"projections"`
}

type analyzeQueryOptions struct {
	Dialect string                    `json:"dialect"`
	Schema  golyglot.ValidationSchema `json:"schema"`
}

const queryAnalysisCacheCapacity = 256

var sharedQueryAnalysisCache = newQueryAnalysisCache(queryAnalysisCacheCapacity)

// AnalyzeQuery returns Golyglot's compact query facts. Successful results are
// cached by SQL, normalized dialect, and deterministic schema payload so graph
// fixpoint rounds and repeated revision builds do not repeat native analysis.
// Failures and canceled requests never enter the cache.
func AnalyzeQuery(ctx context.Context, query, dialect string, schema Schema, constraintSets ...SchemaConstraints) (QueryAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return QueryAnalysis{}, err
	}
	if strings.TrimSpace(query) == "" {
		return QueryAnalysis{}, errors.New("cannot analyze empty SQL")
	}

	optionsJSON, err := marshalAnalyzeQueryOptions(dialect, schema, constraintSets...)
	if err != nil {
		return QueryAnalysis{}, err
	}
	key := queryAnalysisKey(query, optionsJSON)
	if cached, ok := sharedQueryAnalysisCache.get(key); ok {
		return cached, nil
	}

	analysis, err := analyzeQueryUncached(ctx, query, optionsJSON, schema)
	if err != nil {
		return QueryAnalysis{}, err
	}
	if err := ctx.Err(); err != nil {
		return QueryAnalysis{}, err
	}
	sharedQueryAnalysisCache.add(key, analysis)
	return analysis, nil
}

func marshalAnalyzeQueryOptions(dialect string, schema Schema, constraintSets ...SchemaConstraints) (string, error) {
	options := analyzeQueryOptions{
		Dialect: normalizeAnalyzeDialect(dialect),
		Schema:  buildGolyglotSchema(schema, constraintSets...),
	}
	raw, err := json.Marshal(options)
	return string(raw), err
}

func normalizeAnalyzeDialect(dialect string) string {
	switch strings.ToLower(strings.TrimSpace(dialect)) {
	case "", "generic":
		return string(golyglot.DialectGeneric)
	case "postgres", "postgresql":
		return "postgresql"
	default:
		return strings.ToLower(strings.TrimSpace(dialect))
	}
}

func analyzeQueryUncached(ctx context.Context, query, optionsJSON string, schema Schema) (QueryAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return QueryAnalysis{}, err
	}
	var options analyzeQueryOptions
	if err := json.Unmarshal([]byte(optionsJSON), &options); err != nil {
		return QueryAnalysis{}, err
	}
	dialect, err := golyglot.ParseDialect(options.Dialect)
	if err != nil {
		return QueryAnalysis{}, err
	}
	native, err := golyglot.AnalyzeQuery(query, golyglot.AnalyzeQueryOptions{Dialect: dialect, Schema: &options.Schema})
	if err != nil {
		return QueryAnalysis{}, err
	}
	if err := ctx.Err(); err != nil {
		return QueryAnalysis{}, err
	}
	return queryAnalysisFromGolyglot(native, schema), nil
}

func queryAnalysisFromGolyglot(native golyglot.QueryAnalysis, schema Schema) QueryAnalysis {
	result := QueryAnalysis{
		Shape:               native.Shape,
		CTEs:                append([]string(nil), native.CTEs...),
		OutputNamesComplete: native.OutputNamesComplete,
		OutputTypesComplete: native.OutputTypesComplete,
	}
	for _, fact := range native.CTEFacts {
		result.CTEFacts = append(result.CTEFacts, QueryCTEFact{Name: fact.Name, Columns: append([]string(nil), fact.Columns...), BodySQL: fact.BodySQL, OutputColumns: append([]string(nil), fact.OutputColumns...)})
	}
	for _, projection := range native.Projections {
		converted := QueryProjection{
			Index: projection.Index, Name: cloneString(projection.Name), IsStar: projection.IsStar,
			StarTable: cloneString(projection.StarTable), TransformKind: projection.TransformKind,
			CastType: cloneString(projection.CastType), TypeHint: cloneString(projection.TypeHint), Nullability: projection.Nullability,
		}
		if projection.TransformFunction != nil {
			converted.TransformFunction = &QueryTransformFunction{Name: projection.TransformFunction.Name, LiteralArgs: append([]string(nil), projection.TransformFunction.LiteralArgs...)}
			for _, column := range projection.TransformFunction.ColumnArgs {
				converted.TransformFunction.ColumnArgs = append(converted.TransformFunction.ColumnArgs, queryColumnReferenceFromGolyglot(column))
			}
		}
		for _, column := range projection.Upstream {
			converted.Upstream = append(converted.Upstream, queryColumnReferenceFromGolyglot(column))
		}
		result.Projections = append(result.Projections, converted)
	}
	for _, relation := range native.Relations {
		result.Relations = append(result.Relations, queryRelationFromGolyglot(relation))
	}
	for _, relation := range native.BaseTables {
		result.BaseTables = append(result.BaseTables, queryRelationFromGolyglot(relation))
	}
	for _, star := range native.StarProjections {
		result.StarProjections = append(result.StarProjections, QueryStarProjection{Index: star.Index, Table: cloneString(star.Table), ExpandedColumns: append([]string(nil), star.ExpandedColumns...)})
	}
	for _, set := range native.SetOperations {
		converted := QuerySetOperation{Kind: set.Kind, All: set.All, Distinct: set.Distinct, OutputColumns: append([]string(nil), set.OutputColumns...)}
		for _, branch := range set.Branches {
			convertedBranch := QuerySetOperationBranch{Index: branch.Index}
			for _, projection := range branch.Projections {
				convertedBranch.Projections = append(convertedBranch.Projections, QueryProjection{Index: projection.Index, Name: cloneString(projection.Name), IsStar: projection.IsStar, StarTable: cloneString(projection.StarTable), TransformKind: projection.TransformKind, CastType: cloneString(projection.CastType), TypeHint: cloneString(projection.TypeHint), Nullability: projection.Nullability})
			}
			converted.Branches = append(converted.Branches, convertedBranch)
		}
		result.SetOperations = append(result.SetOperations, converted)
	}
	for _, column := range native.OutputColumns {
		if strings.TrimSpace(column.Name) == "" {
			continue
		}
		columnType := ""
		if column.TypeHint != nil {
			columnType = normalizeInferredType(*column.TypeHint)
		}
		result.OutputColumns = appendUniqueSchemaColumn(result.OutputColumns, SchemaColumn{Name: column.Name, Type: columnType, Nullable: queryProjectionNullable(column.Nullability)})
	}
	if len(result.OutputColumns) == 0 {
		result.OutputNamesComplete = false
		result.OutputTypesComplete = false
	}
	return result
}

func queryColumnReferenceFromGolyglot(column golyglot.ColumnReferenceFact) QueryColumnReference {
	return QueryColumnReference{SourceName: cloneString(column.SourceName), SourceAlias: cloneString(column.SourceAlias), SourceKind: column.SourceKind, Table: cloneString(column.Table), Column: column.Column, Unqualified: column.Unqualified, Confidence: column.Confidence}
}

func queryRelationFromGolyglot(relation golyglot.RelationFact) QueryRelation {
	return QueryRelation{Name: relation.Name, Alias: cloneString(relation.Alias), Kind: relation.Kind, Columns: append([]string(nil), relation.Columns...), Catalog: cloneString(relation.Catalog), Schema: cloneString(relation.Schema), Table: cloneString(relation.Table)}
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func finalizeQueryAnalysis(analysis *QueryAnalysis, schema Schema) {
	analysis.OutputNamesComplete = len(analysis.Projections) > 0
	analysis.OutputTypesComplete = len(analysis.Projections) > 0
	analysis.OutputColumns = make([]SchemaColumn, 0, len(analysis.Projections))
	for _, projection := range analysis.Projections {
		name := ""
		if projection.Name != nil {
			name = strings.TrimSpace(*projection.Name)
		}
		if projection.IsStar || name == "" || isSyntheticProjectionName(name, projection.Index) {
			analysis.OutputNamesComplete = false
			analysis.OutputTypesComplete = false
			continue
		}

		columnType := queryProjectionType(projection, schema)
		if columnType == "" {
			analysis.OutputTypesComplete = false
		}
		analysis.OutputColumns = appendUniqueSchemaColumn(analysis.OutputColumns, SchemaColumn{
			Name:     name,
			Type:     columnType,
			Nullable: queryProjectionNullable(projection.Nullability),
		})
	}
	if len(analysis.OutputColumns) == 0 {
		analysis.OutputNamesComplete = false
		analysis.OutputTypesComplete = false
	}
}

func queryProjectionNullable(value string) *bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "non_null":
		value := false
		return &value
	case "nullable":
		value := true
		return &value
	default:
		return nil
	}
}

func isSyntheticProjectionName(name string, index int) bool {
	return strings.EqualFold(name, fmt.Sprintf("_col_%d", index))
}

func queryProjectionType(projection QueryProjection, schema Schema) string {
	if projection.TypeHint != nil && strings.TrimSpace(*projection.TypeHint) != "" {
		return normalizeInferredType(*projection.TypeHint)
	}
	if projection.CastType != nil && strings.TrimSpace(*projection.CastType) != "" {
		return normalizeInferredType(*projection.CastType)
	}

	var inferred string
	for _, upstream := range projection.Upstream {
		for _, candidate := range []*string{upstream.Table, upstream.SourceName} {
			if candidate == nil || strings.TrimSpace(*candidate) == "" {
				continue
			}
			columns, ok := schemaForName(*candidate, schema)
			if !ok {
				continue
			}
			for columnName, columnType := range columns {
				if !strings.EqualFold(strings.TrimSpace(columnName), strings.TrimSpace(upstream.Column)) || strings.TrimSpace(columnType) == "" {
					continue
				}
				normalized := normalizeInferredType(columnType)
				if inferred != "" && normalizedTypeText(inferred) != normalizedTypeText(normalized) {
					return ""
				}
				inferred = normalized
			}
		}
	}
	return inferred
}

func queryAnalysisKey(query, optionsJSON string) [sha256.Size]byte {
	return sha256.Sum256([]byte(query + "\x00" + optionsJSON))
}

type queryAnalysisCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[[sha256.Size]byte]*list.Element
	lru      *list.List
}

type queryAnalysisCacheEntry struct {
	key      [sha256.Size]byte
	analysis QueryAnalysis
}

func newQueryAnalysisCache(capacity int) *queryAnalysisCache {
	if capacity < 1 {
		capacity = 1
	}
	return &queryAnalysisCache{
		capacity: capacity,
		entries:  make(map[[sha256.Size]byte]*list.Element, capacity),
		lru:      list.New(),
	}
}

func (c *queryAnalysisCache) get(key [sha256.Size]byte) (QueryAnalysis, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return QueryAnalysis{}, false
	}
	c.lru.MoveToFront(element)
	return element.Value.(queryAnalysisCacheEntry).analysis, true
}

func (c *queryAnalysisCache) add(key [sha256.Size]byte, analysis QueryAnalysis) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[key]; ok {
		existing.Value = queryAnalysisCacheEntry{key: key, analysis: analysis}
		c.lru.MoveToFront(existing)
		return
	}
	element := c.lru.PushFront(queryAnalysisCacheEntry{key: key, analysis: analysis})
	c.entries[key] = element
	if c.lru.Len() <= c.capacity {
		return
	}
	oldest := c.lru.Back()
	delete(c.entries, oldest.Value.(queryAnalysisCacheEntry).key)
	c.lru.Remove(oldest)
}
