package service

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	bruingit "github.com/bruin-data/bruin/pkg/git"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/afero"

	"renart/internal/web/identity"
	"renart/internal/web/scheduler"
)

// ProjectTemplateInfo describes one create-project template for the
// onboarding UI. ID is the value the create endpoint accepts as `template`.
type ProjectTemplateInfo struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Category     string   `json:"category"`
	Offline      bool     `json:"offline"`
	PipelineName string   `json:"pipeline_name"`
	AssetNames   []string `json:"asset_names"`
	Features     []string `json:"features"`
}

type templateEnvironmentSchedule struct {
	duckdbFile  string
	declaration scheduler.ScheduleDeclaration
}

type projectTemplate struct {
	info                 ProjectTemplateInfo
	duckdbFile           string
	files                func() map[string]string
	environmentSchedules func(primaryEnvironment string) map[string]templateEnvironmentSchedule
}

const (
	ProjectTemplateEmpty          = "empty"
	ProjectTemplateBare           = "bare"
	ProjectTemplateChessDemo      = "demo:chess"
	ProjectTemplateRetailDemo     = "demo:retail"
	ProjectTemplateProductDemo    = PipelineTemplateProductDemo
	ProjectTemplateOperationsDemo = PipelineTemplateOperationsDemo
	ProjectTemplateEarthquakeDemo = PipelineTemplateEarthquakeDemo
	ProjectTemplatePythonDemo     = PipelineTemplatePythonDemo
	ProjectTemplateJinjaDemo      = PipelineTemplateJinjaDemo
)

func projectTemplates() []projectTemplate {
	return []projectTemplate{
		projectTemplateFromPipelineStarter(ProjectTemplateProductDemo),
		projectTemplateFromPipelineStarter(ProjectTemplateOperationsDemo),
		projectTemplateFromPipelineStarter(ProjectTemplateEarthquakeDemo),
		projectTemplateFromPipelineStarter(ProjectTemplatePythonDemo),
		projectTemplateFromPipelineStarter(ProjectTemplateJinjaDemo),
		{
			info: ProjectTemplateInfo{
				ID:           ProjectTemplateChessDemo,
				Title:        "Chess performance",
				Description:  "Loads profiles and January 2024 games for popular players, then compares their results, ratings, and opening choices in DuckDB.",
				Category:     PipelineTemplateCategoryExplore,
				Offline:      false,
				PipelineName: "chess",
				AssetNames: []string{
					"chess.players",
					"chess.games",
					"chess.game_results",
					"chess.player_performance",
					"chess.opening_repertoire",
				},
				Features: []string{"HTTP API", "Iteration", "SQL lineage", "Charts"},
			},
			// Keep the DuckDB catalog name distinct from the asset schema. A
			// database named chess.duckdb makes chess.games ambiguous to DuckDB
			// because "chess" can refer to either the catalog or the schema.
			duckdbFile: "chess_playground.duckdb",
			files: func() map[string]string {
				return map[string]string{
					"pipeline.yml":                        quickstartPipelineYAML("chess", "duckdb-default"),
					"assets/chess/players.asset.yml":      chessPlayersAPIYAML(),
					"assets/chess/games.asset.yml":        chessGamesAPIYAML(),
					"assets/chess/game_results.sql":       chessGameResultsSQL("chess"),
					"assets/chess/player_performance.sql": chessPlayerPerformanceSQL("chess"),
					"assets/chess/opening_repertoire.sql": chessOpeningRepertoireSQL("chess"),
				}
			},
		},
		{
			info: ProjectTemplateInfo{
				ID:           ProjectTemplateRetailDemo,
				Title:        "Retail analytics",
				Description:  "A small retail warehouse built from bundled CSV seed data — every asset runs fully offline against local DuckDB.",
				Category:     PipelineTemplateCategoryAnalytics,
				Offline:      true,
				PipelineName: "retail",
				AssetNames:   []string{"raw.customers", "raw.orders", "analytics.customer_orders", "analytics.daily_revenue"},
				Features:     []string{"Seed files", "Schema metadata", "SQL lineage", "Tables"},
			},
			duckdbFile: "retail.duckdb",
			files: func() map[string]string {
				return map[string]string{
					"pipeline.yml":                         retailPipelineYAML(),
					"assets/raw/customers.asset.yml":       retailRawCustomersSeedYAML(),
					"assets/raw/customers.csv":             retailRawCustomersCSV(),
					"assets/raw/orders.asset.yml":          retailRawOrdersSeedYAML(),
					"assets/raw/orders.csv":                retailRawOrdersCSV(),
					"assets/analytics/customer_orders.sql": retailCustomerOrdersSQL(),
					"assets/analytics/daily_revenue.sql":   retailDailyRevenueSQL(),
				}
			},
		},
		{
			info: ProjectTemplateInfo{
				ID:           ProjectTemplateEmpty,
				Title:        "Empty project",
				Description:  "A minimal pipeline with one example SQL asset against local DuckDB.",
				Category:     PipelineTemplateCategoryStart,
				Offline:      true,
				PipelineName: "analytics",
				AssetNames:   []string{"example.hello"},
				Features:     []string{"Example SQL"},
			},
			duckdbFile: "analytics.duckdb",
			files: func() map[string]string {
				return map[string]string{
					"pipeline.yml":       emptyPipelineYAML(),
					"assets/example.sql": emptyExampleSQL(),
				}
			},
		},
		{
			// The import flow's shell: project identity, config, and git repo
			// with no pipeline — the table import creates the pipeline itself.
			info: ProjectTemplateInfo{
				ID:          ProjectTemplateBare,
				Title:       "Bare project",
				Description: "Project scaffolding only; a pipeline is added by the import step.",
				Category:    PipelineTemplateCategoryStart,
				Offline:     true,
				Features:    []string{"Project files only"},
			},
			duckdbFile: "local.duckdb",
			files:      func() map[string]string { return map[string]string{} },
		},
	}
}

