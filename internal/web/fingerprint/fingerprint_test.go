package fingerprint

import (
	"path/filepath"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sqlAsset(name, content string, upstreams ...string) *pipeline.Asset {
	asset := &pipeline.Asset{
		Name: name,
		Type: "duckdb.sql",
		ExecutableFile: pipeline.ExecutableFile{
			Path:    "/workspace/p/assets/" + name + ".sql",
			Content: content,
		},
		Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeTable},
	}
	for _, upstream := range upstreams {
		asset.Upstreams = append(asset.Upstreams, pipeline.Upstream{Type: "asset", Value: upstream})
	}
	return asset
}

func sourceAsset(name, content string) *pipeline.Asset {
	return &pipeline.Asset{
		Name: name,
		Type: "pg.source",
		ExecutableFile: pipeline.ExecutableFile{
			Path:    "/workspace/p/assets/" + name + ".asset.yml",
			Content: content,
		},
	}
}

func testPipeline(assets ...*pipeline.Asset) *pipeline.Pipeline {
	return &pipeline.Pipeline{
		LegacyID:       "pipeline-uuid",
		Name:           "test",
		DefinitionFile: pipeline.DefinitionFile{Path: "/workspace/p/pipeline.yml"},
		Assets:         assets,
		Variables: pipeline.Variables{
			"region": map[string]any{"type": "string", "default": "eu"},
			"limit":  map[string]any{"type": "integer", "default": 100},
		},
	}
}

func dagOf(t *testing.T, p *pipeline.Pipeline, vars Vars) map[string]Result {
	t.Helper()
	results, err := NewEngine().DAG(p, vars)
	require.NoError(t, err)
	return results
}

func TestDAGIsDeterministic(t *testing.T) {
	t.Parallel()
	build := func() map[string]Result {
		p := testPipeline(
			sqlAsset("a", "select 1"),
			sqlAsset("b", "select * from a where region = '{{ var.region }}'", "a"),
			sqlAsset("c", "select * from b", "b"),
		)
		return dagOf(t, p, Vars{"region": "eu", "limit": 100})
	}
	first := build()
	for i := 0; i < 5; i++ {
		assert.Equal(t, first, build())
	}
}

func TestCommentAndWhitespaceEditsDoNotChangeFingerprint(t *testing.T) {
	t.Parallel()
	base := dagOf(t, testPipeline(sqlAsset("a", "select 1 from t")), Vars{})
	commented := dagOf(t, testPipeline(sqlAsset("a", "-- a comment\nselect 1 /* block */ from t")), Vars{})
	reformatted := dagOf(t, testPipeline(sqlAsset("a", "select   1\n\tfrom\n  t")), Vars{})

	assert.Equal(t, base["pipeline-uuid:a"].FP, commented["pipeline-uuid:a"].FP)
	assert.Equal(t, base["pipeline-uuid:a"].FP, reformatted["pipeline-uuid:a"].FP)
}

func TestFormatterStyleEditsDoNotChangeFingerprint(t *testing.T) {
	t.Parallel()
	// What the format-on-save action produces must fingerprint identically
	// to what the user originally wrote: keyword casing, trailing commas,
	// and layout are all normalized through the same formatter.
	base := dagOf(t, testPipeline(sqlAsset("a", "select a,b, from t where x=1")), Vars{})
	formatted := dagOf(t, testPipeline(sqlAsset("a", "SELECT\n  a,\n  b\nFROM t\nWHERE\n  x = 1")), Vars{})
	keywordCase := dagOf(t, testPipeline(sqlAsset("a", "SELECT a, b FROM t WHERE x = 1")), Vars{})

	assert.Equal(t, base["pipeline-uuid:a"].FP, formatted["pipeline-uuid:a"].FP)
	assert.Equal(t, base["pipeline-uuid:a"].FP, keywordCase["pipeline-uuid:a"].FP)

	// Identifier case is still significant (no cross-dialect case folding).
	identifierCase := dagOf(t, testPipeline(sqlAsset("a", "select A, B from t where x=1")), Vars{})
	assert.NotEqual(t, base["pipeline-uuid:a"].FP, identifierCase["pipeline-uuid:a"].FP)
}

