// Package fingerprint computes deterministic content fingerprints for every
// asset in a pipeline, with Merkle propagation along the dependency DAG.
// Everything above it — the materialization log, coverage, staleness, and
// later cross-version reuse — trusts these hashes, so determinism is the
// load-bearing wall: all inputs are sorted or length-prefixed before
// hashing, and the algorithm version is baked into every hash.
//
// A fingerprint deliberately excludes the environment: it identifies *what
// would be built*, while the environment is a storage dimension on the
// records keyed by it. Per-environment variable overrides change the
// consumed-vars hash, which is part of the fingerprint.
package fingerprint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"renart/internal/sqlformat"
	"renart/internal/web/identity"
)

// Version is the fingerprint algorithm version. It is baked into every hash
// and prefixed onto every fingerprint. Bump it whenever canonicalization
// changes: everything goes stale once, which is correct and self-healing.
//
// v2: SQL is normalized through the embedded formatter before hashing, so
// running the formatter over an asset no longer changes its fingerprint.
// v3: symbolic dependencies are lineage-only and no longer participate in
// ordering, target fingerprints, or achieved read fingerprints.
const Version = "v3"

// Fingerprint is a hex sha256 prefixed with the algorithm version ("v1:…").
type Fingerprint string

// Vars holds the effective variable values for a fingerprint computation
// (pipeline defaults merged with any per-environment overrides).
type Vars map[string]any

// Result is the fingerprint outcome for one asset.
type Result struct {
	// FP is the full Merkle fingerprint: own content + consumed vars +
	// upstream fingerprints.
	FP Fingerprint
	// OwnContent hashes only the asset's own definition (content + config +
	// pinned files). When FP mismatches history but OwnContent matches, the
	// staleness is inherited from upstream.
	OwnContent Fingerprint
	// ConsumedVars are the names (only) of the variables the asset reads;
	// values live in the hash. Persisted so a staleness check can recompute
	// the consumed-vars hash for a new variable configuration without
	// re-rendering.
	ConsumedVars []string
	// ConsumedVarsHash is the hash over the consumed (name, value) pairs.
	ConsumedVarsHash string
}

// Engine computes fingerprint DAGs. It caches disk-derived inputs (Python
// lockfiles, shared-code directories, depends_on_files pins) validated by
// stat, and formatter-normalized SQL keyed by content hash (the wasm
// formatter costs tens of milliseconds per statement — far too slow to run
// on every DAG recompute, but a given content only ever formats to one
// result, so the cache cannot go stale). Everything else in-memory is
// hashed fresh on every call.
type Engine struct {
	mu           sync.Mutex
	fileHashes   map[string]fileHashEntry
	canonicalSQL map[string]string
}

type fileHashEntry struct {
	modTime time.Time
	size    int64
	hash    string
}

// canonicalSQLCacheLimit bounds the content-keyed formatter cache; on
// overflow the cache resets (entries are repopulated on demand).
const canonicalSQLCacheLimit = 8192

func NewEngine() *Engine {
	return &Engine{
		fileHashes:   make(map[string]fileHashEntry),
		canonicalSQL: make(map[string]string),
	}
}

// Invalidate drops cached disk-input hashes. It accepts a durable asset ID
// for API symmetry with the staleness service; because the caches are keyed
// by file path (stat-validated) or content hash (immutable), dropping the
// disk cache wholesale is cheap and the SQL cache never needs dropping.
func (e *Engine) Invalidate(assetID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fileHashes = make(map[string]fileHashEntry)
}

// EffectiveVars resolves the pipeline's declared variables to their default
// values, then applies overrides (per-environment schedule vars, run
// parameters). Unknown override names are ignored.
func EffectiveVars(p *pipeline.Pipeline, overrides map[string]any) Vars {
	vars := make(Vars, len(p.Variables))
	for name, definition := range p.Variables {
		if def, ok := definition["default"]; ok {
			vars[name] = def
		} else {
			vars[name] = nil
		}
	}
	for name, value := range overrides {
		if _, declared := p.Variables[name]; declared {
			vars[name] = value
		}
	}
	return vars
}