// projectTemplateFromPipelineStarter promotes a backend-owned pipeline starter
// into first-run onboarding without copying its files or catalog text into a
// second implementation. Retail and Chess keep their older project-specific
// wrappers because their pipeline variants already source files from those
// wrappers.
func projectTemplateFromPipelineStarter(id string) projectTemplate {
	starter, ok := pipelineTemplateByID(id)
	if !ok {
		panic(fmt.Sprintf("project template references unknown pipeline starter %q", id))
	}
	template := projectTemplate{
		info: ProjectTemplateInfo{
			ID:           starter.info.ID,
			Title:        starter.info.Title,
			Description:  starter.info.Description,
			Category:     starter.info.Category,
			Offline:      starter.info.Offline,
			PipelineName: starter.info.SuggestedPath,
			AssetNames:   starter.info.AssetNames,
			Features:     starter.info.Features,
		},
		duckdbFile:           starter.duckdbFile,
		environmentSchedules: starter.environmentSchedules,
		files: func() map[string]string {
			return starter.files(starter.info.SuggestedPath)
		},
	}
	return template
}

type ProjectTemplatesResponse struct {
	Status    string                `json:"status"`
	Templates []ProjectTemplateInfo `json:"templates"`
}

// ProjectTemplates lists the templates the create-project endpoint accepts.
func ProjectTemplates() []ProjectTemplateInfo {
	templates := projectTemplates()
	infos := make([]ProjectTemplateInfo, 0, len(templates))
	for _, tpl := range templates {
		infos = append(infos, tpl.info)
	}
	return infos
}

func projectTemplateByID(id string) (projectTemplate, bool) {
	for _, tpl := range projectTemplates() {
		if tpl.info.ID == strings.TrimSpace(id) {
			return tpl, true
		}
	}
	return projectTemplate{}, false
}

type ScaffoldProjectRequest struct {
	// TargetDir is the absolute project root; created when missing.
	TargetDir string
	// ConfigPath is the .bruin.yml to write connections into; defaults to
	// <TargetDir>/.bruin.yml.
	ConfigPath string
	// Template is one of the ProjectTemplates IDs.
	Template string
	// NewRepository forces `git init` at TargetDir even when an enclosing
	// repository exists (used when creating a brand-new project directory).
	NewRepository bool
}

type ScaffoldProjectResult struct {
	PipelinePath   string   `json:"pipeline_path"`
	PipelineID     string   `json:"pipeline_id"`
	Files          []string `json:"files"`
	GitInitialized bool     `json:"git_initialized"`
}