func TestUnparseableSQLFallsBackDeterministically(t *testing.T) {
	t.Parallel()
	// Jinja in identifier position defeats the formatter; the stripped
	// canonical form is used instead, deterministically.
	content := "select * from {{ var.table }} where x = 1"
	p := func() *pipeline.Pipeline {
		pl := testPipeline(sqlAsset("a", content))
		pl.Variables["table"] = map[string]any{"type": "string", "default": "events"}
		return pl
	}
	first := dagOf(t, p(), Vars{"table": "events"})
	second := dagOf(t, p(), Vars{"table": "events"})
	assert.Equal(t, first["pipeline-uuid:a"].FP, second["pipeline-uuid:a"].FP)
	assert.Contains(t, first["pipeline-uuid:a"].ConsumedVars, "table")
}

func TestUpstreamEditCascadesDownstreamOnly(t *testing.T) {
	t.Parallel()
	build := func(aSQL string) map[string]Result {
		p := testPipeline(
			sqlAsset("a", aSQL),
			sqlAsset("b", "select * from a", "a"),
			sqlAsset("c", "select * from b", "b"),
			sqlAsset("d", "select 2"),
		)
		return dagOf(t, p, Vars{})
	}
	before := build("select 1")
	after := build("select 1, 2")

	assert.NotEqual(t, before["pipeline-uuid:a"].FP, after["pipeline-uuid:a"].FP)
	assert.NotEqual(t, before["pipeline-uuid:b"].FP, after["pipeline-uuid:b"].FP)
	assert.NotEqual(t, before["pipeline-uuid:c"].FP, after["pipeline-uuid:c"].FP)
	assert.Equal(t, before["pipeline-uuid:d"].FP, after["pipeline-uuid:d"].FP)

	// Downstream own-content stays put: the staleness service uses this to
	// distinguish stale_edited from stale_upstream.
	assert.Equal(t, before["pipeline-uuid:b"].OwnContent, after["pipeline-uuid:b"].OwnContent)
}

func TestSymbolicDependenciesDoNotAffectFingerprintsOrOrdering(t *testing.T) {
	t.Parallel()
	build := func(upstreamSQL string) map[string]Result {
		upstream := sqlAsset("a", upstreamSQL)
		consumer := sqlAsset("b", "select 2")
		consumer.Upstreams = []pipeline.Upstream{{
			Type: "asset", Value: upstream.Name, Mode: pipeline.UpstreamModeSymbolic,
		}}
		// A full edge back would be a cycle if the symbolic relationship were
		// incorrectly included in execution ordering.
		upstream.Upstreams = []pipeline.Upstream{{Type: "asset", Value: consumer.Name}}
		return dagOf(t, testPipeline(upstream, consumer), Vars{})
	}

	before := build("select 1")
	after := build("select 3")
	assert.NotEqual(t, before["pipeline-uuid:a"].FP, after["pipeline-uuid:a"].FP)
	assert.Equal(t, before["pipeline-uuid:b"].FP, after["pipeline-uuid:b"].FP)
}