// DAG computes fingerprints for every asset in the pipeline. Keys of the
// returned map are durable asset IDs (pipeline UUID + ":" + asset name).
func (e *Engine) DAG(p *pipeline.Pipeline, vars Vars) (map[string]Result, error) {
	if p == nil {
		return nil, fmt.Errorf("fingerprint: pipeline is nil")
	}
	pipelineUUID := strings.TrimSpace(p.LegacyID)
	if pipelineUUID == "" {
		return nil, fmt.Errorf("fingerprint: pipeline %q has no stable id", p.Name)
	}

	ordered, err := topoSort(p)
	if err != nil {
		return nil, err
	}

	declared := make(map[string]struct{}, len(p.Variables))
	for name := range p.Variables {
		declared[name] = struct{}{}
	}

	pipelineDir := filepath.Dir(p.DefinitionFile.Path)
	results := make(map[string]Result, len(ordered))
	byName := make(map[string]Result, len(ordered))

	for _, asset := range ordered {
		result, err := e.fingerprintAsset(asset, p, pipelineDir, declared, vars, byName)
		if err != nil {
			return nil, fmt.Errorf("fingerprint: asset %s: %w", asset.Name, err)
		}
		byName[asset.Name] = result
		results[identity.AssetID(pipelineUUID, asset.Name)] = result
	}
	return results, nil
}

func (e *Engine) fingerprintAsset(asset *pipeline.Asset, p *pipeline.Pipeline, pipelineDir string, declared map[string]struct{}, vars Vars, byName map[string]Result) (Result, error) {
	content := assetContent(asset)
	kind := assetKind(asset)

	// Escape hatch: meta.fingerprint_version replaces the content hash
	// entirely, capping every canonicalization edge case.
	var contentHash string
	if pinned := strings.TrimSpace(asset.Meta["fingerprint_version"]); pinned != "" {
		contentHash = hashHex("pinned", pinned)
	} else {
		switch kind {
		case kindSQL:
			contentHash = hashHex("sql", e.normalizedSQL(content, asset))
		case kindPython:
			lockHash, err := e.pythonLockfileHash(asset, pipelineDir)
			if err != nil {
				return Result{}, err
			}
			sharedHash, err := e.sharedDirHash(pipelineDir)
			if err != nil {
				return Result{}, err
			}
			contentHash = hashHex("python", content, lockHash, sharedHash)
		default:
			contentHash = hashHex("raw", content)
		}
	}

	// Escape hatch: meta.depends_on_files pins extra file contents
	// (comma-separated globs relative to the pipeline directory).
	extraHash, err := e.dependsOnFilesHash(asset, pipelineDir)
	if err != nil {
		return Result{}, err
	}

	configHash := configHash(asset, p)
	ownContent := Fingerprint(Version + ":" + hashHex(Version, string(kind), contentHash, configHash, extraHash))

	var consumed []string
	if kind == kindPython {
		// v1 assumes Python reads every declared variable; over-invalidates,
		// which is the safe direction. Phase 7 narrows this with
		// runtime-observed reads.
		consumed = make([]string, 0, len(declared))
		for name := range declared {
			consumed = append(consumed, name)
		}
		sort.Strings(consumed)
	} else {
		consumed = ConsumedVarNames(content, declared)
	}
	consumedVarsHash := VarsHash(vars, consumed)

	upstreamParts := upstreamHashParts(asset, byName)
	parts := append([]string{Version, string(ownContent), consumedVarsHash}, upstreamParts...)
	full := Fingerprint(Version + ":" + hashHex(parts...))

	return Result{
		FP:               full,
		OwnContent:       ownContent,
		ConsumedVars:     consumed,
		ConsumedVarsHash: consumedVarsHash,
	}, nil
}

// AchievedFingerprints computes the fingerprint of the data each just-succeeded
// asset actually produced — its "achieved" fingerprint, as opposed to the
// "target" fingerprint DAG returns. The two are identical except that each
// upstream contributes the fingerprint physically present when the asset read
// it: an upstream rebuilt in the same run contributes its freshly-achieved
// fingerprint (computed here in topo order), while an upstream not rebuilt
// contributes its last recorded fingerprint via latestAchieved.
//
// This is what the materialization log must persist. Recording the target
// fingerprint instead marks a downstream fresh even when it was built on a
// stale upstream — the precise silent-stale-data failure the staleness system
// exists to prevent (materialize C while B is stale: C read old B, so C is not
// current and must stay stale until rerun). Freshness is then simply
// achieved == target: an asset is fresh iff its own content/config/vars are
// current and every upstream it consumed is that upstream's current target.
//
// targets must be DAG(p, vars) for the same pipeline and vars, so the
// own-content and consumed-vars sub-hashes line up with the target computation;
// when an upstream's achieved fingerprint equals its target (a fresh upstream),
// the achieved and target fingerprints of the downstream coincide exactly.
func (e *Engine) AchievedFingerprints(
	p *pipeline.Pipeline,
	targets map[string]Result,
	succeeded map[string]bool,
	latestAchieved func(assetID string) (Fingerprint, bool),
) (map[string]Fingerprint, error) {
	return e.AchievedFingerprintsByConsumer(p, targets, succeeded, func(_, upstreamAssetID string) (Fingerprint, bool) {
		return latestAchieved(upstreamAssetID)
	})
}

