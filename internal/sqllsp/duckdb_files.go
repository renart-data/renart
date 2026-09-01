package sqllsp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/query"
)

var windowsAbsolutePathPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

var duckDBLocalFileExtensions = map[string]struct{}{
	".arrow":   {},
	".avro":    {},
	".csv":     {},
	".csv.gz":  {},
	".json":    {},
	".jsonl":   {},
	".ndjson":  {},
	".parquet": {},
	".tsv":     {},
	".tsv.gz":  {},
}

// IsDuckDBLocalFileRelation reports whether a relation uses DuckDB's direct
// local-file table syntax. Remote URLs are intentionally excluded: disabling
// LocalFileSystem should not mislabel S3 or HTTP relations.
func IsDuckDBLocalFileRelation(relation string) bool {
	relation = strings.Trim(strings.TrimSpace(relation), "`\"'")
	if relation == "" || strings.Contains(relation, "://") {
		return false
	}
	if filepath.IsAbs(relation) || windowsAbsolutePathPattern.MatchString(relation) ||
		strings.HasPrefix(relation, ".") || strings.ContainsAny(relation, `/\`) {
		return true
	}
	lower := strings.ToLower(relation)
	for extension := range duckDBLocalFileExtensions {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

type duckDBFileSchemaSignature struct {
	digest [sha256.Size]byte
}

type duckDBFileSchemaEntry struct {
	signature duckDBFileSchemaSignature
	columns   []ColumnInfo
}

// DuckDBFileSchemaCache keeps editor requests from reopening DuckDB for the
// same unchanged file on every keystroke.
type DuckDBFileSchemaCache struct {
	mu      sync.Mutex
	entries map[string]duckDBFileSchemaEntry
}

func NewDuckDBFileSchemaCache() *DuckDBFileSchemaCache {
	return &DuckDBFileSchemaCache{entries: make(map[string]duckDBFileSchemaEntry)}
}

func (c *DuckDBFileSchemaCache) columns(ctx context.Context, filePath string) ([]ColumnInfo, error) {
	signature, err := duckDBFileSignature(filePath)
	if err != nil {
		return nil, err
	}
	if c != nil {
		c.mu.Lock()
		entry, ok := c.entries[filePath]
		c.mu.Unlock()
		if ok && entry.signature == signature {
			return append([]ColumnInfo(nil), entry.columns...), nil
		}
	}

	client, err := duck.NewClient(duck.Config{Path: ""})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	escapedPath := strings.ReplaceAll(filepath.ToSlash(filePath), "'", "''")
	result, err := client.SelectWithSchema(ctx, &query.Query{Query: "select * from '" + escapedPath + "' limit 0"})
	if err != nil {
		return nil, err
	}
	columns := make([]ColumnInfo, 0, len(result.Columns))
	for index, name := range result.Columns {
		column := ColumnInfo{Name: name}
		if index < len(result.ColumnTypes) {
			column.Type = result.ColumnTypes[index]
		}
		columns = append(columns, column)
	}
	if c != nil {
		c.mu.Lock()
		if c.entries == nil {
			c.entries = make(map[string]duckDBFileSchemaEntry)
		}
		c.entries[filePath] = duckDBFileSchemaEntry{signature: signature, columns: append([]ColumnInfo(nil), columns...)}
		c.mu.Unlock()
	}
	return columns, nil
}

func duckDBFileSignature(filePath string) (duckDBFileSchemaSignature, error) {
	paths := []string{filePath}
	if strings.ContainsAny(filePath, "*?[") {
		matches, err := filepath.Glob(filePath)
		if err != nil {
			return duckDBFileSchemaSignature{}, err
		}
		if len(matches) == 0 {
			return duckDBFileSchemaSignature{}, &os.PathError{Op: "glob", Path: filePath, Err: os.ErrNotExist}
		}
		paths = matches
	}
	hash := sha256.New()
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return duckDBFileSchemaSignature{}, err
		}
		if !info.Mode().IsRegular() {
			return duckDBFileSchemaSignature{}, fmt.Errorf("DuckDB file relation is not a regular file: %s", path)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", filepath.Clean(path), info.Size(), info.ModTime().UnixNano())
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return duckDBFileSchemaSignature{digest: digest}, nil
}

// EnrichDuckDBFileRelations adds request-local relation/schema nodes for files
// referenced by the unsaved SQL document. Schema discovery is best-effort: a
// missing or temporarily invalid file remains a valid DuckDB relation syntax
// and therefore does not become a misleading unknown-table diagnostic.
func EnrichDuckDBFileRelations(
	ctx context.Context,
	graph CanonicalGraph,
	doc TextDocumentItem,
	workspaceRoot string,
	cache *DuckDBFileSchemaCache,
) CanonicalGraph {
	if cache == nil {
		cache = NewDuckDBFileSchemaCache()
	}
	analysis := analyzeSQL(doc.Text)
	existingRelations := make(map[string]string, len(graph.Relations))
	for _, relation := range graph.Relations {
		existingRelations[strings.ToLower(relation.Name)] = relation.ID
	}
	seen := make(map[string]struct{})
	for _, use := range analysis.relations {
		if !IsDuckDBLocalFileRelation(use.name) {
			continue
		}
		nameKey := strings.ToLower(use.name)
		if _, ok := seen[nameKey]; ok {
			continue
		}
		seen[nameKey] = struct{}{}
		resolvedPath := resolveDuckDBFilePath(workspaceRoot, use.name)
		relationID, ok := existingRelations[nameKey]
		if !ok {
			relationID = duckDBFileRelationID(resolvedPath)
			graph.Relations = append(graph.Relations, RelationNode{
				ID:   relationID,
				Name: use.name,
				Provenance: []Provenance{{
					Provider:   "duckdb-file",
					ProviderID: resolvedPath,
					URI:        FileURI(resolvedPath),
					Confidence: "high",
				}},
			})
			existingRelations[nameKey] = relationID
		}
		columns, err := cache.columns(ctx, resolvedPath)
		if err != nil || len(columns) == 0 {
			continue
		}
		graph.Schemas = append(graph.Schemas, SchemaLayer{
			RelationID:   relationID,
			SourceKind:   "duckdb-file",
			Completeness: "complete",
			Confidence:   "high",
			Columns:      columns,
		})
	}
	return graph
}

func resolveDuckDBFilePath(workspaceRoot, relation string) string {
	relation = strings.Trim(strings.TrimSpace(relation), "`\"'")
	relation = filepath.FromSlash(relation)
	if filepath.IsAbs(relation) || windowsAbsolutePathPattern.MatchString(relation) {
		return filepath.Clean(relation)
	}
	return filepath.Clean(filepath.Join(workspaceRoot, relation))
}

func duckDBFileRelationID(filePath string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(filePath)))
	return fmt.Sprintf("duckdb-file:%x", digest[:12])
}