func TestSymbolicExternalDependencyDoesNotChangeTargetOrAchievedFingerprint(t *testing.T) {
	t.Parallel()
	without := sqlAsset("consumer", "select 1")
	withSymbolic := sqlAsset("consumer", "select 1")
	withSymbolic.Upstreams = []pipeline.Upstream{{
		Type: "uri", Value: "warehouse://raw/orders", Mode: pipeline.UpstreamModeSymbolic,
	}}

	withoutTargets := dagOf(t, testPipeline(without), Vars{})
	p := testPipeline(withSymbolic)
	engine := NewEngine()
	withTargets, err := engine.DAG(p, Vars{})
	require.NoError(t, err)
	assetID := "pipeline-uuid:consumer"
	assert.Equal(t, withoutTargets[assetID].FP, withTargets[assetID].FP)

	achieved, err := engine.AchievedFingerprints(
		p,
		withTargets,
		map[string]bool{assetID: true},
		func(string) (Fingerprint, bool) { return "", false },
	)
	require.NoError(t, err)
	assert.Equal(t, withTargets[assetID].FP, achieved[assetID])
}

func TestSourceDeclarationIsAnImmediatelyAchievableReadContract(t *testing.T) {
	t.Parallel()
	source := sourceAsset("external.orders", "name: external.orders\ntype: pg.source\ncolumns:\n  - name: id\n")
	consumer := sqlAsset("analytics.orders", "select * from external.orders", source.Name)
	p := testPipeline(source, consumer)
	engine := NewEngine()
	targets, err := engine.DAG(p, Vars{})
	require.NoError(t, err)

	consumerID := "pipeline-uuid:" + consumer.Name
	achieved, err := engine.AchievedFingerprints(
		p,
		targets,
		map[string]bool{consumerID: true},
		func(string) (Fingerprint, bool) { return "", false },
	)
	require.NoError(t, err)
	assert.Equal(t, targets[consumerID].FP, achieved[consumerID])

	source.ExecutableFile.Content += "  - name: created_at\n"
	updatedTargets, err := engine.DAG(p, Vars{})
	require.NoError(t, err)
	updatedAchieved, err := engine.AchievedFingerprints(
		p,
		updatedTargets,
		map[string]bool{consumerID: true},
		func(string) (Fingerprint, bool) { return "", false },
	)
	require.NoError(t, err)
	assert.NotEqual(t, targets[consumerID].FP, updatedTargets[consumerID].FP)
	assert.Equal(t, updatedTargets[consumerID].FP, updatedAchieved[consumerID])
}

func TestUnconsumedVarFlipDoesNotChangeFingerprint(t *testing.T) {
	t.Parallel()
	p := func() *pipeline.Pipeline {
		return testPipeline(
			sqlAsset("a", "select * from t where region = '{{ var.region }}'"),
			sqlAsset("b", "select 1"),
		)
	}
	before := dagOf(t, p(), Vars{"region": "eu", "limit": 100})
	limitFlipped := dagOf(t, p(), Vars{"region": "eu", "limit": 999})
	regionFlipped := dagOf(t, p(), Vars{"region": "us", "limit": 100})

	assert.Equal(t, before["pipeline-uuid:a"].FP, limitFlipped["pipeline-uuid:a"].FP)
	assert.NotEqual(t, before["pipeline-uuid:a"].FP, regionFlipped["pipeline-uuid:a"].FP)
	assert.Equal(t, before["pipeline-uuid:b"].FP, regionFlipped["pipeline-uuid:b"].FP)
	assert.Equal(t, []string{"region"}, before["pipeline-uuid:a"].ConsumedVars)
}

func TestConfigChangeChangesFingerprint(t *testing.T) {
	t.Parallel()
	tableAsset := sqlAsset("a", "select 1")
	viewAsset := sqlAsset("a", "select 1")
	viewAsset.Materialization.Type = pipeline.MaterializationTypeView

	before := dagOf(t, testPipeline(tableAsset), Vars{})
	after := dagOf(t, testPipeline(viewAsset), Vars{})
	assert.NotEqual(t, before["pipeline-uuid:a"].FP, after["pipeline-uuid:a"].FP)
}