// AchievedFingerprintsByConsumer is the read-set-aware form used by durable
// completion recording. latestAchieved is evaluated for the consumer that
// actually read an upstream, so a later concurrent writer cannot be
// retroactively attributed to an earlier task.
func (e *Engine) AchievedFingerprintsByConsumer(
	p *pipeline.Pipeline,
	targets map[string]Result,
	succeeded map[string]bool,
	latestAchieved func(consumerAssetID, upstreamAssetID string) (Fingerprint, bool),
) (map[string]Fingerprint, error) {
	if p == nil {
		return nil, fmt.Errorf("fingerprint: pipeline is nil")
	}
	pipelineUUID := strings.TrimSpace(p.LegacyID)
	if pipelineUUID == "" {
		return nil, fmt.Errorf("fingerprint: pipeline %q has no stable id", p.Name)
	}
	ordered, err := topoSort(p)
	if err != nil {
		return nil, err
	}
	inPipeline := make(map[string]*pipeline.Asset, len(p.Assets))
	for _, asset := range p.Assets {
		inPipeline[asset.Name] = asset
	}

	achieved := make(map[string]Fingerprint, len(ordered))
	for _, asset := range ordered {
		assetID := identity.AssetID(pipelineUUID, asset.Name)
		if !succeeded[assetID] {
			continue
		}
		result, ok := targets[assetID]
		if !ok {
			return nil, fmt.Errorf("fingerprint: achieved: no target fingerprint for %s", asset.Name)
		}
		parts := make([]string, 0, len(asset.Upstreams))
		for _, upstream := range asset.Upstreams {
			if upstream.Mode == pipeline.UpstreamModeSymbolic {
				continue
			}
			upstreamAsset, ok := inPipeline[upstream.Value]
			if !ok {
				parts = append(parts, "ext:"+upstream.Type+":"+upstream.Value)
				continue
			}
			upID := identity.AssetID(pipelineUUID, upstream.Value)
			if isSourceAsset(upstreamAsset) {
				// Source assets describe data maintained outside Renart. They never
				// receive a materialization fact, so the declaration fingerprint is
				// the exact read contract for an in-pipeline consumer. This lets a
				// successful consumer become fresh while still cascading source
				// declaration edits through the ordinary target fingerprint.
				result, exists := targets[upID]
				if !exists {
					return nil, fmt.Errorf("fingerprint: achieved: no target fingerprint for source %s", upstream.Value)
				}
				parts = append(parts, "up:"+string(result.FP))
			} else if fp, ok := achieved[upID]; ok {
				parts = append(parts, "up:"+string(fp))
			} else if fp, ok := latestAchieved(assetID, upID); ok {
				parts = append(parts, "up:"+string(fp))
			} else {
				// The upstream has never materialized in this environment, so the
				// asset read no current upstream data. Keep this distinct from the
				// target's "up:"+fingerprint so the asset stays stale until rerun.
				parts = append(parts, "up-unbuilt:"+upstream.Value)
			}
		}
		sort.Strings(parts)
		hashParts := append([]string{Version, string(result.OwnContent), result.ConsumedVarsHash}, parts...)
		achieved[assetID] = Fingerprint(Version + ":" + hashHex(hashParts...))
	}
	return achieved, nil
}

func isSourceAsset(asset *pipeline.Asset) bool {
	return asset != nil && strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".source")
}

// upstreamHashParts returns the sorted upstream contribution: resolved
// assets contribute their fingerprints, external dependencies (URIs, assets
// outside this pipeline) contribute a stable token of their name.
func upstreamHashParts(asset *pipeline.Asset, byName map[string]Result) []string {
	parts := make([]string, 0, len(asset.Upstreams))
	for _, upstream := range asset.Upstreams {
		if upstream.Mode == pipeline.UpstreamModeSymbolic {
			continue
		}
		if result, ok := byName[upstream.Value]; ok {
			parts = append(parts, "up:"+string(result.FP))
		} else {
			parts = append(parts, "ext:"+upstream.Type+":"+upstream.Value)
		}
	}
	sort.Strings(parts)
	return parts
}