// ScaffoldProject writes a template's project files into TargetDir: the
// pipeline directory, a DuckDB connection in the workspace config, the
// project identity, and — when no git repository exists yet (or
// NewRepository is set) — `git init` + .gitignore + an initial commit.
func ScaffoldProject(req ScaffoldProjectRequest) (ScaffoldProjectResult, error) {
	tpl, ok := projectTemplateByID(req.Template)
	if !ok {
		return ScaffoldProjectResult{}, fmt.Errorf("unknown project template %q", req.Template)
	}

	target, err := filepath.Abs(strings.TrimSpace(req.TargetDir))
	if err != nil {
		return ScaffoldProjectResult{}, err
	}
	if target == "" || target == string(filepath.Separator) {
		return ScaffoldProjectResult{}, fmt.Errorf("invalid project directory %q", req.TargetDir)
	}

	fs := afero.NewOsFs()
	if err := fs.MkdirAll(target, 0o755); err != nil {
		return ScaffoldProjectResult{}, err
	}

	pipelineDir := filepath.Join(target, tpl.info.PipelineName)
	if tpl.info.PipelineName != "" {
		if exists, statErr := afero.Exists(fs, pipelineDir); statErr != nil {
			return ScaffoldProjectResult{}, statErr
		} else if exists {
			return ScaffoldProjectResult{}, fmt.Errorf("directory %q already exists in the project", tpl.info.PipelineName)
		}
	}

	// Repository first, so the config below lands relative to the right root.
	repo, initialized, err := openOrInitRepository(target, req.NewRepository)
	if err != nil {
		return ScaffoldProjectResult{}, err
	}

	// The server may have seeded a partial .gitignore already (log sinks
	// append their own patterns), so ensure every default pattern instead of
	// writing the file only when missing.
	created := []string{".gitignore"}
	for _, pattern := range strings.Split(strings.TrimSpace(defaultGitignoreContents), "\n") {
		if err := bruingit.EnsureGivenPatternIsInGitignore(fs, target, pattern); err != nil {
			return ScaffoldProjectResult{}, err
		}
	}

	configPath := strings.TrimSpace(req.ConfigPath)
	if configPath == "" {
		configPath = filepath.Join(target, ".bruin.yml")
	}
	configExisted, _ := afero.Exists(fs, configPath)
	primaryEnvironment, err := ensureScaffoldDuckDBConnectionWithEnvironment(
		target,
		configPath,
		"duckdb-files/"+tpl.duckdbFile,
	)
	if err != nil {
		return ScaffoldProjectResult{}, err
	}
	var environmentSchedules map[string]templateEnvironmentSchedule
	if tpl.environmentSchedules != nil {
		environmentSchedules = tpl.environmentSchedules(primaryEnvironment)
		environments := make([]string, 0, len(environmentSchedules))
		for environment := range environmentSchedules {
			environments = append(environments, environment)
		}
		sort.Strings(environments)
		for _, environment := range environments {
			if environment == primaryEnvironment {
				continue
			}
			if err := ensureScaffoldDuckDBConnectionInEnvironment(
				target,
				configPath,
				environment,
				"duckdb-files/"+environmentSchedules[environment].duckdbFile,
			); err != nil {
				return ScaffoldProjectResult{}, err
			}
		}
	}
	if !configExisted {
		if rel, relErr := filepath.Rel(target, configPath); relErr == nil && !strings.HasPrefix(rel, "..") {
			created = append(created, filepath.ToSlash(rel))
		}
	}
	if err := fs.MkdirAll(filepath.Join(target, "duckdb-files"), 0o755); err != nil {
		return ScaffoldProjectResult{}, err
	}

	for relPath, content := range tpl.files() {
		absPath := filepath.Join(pipelineDir, filepath.FromSlash(relPath))
		if err := fs.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return ScaffoldProjectResult{}, err
		}
		if err := afero.WriteFile(fs, absPath, []byte(content), 0o644); err != nil {
			return ScaffoldProjectResult{}, err
		}
		created = append(created, filepath.ToSlash(filepath.Join(tpl.info.PipelineName, relPath)))
	}

	var pipelineUUID string
	if tpl.info.PipelineName != "" {
		pipelineUUID, _, err = identity.EnsurePipelineID(
			fs,
			filepath.Join(pipelineDir, "pipeline.yml"),
		)
		if err != nil {
			return ScaffoldProjectResult{}, err
		}
	}

	if _, err := identity.EnsureProject(fs, filepath.Join(target, ".renart", "project.yml"), filepath.Base(target)); err != nil {
		return ScaffoldProjectResult{}, err
	}
	created = append(created, ".renart/project.yml")
	if len(environmentSchedules) > 0 {
		store := scheduler.NewScheduleDeclarationStore(filepath.Join(target, ".renart", "schedules.yml"))
		environments := make([]string, 0, len(environmentSchedules))
		for environment := range environmentSchedules {
			environments = append(environments, environment)
		}
		sort.Strings(environments)
		for _, environment := range environments {
			if err := store.Set(
				pipelineUUID,
				environment,
				environmentSchedules[environment].declaration,
			); err != nil {
				return ScaffoldProjectResult{}, err
			}
		}
		created = append(created, ".renart/schedules.yml")
	}
	sort.Strings(created)

	if initialized {
		if err := commitScaffold(repo, created); err != nil {
			return ScaffoldProjectResult{}, err
		}
	}

	return ScaffoldProjectResult{
		PipelinePath:   tpl.info.PipelineName,
		PipelineID:     EncodeID(tpl.info.PipelineName),
		Files:          created,
		GitInitialized: initialized,
	}, nil
}

