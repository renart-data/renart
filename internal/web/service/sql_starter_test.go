package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func starterColumns(names ...string) []pipeline.Column {
	columns := make([]pipeline.Column, 0, len(names))
	for _, name := range names {
		columns = append(columns, pipeline.Column{Name: name})
	}
	return columns
}

func TestDownstreamSQLStarterListsKnownColumns(t *testing.T) {
	t.Parallel()
	style := sqlStarterStyle{Dialect: "duckdb"}

	assert.Equal(t, "SELECT\n    event_id,\n    user_id,\n    \"Event Name\",\n    \"order\"\nFROM product.events\n",
		downstreamSQLStarter([]sqlStarterSource{{Name: "product.events", Columns: starterColumns("event_id", "user_id", "Event Name", "order")}}, style))
	assert.Equal(t, "SELECT *\nFROM product.events\n",
		downstreamSQLStarter([]sqlStarterSource{{Name: "product.events"}}, style))
	assert.Equal(t, "select *\nfrom product.events\n",
		downstreamSQLStarter([]sqlStarterSource{{Name: "product.events"}}, sqlStarterStyle{Lowercase: true}))
	assert.Equal(t, "SELECT\n    `Event Name`\nFROM raw.events\n",
		downstreamSQLStarter([]sqlStarterSource{{Name: "raw.events", Columns: starterColumns("Event Name")}}, sqlStarterStyle{Dialect: "bigquery"}))
	assert.Equal(t, "SELECT\n    \"UserId\"\nFROM raw.events\n",
		downstreamSQLStarter([]sqlStarterSource{{Name: "raw.events", Columns: starterColumns("UserId")}}, sqlStarterStyle{Dialect: "postgres"}))
}

func TestDownstreamSQLStarterJoinsSourcesOnSharedKeys(t *testing.T) {
	t.Parallel()
	users := []pipeline.Column{{Name: "user_id", PrimaryKey: true}, {Name: "plan"}, {Name: "country"}}
	events := starterColumns("event_id", "user_id", "event_date")

	assert.Equal(t, `SELECT
    events.event_id,
    events.user_id,
    events.event_date,
    users.plan,
    users.country
FROM product.events AS events
LEFT JOIN product.users AS users
    ON events.user_id = users.user_id
`, downstreamSQLStarter([]sqlStarterSource{
		{Name: "product.events", Columns: events},
		{Name: "product.users", Columns: users},
	}, sqlStarterStyle{Dialect: "duckdb"}))

	// Unknown columns select alias.*, and no shared key leaves an explicit
	// CROSS JOIN for the user to replace.
	assert.Equal(t, `select
    events.*,
    events_2.*
from raw.events as events
-- No shared column with events_2: replace the CROSS JOIN with a join condition.
cross join product.events as events_2
`, downstreamSQLStarter([]sqlStarterSource{{Name: "raw.events"}, {Name: "product.events"}}, sqlStarterStyle{Lowercase: true}))
}

func TestJoinedSQLQueryWrapsTheExistingQuery(t *testing.T) {
	t.Parallel()
	query := "-- Daily totals\nSELECT user_id, count(*) AS n\nFROM product.events\nGROUP BY user_id;\n"
	got := joinedSQLQuery("product.activity", query, starterColumns("user_id", "n"),
		sqlStarterSource{Name: "product.users", Columns: []pipeline.Column{{Name: "user_id", PrimaryKey: true}, {Name: "plan"}}},
		sqlStarterStyle{Dialect: "duckdb"})

	assert.Equal(t, `WITH activity AS (
    -- Daily totals
    SELECT user_id, count(*) AS n
    FROM product.events
    GROUP BY user_id
)

SELECT
    activity.*,
    users.plan
FROM activity
LEFT JOIN product.users AS users
    ON activity.user_id = users.user_id
`, got)
}

func TestProjectSQLStarterStyleFollowsTheProjectsKeywordCase(t *testing.T) {
	t.Parallel()
	sqlAsset := func(body string) *pipeline.Asset {
		return &pipeline.Asset{Type: "duckdb.sql", ExecutableFile: pipeline.ExecutableFile{Content: body}}
	}
	lower := &pipeline.Pipeline{Assets: []*pipeline.Asset{
		sqlAsset("-- SELECT FROM in a comment does not count\nselect a from b where c\n"),
		sqlAsset("select * from x"),
	}}
	assert.True(t, projectSQLStarterStyle(lower, "duckdb.sql").Lowercase)
	assert.Equal(t, "duckdb", projectSQLStarterStyle(lower, "duckdb.sql").Dialect)

	upper := &pipeline.Pipeline{Assets: []*pipeline.Asset{sqlAsset("SELECT a FROM b")}}
	assert.False(t, projectSQLStarterStyle(upper, "duckdb.sql").Lowercase)
	assert.False(t, projectSQLStarterStyle(&pipeline.Pipeline{}, "duckdb.sql").Lowercase)
}