// normalizedSQL is the v2 SQL canonical form: strip comments and collapse
// whitespace (CanonicalSQL), then run the embedded SQL formatter and
// collapse again, so formatter-induced rewrites (keyword casing, trailing
// commas, layout) never change the fingerprint. When the formatter cannot
// parse the statement (e.g. Jinja in identifier position) the stripped form
// is used — deterministic for a given content either way. Results are
// cached by content hash because a single format call costs tens of
// milliseconds.
func (e *Engine) normalizedSQL(content string, asset *pipeline.Asset) string {
	stripped := CanonicalSQL(content)
	dialect := sqlDialectForAsset(asset)
	key := hashHex("canonical-sql", dialect, stripped)

	e.mu.Lock()
	if cached, ok := e.canonicalSQL[key]; ok {
		e.mu.Unlock()
		return cached
	}
	e.mu.Unlock()

	canonical := stripped
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if formatted, err := sqlformat.Format(ctx, stripped, dialect); err == nil {
		canonical = CanonicalSQL(formatted)
	}
	cancel()

	e.mu.Lock()
	if len(e.canonicalSQL) >= canonicalSQLCacheLimit {
		e.canonicalSQL = make(map[string]string)
	}
	e.canonicalSQL[key] = canonical
	e.mu.Unlock()
	return canonical
}

// sqlDialectForAsset mirrors the formatter dialect mapping used by the
// format-on-save path (service.sqlFormatDialectForAssetType), kept local to
// avoid pulling the service package into the fingerprint dependency graph.
func sqlDialectForAsset(asset *pipeline.Asset) string {
	switch asset.Type {
	case pipeline.AssetTypeBigqueryQuery:
		return "bigquery"
	case pipeline.AssetTypeSnowflakeQuery:
		return "snowflake"
	case pipeline.AssetTypePostgresQuery:
		return "postgresql"
	case pipeline.AssetTypeRedshiftQuery:
		return "redshift"
	case pipeline.AssetTypeTrinoQuery:
		return "trino"
	case pipeline.AssetTypeAthenaQuery:
		return "athena"
	case pipeline.AssetTypeClickHouse:
		return "clickhouse"
	case pipeline.AssetTypeDatabricksQuery:
		return "databricks"
	case pipeline.AssetTypeMsSQLQuery, pipeline.AssetTypeSynapseQuery:
		return "tsql"
	case pipeline.AssetTypeDuckDBQuery:
		return "duckdb"
	default:
		return sqlformat.DialectGeneric
	}
}

type assetKindType string

const (
	kindSQL    assetKindType = "sql"
	kindPython assetKindType = "python"
	kindOther  assetKindType = "other"
)

func assetKind(asset *pipeline.Asset) assetKindType {
	typeName := strings.ToLower(string(asset.Type))
	path := strings.ToLower(asset.ExecutableFile.Path)
	switch {
	case strings.Contains(typeName, "python") || strings.HasSuffix(path, ".py"):
		return kindPython
	case strings.HasSuffix(typeName, ".sql") || strings.Contains(typeName, "sql") || strings.HasSuffix(path, ".sql"):
		return kindSQL
	default:
		return kindOther
	}
}

func assetContent(asset *pipeline.Asset) string {
	if asset.ExecutableFile.Content != "" {
		return asset.ExecutableFile.Content
	}
	if asset.DefinitionFile.Path != "" {
		if data, err := os.ReadFile(asset.DefinitionFile.Path); err == nil {
			return string(data)
		}
	}
	return ""
}

// fingerprintedConfig is the canonical subset of asset configuration that
// changes what lands in the warehouse. Display-only metadata (description,
// owner, tags) is deliberately excluded.
type fingerprintedConfig struct {
	Name              string                        `json:"name"`
	Type              string                        `json:"type"`
	Connection        string                        `json:"connection"`
	Materialization   *fingerprintedMaterialization `json:"materialization"`
	Parameters        pipeline.ParameterMap         `json:"parameters,omitempty"`
	Image             string                        `json:"image,omitempty"`
	Instance          string                        `json:"instance,omitempty"`
	IntervalModifiers pipeline.IntervalModifiers    `json:"interval_modifiers"`
	Columns           []fingerprintedColumn         `json:"columns,omitempty"`
}