// openOrInitRepository returns the repository governing target, initializing
// one at target when none exists (or when forceInit asks for a fresh one).
func openOrInitRepository(target string, forceInit bool) (*gogit.Repository, bool, error) {
	if !forceInit {
		repo, err := gogit.PlainOpenWithOptions(target, &gogit.PlainOpenOptions{DetectDotGit: true})
		if err == nil {
			return repo, false, nil
		}
	}

	repo, err := gogit.PlainInitWithOptions(target, &gogit.PlainInitOptions{
		InitOptions: gogit.InitOptions{
			DefaultBranch: plumbing.NewBranchReferenceName("main"),
		},
	})
	if err != nil {
		// A forced init inside an existing repository degrades to opening it.
		repo, openErr := gogit.PlainOpenWithOptions(target, &gogit.PlainOpenOptions{DetectDotGit: true})
		if openErr != nil {
			return nil, false, err
		}
		return repo, false, nil
	}
	return repo, true, nil
}

func commitScaffold(repo *gogit.Repository, paths []string) error {
	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := worktree.AddWithOptions(&gogit.AddOptions{Path: filepath.ToSlash(path), SkipStatus: true}); err != nil {
			return err
		}
	}
	_, err = worktree.Commit("Initialize renart project", &gogit.CommitOptions{Author: commitAuthor(repo)})
	return err
}

// ensureScaffoldDuckDBConnection makes sure the workspace config has a
// default environment with a `duckdb-default` connection, without touching
// connections a user already configured.
func ensureScaffoldDuckDBConnection(workspaceRoot, configPath, databasePath string) error {
	_, err := ensureScaffoldDuckDBConnectionWithEnvironment(workspaceRoot, configPath, databasePath)
	return err
}