const starterTestUsersSQL = `/* @bruin
type: duckdb.sql
columns:
  - name: user_id
    type: integer
    primary_key: true
  - name: plan
    type: varchar
@bruin */

SELECT 1 AS user_id, 'free' AS plan
`

const starterTestEventsSQL = `/* @bruin
type: duckdb.sql
columns:
  - name: event_id
    type: integer
  - name: user_id
    type: integer
@bruin */

SELECT 1 AS event_id, 1 AS user_id
`

func TestAssetServiceCreateDownstreamSQLStartsFromKnownColumns(t *testing.T) {
	t.Parallel()
	service, pipelineRoot := newAssetCreationProfileTestService(t, "name: analytics\ndefault_connections:\n  duckdb: warehouse\n", assetCreationProfileTestConfig)
	assetsDir := filepath.Join(pipelineRoot, "assets", "analytics")
	require.NoError(t, os.WriteFile(filepath.Join(assetsDir, "users.sql"), []byte(starterTestUsersSQL), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsDir, "events.sql"), []byte(starterTestEventsSQL), 0o644))

	_, apiErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:          "analytics.users_daily",
		Kind:          assetCreationKindSQL,
		Connection:    "warehouse",
		Environment:   "dev",
		SourceAssetID: EncodeID("analytics/assets/analytics/users.sql"),
	})
	require.Nil(t, apiErr)
	content, err := os.ReadFile(filepath.Join(assetsDir, "users_daily.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "@bruin */\n\nSELECT\n    user_id,\n    plan\nFROM analytics.users\n")
	assert.Contains(t, string(content), "depends:\n  - analytics.users")

	_, apiErr = service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:           "analytics.events_enriched",
		Kind:           assetCreationKindSQL,
		Connection:     "warehouse",
		Environment:    "dev",
		SourceAssetID:  EncodeID("analytics/assets/analytics/events.sql"),
		SourceAssetIDs: []string{EncodeID("analytics/assets/analytics/users.sql")},
	})
	require.Nil(t, apiErr)
	content, err = os.ReadFile(filepath.Join(assetsDir, "events_enriched.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(content), `SELECT
    events.event_id,
    events.user_id,
    users.plan
FROM analytics.events AS events
LEFT JOIN analytics.users AS users
    ON events.user_id = users.user_id
`)
	assert.Contains(t, string(content), "  - analytics.events\n")
	assert.Contains(t, string(content), "  - analytics.users\n")

	_, apiErr = service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:           "analytics.events_py",
		Kind:           assetCreationKindPython,
		Environment:    "dev",
		SourceAssetID:  EncodeID("analytics/assets/analytics/events.sql"),
		SourceAssetIDs: []string{EncodeID("analytics/assets/analytics/users.sql")},
	})
	require.NotNil(t, apiErr)
	assert.Equal(t, "invalid_source_assets", apiErr.Code)
}

func TestAssetServiceJoinUpstreamRewritesTheQuery(t *testing.T) {
	t.Parallel()
	service, pipelineRoot := newAssetCreationProfileTestService(t, "name: analytics\ndefault_connections:\n  duckdb: warehouse\n", assetCreationProfileTestConfig)
	assetsDir := filepath.Join(pipelineRoot, "assets", "analytics")
	require.NoError(t, os.WriteFile(filepath.Join(assetsDir, "users.sql"), []byte(starterTestUsersSQL), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsDir, "events.sql"), []byte(starterTestEventsSQL), 0o644))
	usersID := EncodeID("analytics/assets/analytics/users.sql")
	eventsID := EncodeID("analytics/assets/analytics/events.sql")

	_, apiErr := service.JoinUpstream(context.Background(), eventsID, JoinUpstreamRequest{SourceAssetID: usersID})
	require.Nil(t, apiErr)
	content, err := os.ReadFile(filepath.Join(assetsDir, "events.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(content), `WITH events AS (
    SELECT 1 AS event_id, 1 AS user_id
)

SELECT
    events.*,
    users.plan
FROM events
LEFT JOIN analytics.users AS users
    ON events.user_id = users.user_id
`)
	assert.Contains(t, string(content), "depends:\n  - analytics.users")

	_, apiErr = service.JoinUpstream(context.Background(), eventsID, JoinUpstreamRequest{SourceAssetID: usersID})
	require.NotNil(t, apiErr)
	assert.Equal(t, "already_upstream", apiErr.Code)
	_, apiErr = service.JoinUpstream(context.Background(), eventsID, JoinUpstreamRequest{SourceAssetID: eventsID})
	require.NotNil(t, apiErr)
	assert.Equal(t, "invalid_source_asset_id", apiErr.Code)
}