// fingerprintedMaterialization is deliberately owned by Renart instead of
// embedding pipeline.Materialization. Bruin occasionally adds execution
// fields to its struct; an empty new field must not silently change Renart's
// existing fingerprint version. New fields that affect produced data can be
// added here with omitempty so existing assets remain stable.
type fingerprintedMaterialization struct {
	Type                 pipeline.MaterializationType            `json:"type"`
	Strategy             pipeline.MaterializationStrategy        `json:"strategy"`
	PartitionBy          string                                  `json:"partition_by"`
	ClusterBy            []string                                `json:"cluster_by"`
	IncrementalKey       string                                  `json:"incremental_key"`
	TimeGranularity      pipeline.MaterializationTimeGranularity `json:"time_granularity"`
	IncrementalPredicate string                                  `json:"incremental_predicate,omitempty"`
}

type fingerprintedColumn struct {
	Name          string `json:"name"`
	Type          string `json:"type,omitempty"`
	PrimaryKey    bool   `json:"primary_key,omitempty"`
	UpdateOnMerge bool   `json:"update_on_merge,omitempty"`
	MergeSQL      string `json:"merge_sql,omitempty"`
	Nullable      bool   `json:"nullable,omitempty"`
}

func configHash(asset *pipeline.Asset, p *pipeline.Pipeline) string {
	connection := asset.Connection
	if connection == "" && p != nil {
		if resolved, err := p.GetConnectionNameForAsset(asset); err == nil {
			connection = resolved
		}
	}

	columns := make([]fingerprintedColumn, 0, len(asset.Columns))
	for _, column := range asset.Columns {
		columns = append(columns, fingerprintedColumn{
			Name:          column.Name,
			Type:          column.Type,
			PrimaryKey:    column.PrimaryKey,
			UpdateOnMerge: column.UpdateOnMerge,
			MergeSQL:      column.MergeSQL,
			Nullable:      column.Nullable.Bool(),
		})
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i].Name < columns[j].Name })

	cfg := fingerprintedConfig{
		Name:              asset.Name,
		Type:              string(asset.Type),
		Connection:        connection,
		Materialization:   materializationForFingerprint(asset.Materialization),
		Parameters:        asset.Parameters,
		Image:             asset.Image,
		Instance:          asset.Instance,
		IntervalModifiers: asset.IntervalModifiers,
		Columns:           columns,
	}
	return hashHex("config", canonicalJSON(cfg))
}

func materializationForFingerprint(materialization pipeline.Materialization) *fingerprintedMaterialization {
	if materialization.Type == "" &&
		materialization.Strategy == "" &&
		materialization.PartitionBy == "" &&
		len(materialization.ClusterBy) == 0 &&
		materialization.IncrementalKey == "" &&
		materialization.TimeGranularity == "" &&
		materialization.IncrementalPredicate == "" {
		return nil
	}

	return &fingerprintedMaterialization{
		Type:                 materialization.Type,
		Strategy:             materialization.Strategy,
		PartitionBy:          materialization.PartitionBy,
		ClusterBy:            materialization.ClusterBy,
		IncrementalKey:       materialization.IncrementalKey,
		TimeGranularity:      materialization.TimeGranularity,
		IncrementalPredicate: materialization.IncrementalPredicate,
	}
}

func topoSort(p *pipeline.Pipeline) ([]*pipeline.Asset, error) {
	byName := make(map[string]*pipeline.Asset, len(p.Assets))
	for _, asset := range p.Assets {
		byName[asset.Name] = asset
	}

	// Sort roots and neighbors for deterministic ordering.
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(byName))
	ordered := make([]*pipeline.Asset, 0, len(byName))

	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("fingerprint: dependency cycle through asset %q", name)
		}
		state[name] = visiting
		asset := byName[name]
		upstreams := make([]string, 0, len(asset.Upstreams))
		for _, upstream := range asset.Upstreams {
			if upstream.Mode == pipeline.UpstreamModeSymbolic {
				continue
			}
			if _, ok := byName[upstream.Value]; ok {
				upstreams = append(upstreams, upstream.Value)
			}
		}
		sort.Strings(upstreams)
		for _, upstream := range upstreams {
			if err := visit(upstream); err != nil {
				return err
			}
		}
		state[name] = done
		ordered = append(ordered, asset)
		return nil
	}

	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