func ensureScaffoldDuckDBConnectionWithEnvironment(
	workspaceRoot,
	configPath,
	databasePath string,
) (string, error) {
	configService := NewConfigService(workspaceRoot, configPath)
	cfg, _, err := configService.LoadForEditing()
	if err != nil {
		return "", err
	}

	environmentName := strings.TrimSpace(cfg.DefaultEnvironmentName)
	if environmentName == "" {
		environmentName = "default"
	}
	if _, exists := cfg.Environments[environmentName]; !exists {
		if err := cfg.AddEnvironment(environmentName, ""); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(cfg.DefaultEnvironmentName) == "" {
		cfg.DefaultEnvironmentName = environmentName
	}
	if strings.TrimSpace(cfg.SelectedEnvironmentName) == "" {
		cfg.SelectedEnvironmentName = environmentName
	}

	if !workspaceConfigHasConnection(cfg, environmentName, "duckdb-default") {
		if err := cfg.AddConnection(environmentName, "duckdb-default", "duckdb", map[string]any{"path": databasePath}); err != nil {
			return "", err
		}
	}

	if _, err = configService.Persist(cfg); err != nil {
		return "", err
	}
	return environmentName, nil
}

func ensureScaffoldDuckDBConnectionInEnvironment(
	workspaceRoot,
	configPath,
	environmentName,
	databasePath string,
) error {
	configService := NewConfigService(workspaceRoot, configPath)
	cfg, _, err := configService.LoadForEditing()
	if err != nil {
		return err
	}
	environmentName = strings.TrimSpace(environmentName)
	if environmentName == "" {
		return fmt.Errorf("scaffold environment is required")
	}
	if _, exists := cfg.Environments[environmentName]; !exists {
		if err := cfg.AddEnvironment(environmentName, ""); err != nil {
			return err
		}
	}
	if !workspaceConfigHasConnection(cfg, environmentName, "duckdb-default") {
		if err := cfg.AddConnection(
			environmentName,
			"duckdb-default",
			"duckdb",
			map[string]any{"path": databasePath},
		); err != nil {
			return err
		}
	}
	_, err = configService.Persist(cfg)
	return err
}

func workspaceConfigHasConnection(cfg *config.Config, environmentName, connectionName string) bool {
	env, exists := cfg.Environments[environmentName]
	if !exists || env.Connections == nil {
		return false
	}
	for name := range env.Connections.ConnectionsSummaryList() {
		if name == connectionName {
			return true
		}
	}
	return false
}

func retailPipelineYAML() string {
	return `name: retail
schedule: daily
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`
}

func retailRawCustomersSeedYAML() string {
	return `name: raw.customers
type: duckdb.seed
description: Bundled customer records for the offline retail demo.
meta:
  renart_seed_file: customers.csv
parameters:
  path: ./customers.csv
  file_type: csv
  enforce_schema: true
columns:
  - name: customer_id
    type: integer
  - name: customer_name
    type: string
  - name: city
    type: string
  - name: signed_up_at
    type: date
`
}

func retailRawCustomersCSV() string {
	return `customer_id,customer_name,city,signed_up_at
1,Ada Lovelace,London,2023-10-04
2,Grace Hopper,New York,2023-10-19
3,Alan Turing,Manchester,2023-11-02
4,Katherine Johnson,Hampton,2023-11-15
5,Margaret Hamilton,Boston,2023-11-28
6,Claude Shannon,Ann Arbor,2023-12-06
7,Edsger Dijkstra,Rotterdam,2023-12-14
8,Barbara Liskov,Los Angeles,2023-12-22
9,Donald Knuth,Milwaukee,2024-01-03
10,Radia Perlman,Portsmouth,2024-01-11
11,John von Neumann,Budapest,2024-01-18
12,Annie Easley,Birmingham,2024-01-25
`
}

func retailRawOrdersSeedYAML() string {
	return `name: raw.orders
type: duckdb.seed
description: Bundled deterministic orders for the offline retail demo.
meta:
  renart_seed_file: orders.csv
parameters:
  path: ./orders.csv
  file_type: csv
  enforce_schema: true
columns:
  - name: order_id
    type: integer
  - name: customer_id
    type: integer
  - name: ordered_at
    type: date
  - name: order_total
    type: decimal(10,2)
  - name: status
    type: string
`
}

func retailRawOrdersCSV() string {
	var result strings.Builder
	result.Grow(20 << 10)
	result.WriteString("order_id,customer_id,ordered_at,order_total,status\n")
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	for sequence := 1; sequence <= 480; sequence++ {
		customerID := 1 + (sequence*7)%12
		orderedAt := start.AddDate(0, 0, (sequence*13)%120)
		totalCents := 450 + (sequence*37)%9000
		status := "completed"
		if (sequence*11)%10 == 0 {
			status = "returned"
		}
		_, _ = fmt.Fprintf(
			&result,
			"%d,%d,%s,%d.%02d,%s\n",
			sequence,
			customerID,
			orderedAt.Format("2006-01-02"),
			totalCents/100,
			totalCents%100,
			status,
		)
	}
	return result.String()
}

func retailCustomerOrdersSQL() string {
	return `/* @bruin
name: analytics.customer_orders
type: duckdb.sql
materialization:
  type: table
depends:
  - raw.customers
  - raw.orders
columns:
  - name: customer_id
    type: integer
    description: Unique customer identifier
    primary_key: true
  - name: lifetime_value
    type: decimal
    description: Total revenue from the customer's completed orders
  - name: customer_name
    type: varchar
  - name: city
    type: varchar
  - name: order_count
    type: bigint
  - name: last_ordered_at
    type: date
@bruin */

SELECT
    customers.customer_id,
    customers.customer_name,
    customers.city,
    count(orders.order_id) AS order_count,
    round(coalesce(sum(orders.order_total) FILTER (WHERE orders.status = 'completed'), 0), 2) AS lifetime_value,
    max(orders.ordered_at) AS last_ordered_at
FROM raw.customers AS customers
LEFT JOIN raw.orders AS orders
    ON orders.customer_id = customers.customer_id
GROUP BY customers.customer_id, customers.customer_name, customers.city
ORDER BY lifetime_value DESC
`
}

func retailDailyRevenueSQL() string {
	return `/* @bruin
name: analytics.daily_revenue
type: duckdb.sql
materialization:
  type: table
depends:
  - raw.orders
@bruin */

SELECT
    ordered_at AS order_date,
    count(*) AS orders,
    round(sum(order_total), 2) AS revenue
FROM raw.orders
WHERE status = 'completed'
GROUP BY ordered_at
ORDER BY ordered_at
`
}

func emptyPipelineYAML() string {
	return `name: analytics
schedule: daily
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`
}

func emptyExampleSQL() string {
	return `/* @bruin
name: example.hello
type: duckdb.sql
materialization:
  type: table
@bruin */

SELECT
    42 AS answer,
    'hello from renart' AS greeting
`
}