func TestIncrementalPredicateChangesFingerprintWithoutDestabilizingEmptyMaterializations(t *testing.T) {
	t.Parallel()
	baseAsset := sqlAsset("a", "select 1")
	predicateAsset := sqlAsset("a", "select 1")
	predicateAsset.Materialization.IncrementalPredicate = "event_at >= '{{ start_datetime }}'"

	base := dagOf(t, testPipeline(baseAsset), Vars{})
	withPredicate := dagOf(t, testPipeline(predicateAsset), Vars{})

	assert.NotEqual(t, base["pipeline-uuid:a"].FP, withPredicate["pipeline-uuid:a"].FP)
}

func TestPinnedVersionEscapeHatchReplacesContentHash(t *testing.T) {
	t.Parallel()
	pinned := func(content string) map[string]Result {
		asset := sqlAsset("a", content)
		asset.Meta = map[string]string{"fingerprint_version": "7"}
		return dagOf(t, testPipeline(asset), Vars{})
	}
	assert.Equal(t, pinned("select 1")["pipeline-uuid:a"].FP, pinned("select 2")["pipeline-uuid:a"].FP)
}

func TestDependsOnFilesEscapeHatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, writeFile(filepath.Join(dir, "pipeline.yml"), "id: x\n"))
	require.NoError(t, writeFile(filepath.Join(dir, "seed.csv"), "a,b\n1,2\n"))

	build := func() map[string]Result {
		asset := sqlAsset("a", "select 1")
		asset.Meta = map[string]string{"depends_on_files": "seed.csv"}
		p := testPipeline(asset)
		p.DefinitionFile.Path = filepath.Join(dir, "pipeline.yml")
		return dagOf(t, p, Vars{})
	}
	before := build()
	require.NoError(t, writeFile(filepath.Join(dir, "seed.csv"), "a,b\n3,4\n"))
	after := build()
	assert.NotEqual(t, before["pipeline-uuid:a"].FP, after["pipeline-uuid:a"].FP)
}

func TestDependencyCycleErrors(t *testing.T) {
	t.Parallel()
	p := testPipeline(
		sqlAsset("a", "select * from b", "b"),
		sqlAsset("b", "select * from a", "a"),
	)
	_, err := NewEngine().DAG(p, Vars{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestExternalUpstreamIsStableToken(t *testing.T) {
	t.Parallel()
	build := func() map[string]Result {
		p := testPipeline(sqlAsset("a", "select * from other_pipeline.table", "other_pipeline.table"))
		return dagOf(t, p, Vars{})
	}
	assert.Equal(t, build()["pipeline-uuid:a"].FP, build()["pipeline-uuid:a"].FP)
}

func TestPythonAssetHashesFileBytes(t *testing.T) {
	t.Parallel()
	pyAsset := func(content string) *pipeline.Asset {
		return &pipeline.Asset{
			Name: "py",
			Type: "python",
			ExecutableFile: pipeline.ExecutableFile{
				Path:    "/workspace/p/assets/py.py",
				Content: content,
			},
		}
	}
	// Python v1 hashes raw bytes: even a comment edit invalidates
	// (accepted limitation until Phase 7).
	before := dagOf(t, testPipeline(pyAsset("print(1)")), Vars{})
	commentEdit := dagOf(t, testPipeline(pyAsset("# note\nprint(1)")), Vars{})
	assert.NotEqual(t, before["pipeline-uuid:py"].FP, commentEdit["pipeline-uuid:py"].FP)

	// Python assumes all declared vars are consumed.
	assert.ElementsMatch(t, []string{"region", "limit"}, before["pipeline-uuid:py"].ConsumedVars)
}

func TestFingerprintsCarryVersionPrefix(t *testing.T) {
	t.Parallel()
	results := dagOf(t, testPipeline(sqlAsset("a", "select 1")), Vars{})
	assert.Regexp(t, "^"+Version+":[0-9a-f]{64}$", string(results["pipeline-uuid:a"].FP))
	assert.Regexp(t, "^"+Version+":[0-9a-f]{64}$", string(results["pipeline-uuid:a"].OwnContent))
}
