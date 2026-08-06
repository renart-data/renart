package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
	"renart/internal/web/identity"
	webmodel "renart/internal/web/model"
	"renart/internal/web/scheduler"
)

type PipelineService struct {
	workspaceRoot       string
	remoteCatalog       RemoteCatalogProvider
	selectedEnvironment func() string
}

var ErrInvalidPipelineDefaultConnection = errors.New("invalid pipeline default connection")

func NewPipelineService(workspaceRoot string) *PipelineService {
	return &PipelineService{workspaceRoot: workspaceRoot}
}

// SetRemoteCatalogProvider wires optional, process-local catalog evidence into
// interactive type-checking. The provider snapshot is read without I/O; CLI
// and execution-planning type checks deliberately remain provider-free.
func (s *PipelineService) SetRemoteCatalogProvider(provider RemoteCatalogProvider, selectedEnvironment func() string) {
	s.remoteCatalog = provider
	s.selectedEnvironment = selectedEnvironment
}

func (s *PipelineService) resolver() *WorkspaceResolver {
	return NewWorkspaceResolver(s.workspaceRoot, func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		return s.newPipelineBuilder().CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate(), pipeline.WithOnlyPipeline())
	})
}

func (s *PipelineService) Create(ctx context.Context, relPath, name, content, templateID string) (string, error) {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		templateID = PipelineTemplateBlank
	}
	template, ok := pipelineTemplateByID(templateID)
	if !ok {
		return "", fmt.Errorf("unknown pipeline template %q", templateID)
	}
	if templateID != PipelineTemplateBlank && strings.TrimSpace(content) != "" {
		return "", fmt.Errorf("pipeline template %q cannot be combined with custom content", templateID)
	}

	absPath, err := SafeJoin(s.workspaceRoot, relPath)
	if err != nil {
		return "", err
	}
	fs := afero.NewOsFs()
	if exists, existsErr := afero.Exists(fs, absPath); existsErr != nil {
		return "", existsErr
	} else if exists {
		return "", fmt.Errorf("pipeline directory %q already exists", filepath.ToSlash(relPath))
	}

	if strings.TrimSpace(name) == "" {
		name = filepath.Base(absPath)
	}
	files := template.files(name)
	if strings.TrimSpace(content) != "" {
		files["pipeline.yml"] = content
	}
	if strings.TrimSpace(files["pipeline.yml"]) == "" {
		files["pipeline.yml"] = basicPipelineYAML(name)
	}
	var pipelineDocument yaml.Node
	if err := yaml.Unmarshal([]byte(files["pipeline.yml"]), &pipelineDocument); err != nil {
		return "", fmt.Errorf("invalid pipeline config: %w", err)
	}
	pipelineRoot := yamlDocumentMapping(&pipelineDocument)
	if pipelineRoot == nil {
		return "", fmt.Errorf("pipeline config must be a YAML mapping")
	}

	var environmentSchedules map[string]templateEnvironmentSchedule
	if template.duckdbFile != "" {
		primaryEnvironment, connectionErr := ensureScaffoldDuckDBConnectionWithEnvironment(
			s.workspaceRoot,
			filepath.Join(s.workspaceRoot, ".bruin.yml"),
			"duckdb-files/"+template.duckdbFile,
		)
		if connectionErr != nil {
			return "", connectionErr
		}
		if template.environmentSchedules != nil {
			environmentSchedules = template.environmentSchedules(primaryEnvironment)
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
					s.workspaceRoot,
					filepath.Join(s.workspaceRoot, ".bruin.yml"),
					environment,
					"duckdb-files/"+environmentSchedules[environment].duckdbFile,
				); err != nil {
					return "", err
				}
			}
		}
		if err := fs.MkdirAll(filepath.Join(s.workspaceRoot, "duckdb-files"), 0o755); err != nil {
			return "", err
		}
	}

	if err := fs.MkdirAll(absPath, 0o755); err != nil {
		return "", err
	}
	created := false
	defer func() {
		if !created {
			_ = fs.RemoveAll(absPath)
		}
	}()

	paths := make([]string, 0, len(files))
	for relFile := range files {
		paths = append(paths, relFile)
	}
	sort.Strings(paths)
	for _, relFile := range paths {
		filePath := filepath.Join(absPath, filepath.FromSlash(relFile))
		if err := fs.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			return "", err
		}
		if err := afero.WriteFile(fs, filePath, []byte(files[relFile]), 0o644); err != nil {
			return "", err
		}
	}
	pipelineYmlPath := filepath.Join(absPath, "pipeline.yml")
	if err := fs.MkdirAll(filepath.Join(absPath, "assets"), 0o755); err != nil {
		return "", err
	}
	pipelineUUID, _, err := identity.EnsurePipelineID(fs, pipelineYmlPath)
	if err != nil {
		return "", err
	}
	if len(environmentSchedules) > 0 {
		store := scheduler.NewScheduleDeclarationStore(
			filepath.Join(s.workspaceRoot, ".renart", "schedules.yml"),
		)
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
				return "", err
			}
		}
	}

	created = true
	return filepath.ToSlash(relPath), nil
}

func (s *PipelineService) Update(ctx context.Context, pipelineID, name, content string) (string, error) {
	relPath, absPath, err := s.resolver().DecodePipelineID(pipelineID)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(name) != "" && strings.TrimSpace(content) == "" {
		builder := s.newPipelineBuilder()
		parsed, err := builder.CreatePipelineFromPath(ctx, absPath, pipeline.WithMutate(), pipeline.WithOnlyPipeline())
		if err != nil {
			return "", err
		}

		parsed.Name = strings.TrimSpace(name)
		parsed.DefinitionFile.Path = filepath.Join(absPath, "pipeline.yml")

		if err := parsed.Persist(afero.NewOsFs()); err != nil {
			return "", err
		}

		return filepath.ToSlash(relPath), nil
	}

	if err := afero.WriteFile(afero.NewOsFs(), filepath.Join(absPath, "pipeline.yml"), []byte(content), 0o644); err != nil {
		return "", err
	}

	return filepath.ToSlash(relPath), nil
}

func (s *PipelineService) GetConfig(ctx context.Context, pipelineID string) (*webmodel.PipelineConfigResponse, error) {
	parsed, relPath, err := s.loadPipeline(ctx, pipelineID)
	if err != nil {
		return nil, err
	}

	resp := buildPipelineConfigResponse(pipelineID, filepath.ToSlash(relPath), parsed)
	resp.InferredDefaultConnections, resp.ReferencedConnections = s.pipelineConnectionSummaries(ctx, relPath)
	resp.Status = "ok"
	return resp, nil
}

func (s *PipelineService) GetSchedule(ctx context.Context, pipelineID string) (scheduler.PipelineSchedule, error) {
	parsed, relPath, err := s.loadPipeline(ctx, pipelineID)
	if err != nil {
		return scheduler.PipelineSchedule{}, err
	}
	timezone, catchup := s.readScheduleExtras(relPath)
	schedule := strings.TrimSpace(string(parsed.Schedule))
	return scheduler.PipelineSchedule{
		PipelineID:   pipelineID,
		PipelineUUID: strings.TrimSpace(parsed.LegacyID),
		PipelineName: parsed.Name,
		PipelinePath: filepath.ToSlash(relPath),
		Schedule:     schedule,
		Timezone:     timezone,
		Catchup:      catchup,
		Enabled:      schedule != "",
	}, nil
}

func (s *PipelineService) ListSchedules(ctx context.Context) ([]scheduler.PipelineSchedule, error) {
	state, err := NewWorkspaceService(s.workspaceRoot, "").ComputeState(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]scheduler.PipelineSchedule, 0, len(state.Pipelines))
	for _, item := range state.Pipelines {
		pipelineSchedule, err := s.GetSchedule(ctx, item.ID)
		if err != nil {
			continue
		}
		items = append(items, pipelineSchedule)
	}
	return items, nil
}

func (s *PipelineService) UpdateSchedule(ctx context.Context, pipelineID string, req scheduler.UpdateScheduleRequest) (string, scheduler.PipelineSchedule, error) {
	_, relPath, err := s.loadPipeline(ctx, pipelineID)
	if err != nil {
		return "", scheduler.PipelineSchedule{}, err
	}
	absPath := filepath.Join(s.workspaceRoot, relPath, "pipeline.yml")
	bytes, err := afero.ReadFile(afero.NewOsFs(), absPath)
	if err != nil {
		return "", scheduler.PipelineSchedule{}, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(bytes, &doc); err != nil {
		return "", scheduler.PipelineSchedule{}, err
	}
	root := yamlDocumentMapping(&doc)
	if root == nil {
		return "", scheduler.PipelineSchedule{}, fmt.Errorf("pipeline config must be a YAML mapping")
	}
	schedule := strings.TrimSpace(req.Schedule)
	if schedule == "" {
		schedule = strings.TrimSpace(yamlScalar(root, "schedule"))
	}
	if req.Enabled && schedule == "" {
		return "", scheduler.PipelineSchedule{}, fmt.Errorf("schedule is required when scheduling is enabled")
	}
	if schedule != "" {
		setYAMLScalar(root, "schedule", schedule)
	}
	if strings.TrimSpace(req.Timezone) != "" {
		setYAMLScalar(root, "timezone", strings.TrimSpace(req.Timezone))
	}
	setYAMLBool(root, "catchup", req.Catchup)
	formatted, err := yaml.Marshal(&doc)
	if err != nil {
		return "", scheduler.PipelineSchedule{}, err
	}
	if err := afero.WriteFile(afero.NewOsFs(), absPath, formatted, 0o644); err != nil {
		return "", scheduler.PipelineSchedule{}, err
	}
	updated, err := s.GetSchedule(ctx, pipelineID)
	if err != nil {
		return "", scheduler.PipelineSchedule{}, err
	}
	return filepath.ToSlash(relPath), updated, nil
}

func (s *PipelineService) UpdateConfig(ctx context.Context, pipelineID string, req webmodel.UpdatePipelineConfigRequest) (string, *webmodel.PipelineConfigResponse, error) {
	parsed, relPath, err := s.loadPipeline(ctx, pipelineID)
	if err != nil {
		return "", nil, err
	}

	defaultConnections, err := normalizeDefaultConnections(req.DefaultConnections)
	if err != nil {
		return "", nil, err
	}
	if !maps.Equal(parsed.DefaultConnections, defaultConnections) {
		if err := s.validateConfiguredDefaultConnections(defaultConnections); err != nil {
			return "", nil, err
		}
	}

	parsed.Name = strings.TrimSpace(req.Name)
	parsed.Schedule = pipeline.Schedule(strings.TrimSpace(req.Schedule))
	parsed.StartDate = strings.TrimSpace(req.StartDate)
	parsed.Owner = strings.TrimSpace(req.Owner)
	parsed.Tags = normalizeStringArray(req.Tags)
	parsed.Domains = normalizeStringArray(req.Domains)
	parsed.DefaultConnections = defaultConnections
	previousCatchup := parsed.Catchup
	parsed.Catchup = pipeline.CatchupNone
	if req.Catchup {
		parsed.Catchup = pipeline.CatchupActive
		if previousCatchup == pipeline.CatchupAll {
			parsed.Catchup = pipeline.CatchupAll
		}
	}
	parsed.MetadataPush = pipeline.MetadataPush{BigQuery: req.MetadataPushBigQuery}
	parsed.Retries = nil
	if req.Retries != 0 {
		parsed.Retries = &req.Retries
	}
	parsed.Concurrency = max(req.Concurrency, 1)
	parsed.MaxActiveSteps = normalizeOptionalInt(req.MaxActiveSteps)
	parsed.Notifications = buildNotifications(req.NotificationsSlack, req.NotificationsTeams)
	defaultValues, err := buildDefaultValues(req.Defaults)
	if err != nil {
		return "", nil, err
	}
	parsed.DefaultValues = defaultValues

	variables, err := buildVariables(req.Variables)
	if err != nil {
		return "", nil, err
	}
	parsed.Variables = variables
	parsed.DefinitionFile.Path = filepath.Join(s.workspaceRoot, relPath, "pipeline.yml")

	if err := parsed.Persist(afero.NewOsFs()); err != nil {
		return "", nil, err
	}

	updated, _, err := s.loadPipeline(ctx, pipelineID)
	if err != nil {
		return "", nil, err
	}

	resp := buildPipelineConfigResponse(pipelineID, filepath.ToSlash(relPath), updated)
	resp.InferredDefaultConnections, resp.ReferencedConnections = s.pipelineConnectionSummaries(ctx, relPath)
	resp.Status = "ok"
	return filepath.ToSlash(relPath), resp, nil
}

func (s *PipelineService) Delete(pipelineID string) (string, error) {
	relPath, absPath, err := s.resolver().DecodePipelineID(pipelineID)
	if err != nil {
		return "", err
	}

	if err := afero.NewOsFs().RemoveAll(absPath); err != nil {
		return "", err
	}

	return filepath.ToSlash(relPath), nil
}

func (s *PipelineService) loadPipeline(ctx context.Context, pipelineID string) (*pipeline.Pipeline, string, error) {
	relPath, _, parsed, err := s.resolver().LoadPipelineByID(ctx, pipelineID)
	if err != nil {
		return nil, "", err
	}

	return parsed, relPath, nil
}

func (s *PipelineService) readScheduleExtras(relPath string) (string, bool) {
	absPath := filepath.Join(s.workspaceRoot, relPath, "pipeline.yml")
	bytes, err := afero.ReadFile(afero.NewOsFs(), absPath)
	if err != nil {
		return "UTC", false
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(bytes, &doc); err != nil {
		return "UTC", false
	}
	root := yamlDocumentMapping(&doc)
	if root == nil {
		return "UTC", false
	}
	timezone := strings.TrimSpace(yamlScalar(root, "timezone"))
	if timezone == "" {
		timezone = "UTC"
	}
	return timezone, yamlBool(root, "catchup")
}

func yamlDocumentMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	return doc
}

func yamlScalar(root *yaml.Node, key string) string {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1].Value
		}
	}
	return ""
}

func yamlBool(root *yaml.Node, key string) bool {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return strings.EqualFold(root.Content[i+1].Value, "true")
		}
	}
	return false
}

func setYAMLScalar(root *yaml.Node, key, value string) {
	setYAMLNode(root, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func setYAMLBool(root *yaml.Node, key string, value bool) {
	setYAMLNode(root, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value)})
}

func setYAMLNode(root *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1] = value
			return
		}
	}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func removeYAMLKey(root *yaml.Node, key string) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			return
		}
	}
}

func buildPipelineConfigResponse(pipelineID, relPath string, parsed *pipeline.Pipeline) *webmodel.PipelineConfigResponse {
	defaultConnections := make([]webmodel.PipelineConfigConnection, 0, len(parsed.DefaultConnections))
	for platform, name := range parsed.DefaultConnections {
		if strings.TrimSpace(platform) == "" || strings.TrimSpace(name) == "" {
			continue
		}
		defaultConnections = append(defaultConnections, webmodel.PipelineConfigConnection{
			Platform: platform,
			Name:     name,
		})
	}
	sort.Slice(defaultConnections, func(i, j int) bool {
		return defaultConnections[i].Platform < defaultConnections[j].Platform
	})

	variables := make([]webmodel.PipelineConfigVariable, 0, len(parsed.Variables))
	variableNames := make([]string, 0, len(parsed.Variables))
	for name := range parsed.Variables {
		variableNames = append(variableNames, name)
	}
	sort.Strings(variableNames)
	for _, name := range variableNames {
		definition := parsed.Variables[name]
		extra := make(map[string]any)
		var variableType string
		var defaultValue any
		var description string
		for key, value := range definition {
			switch key {
			case "type":
				variableType, _ = value.(string)
			case "default":
				defaultValue = value
			case "description":
				description, _ = value.(string)
			default:
				extra[key] = value
			}
		}
		if len(extra) == 0 {
			extra = nil
		}
		variables = append(variables, webmodel.PipelineConfigVariable{
			Name:         name,
			Type:         variableType,
			DefaultValue: defaultValue,
			Description:  description,
			Extra:        extra,
		})
	}

	defaults := webmodel.PipelineConfigDefaults{}
	if parsed.DefaultValues != nil {
		defaults.RerunCooldown = parsed.DefaultValues.RerunCooldown
		defaults.StartOffsetRaw = timeModifierString(parsed.DefaultValues.IntervalModifiers.Start)
		defaults.EndOffsetRaw = timeModifierString(parsed.DefaultValues.IntervalModifiers.End)
	}

	formattedContent, err := parsed.FormatContent()
	if err != nil {
		return nil
	}

	return &webmodel.PipelineConfigResponse{
		ID:                   pipelineID,
		Path:                 relPath,
		Name:                 parsed.Name,
		Schedule:             string(parsed.Schedule),
		StartDate:            parsed.StartDate,
		Owner:                parsed.Owner,
		Tags:                 []string(parsed.Tags),
		Domains:              []string(parsed.Domains),
		DefaultConnections:   defaultConnections,
		Catchup:              parsed.Catchup != pipeline.CatchupNone,
		MetadataPushBigQuery: parsed.MetadataPush.BigQuery,
		Retries:              optionalIntValue(parsed.Retries),
		Concurrency:          parsed.Concurrency,
		MaxActiveSteps:       parsed.MaxActiveSteps,
		NotificationsSlack:   buildSlackNotificationResponse(parsed.Notifications),
		NotificationsTeams:   buildTeamsNotificationResponse(parsed.Notifications),
		Defaults:             defaults,
		Variables:            variables,
		YAML:                 string(formattedContent),
	}
}

func buildSlackNotificationResponse(notifications pipeline.Notifications) webmodel.PipelineConfigNotification {
	if len(notifications.Slack) == 0 {
		return webmodel.PipelineConfigNotification{Success: true, Failure: true}
	}
	item := notifications.Slack[0]
	return webmodel.PipelineConfigNotification{
		Enabled: true,
		Channel: item.Channel,
		Success: item.Success.Bool(),
		Failure: item.Failure.Bool(),
	}
}

func buildTeamsNotificationResponse(notifications pipeline.Notifications) webmodel.PipelineConfigNotification {
	if len(notifications.MSTeams) == 0 {
		return webmodel.PipelineConfigNotification{Success: true, Failure: true}
	}
	item := notifications.MSTeams[0]
	return webmodel.PipelineConfigNotification{
		Enabled:    true,
		Connection: item.Connection,
		Success:    item.Success.Bool(),
		Failure:    item.Failure.Bool(),
	}
}

func normalizeStringArray(values []string) pipeline.EmptyStringArray {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeDefaultConnections(
	input []webmodel.PipelineConfigConnection,
) (pipeline.EmptyStringMap, error) {
	result := make(map[string]string)
	for _, item := range input {
		platform := strings.TrimSpace(item.Platform)
		name := strings.TrimSpace(item.Name)
		if platform == "" || name == "" {
			return nil, fmt.Errorf(
				"%w: platform and connection are both required",
				ErrInvalidPipelineDefaultConnection,
			)
		}
		if _, exists := result[platform]; exists {
			return nil, fmt.Errorf(
				"%w: platform %q is configured more than once",
				ErrInvalidPipelineDefaultConnection,
				platform,
			)
		}
		result[platform] = name
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func (s *PipelineService) validateConfiguredDefaultConnections(
	defaults pipeline.EmptyStringMap,
) error {
	if len(defaults) == 0 {
		return nil
	}

	cfg, _, err := NewConfigService(s.workspaceRoot, "").LoadReadOnly()
	if err != nil {
		return fmt.Errorf("load project connections: %w", err)
	}
	available := make(map[string]map[string]struct{})
	for _, environment := range cfg.Environments {
		for _, connection := range buildWorkspaceConfigConnections(environment.Connections) {
			if available[connection.Type] == nil {
				available[connection.Type] = make(map[string]struct{})
			}
			available[connection.Type][connection.Name] = struct{}{}
		}
	}

	platforms := make([]string, 0, len(defaults))
	for platform := range defaults {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	for _, platform := range platforms {
		name := defaults[platform]
		names := available[platform]
		if len(names) == 0 {
			return fmt.Errorf(
				"%w: platform %q has no configured project connections",
				ErrInvalidPipelineDefaultConnection,
				platform,
			)
		}
		if _, exists := names[name]; !exists {
			return fmt.Errorf(
				"%w: connection %q is not configured for platform %q in any environment",
				ErrInvalidPipelineDefaultConnection,
				name,
				platform,
			)
		}
	}
	return nil
}

// pipelineConnectionSummaries loads assets only for the settings response. The
// normal pipeline config path deliberately parses pipeline.yml alone so
// schedule/config edits remain available even when an asset is temporarily
// invalid. These summaries are supplementary: a failed full parse simply
// leaves both read-only lists empty.
func (s *PipelineService) pipelineConnectionSummaries(
	ctx context.Context,
	relPath string,
) ([]webmodel.PipelineConfigConnection, []webmodel.PipelineReferencedConnection) {
	absPath, err := SafeJoin(s.workspaceRoot, relPath)
	if err != nil {
		return nil, nil
	}
	parsed, err := s.newPipelineBuilder().CreatePipelineFromPath(ctx, absPath, pipeline.WithMutate())
	if err != nil {
		return nil, nil
	}
	return inferPipelineDefaultConnections(parsed), referencedPipelineConnections(parsed)
}

func inferPipelineDefaultConnections(parsed *pipeline.Pipeline) []webmodel.PipelineConfigConnection {
	if parsed == nil {
		return nil
	}

	explicitNames := make(map[string]struct{}, len(parsed.DefaultConnections))
	for _, name := range parsed.DefaultConnections {
		if name = strings.TrimSpace(name); name != "" {
			explicitNames[name] = struct{}{}
		}
	}

	inferred := make(map[string]string)
	for _, asset := range parsed.Assets {
		if asset == nil || strings.TrimSpace(asset.Connection) != "" {
			continue
		}
		platform, ok := defaultConnectionPlatformForAsset(parsed, asset)
		if !ok {
			continue
		}
		if explicit := strings.TrimSpace(parsed.DefaultConnections[platform]); explicit != "" {
			continue
		}

		name, err := targetConnectionNameForAsset(asset, parsed)
		if err != nil {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, explicit := explicitNames[name]; explicit {
			continue
		}
		if current, exists := inferred[platform]; !exists || name < current {
			inferred[platform] = name
		}
	}

	result := make([]webmodel.PipelineConfigConnection, 0, len(inferred))
	for platform, name := range inferred {
		result = append(result, webmodel.PipelineConfigConnection{Platform: platform, Name: name})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Platform == result[j].Platform {
			return result[i].Name < result[j].Name
		}
		return result[i].Platform < result[j].Platform
	})
	return result
}

func referencedPipelineConnections(parsed *pipeline.Pipeline) []webmodel.PipelineReferencedConnection {
	if parsed == nil {
		return nil
	}

	assetsByConnection := make(map[string]map[string]struct{})
	add := func(connectionName, assetName string) {
		connectionName = strings.TrimSpace(connectionName)
		assetName = strings.TrimSpace(assetName)
		if connectionName == "" || assetName == "" || connectionName == "local" {
			return
		}
		if assetsByConnection[connectionName] == nil {
			assetsByConnection[connectionName] = make(map[string]struct{})
		}
		assetsByConnection[connectionName][assetName] = struct{}{}
	}

	for _, asset := range parsed.Assets {
		if asset == nil {
			continue
		}
		if names, err := parsed.GetAllConnectionNamesForAsset(asset); err == nil {
			for _, name := range names {
				add(name, asset.Name)
			}
		}

		// Renart-owned API and Load assets are not fully represented by Bruin's
		// generic connection resolver. Include their canonical target and Load
		// source so this list matches the execution path.
		if isAPIAsset(asset) || isLoadAsset(asset) {
			if target, err := targetConnectionNameForAsset(asset, parsed); err == nil {
				add(target, asset.Name)
			}
		}
		if isLoadAsset(asset) {
			add(loadParamsFromAsset(asset).SourceConnection, asset.Name)
		}
	}

	connectionNames := make([]string, 0, len(assetsByConnection))
	for name := range assetsByConnection {
		connectionNames = append(connectionNames, name)
	}
	sort.Strings(connectionNames)

	result := make([]webmodel.PipelineReferencedConnection, 0, len(connectionNames))
	for _, name := range connectionNames {
		assetNames := make([]string, 0, len(assetsByConnection[name]))
		for assetName := range assetsByConnection[name] {
			assetNames = append(assetNames, assetName)
		}
		sort.Strings(assetNames)
		result = append(result, webmodel.PipelineReferencedConnection{
			Name:   name,
			Assets: assetNames,
		})
	}
	return result
}

func defaultConnectionPlatformForAsset(parsed *pipeline.Pipeline, asset *pipeline.Asset) (string, bool) {
	if parsed == nil || asset == nil {
		return "", false
	}

	assetType := asset.Type
	switch {
	case isAPIAsset(asset):
		// API assets can name their target inside parameters.load.target rather
		// than through asset.Connection. That is still an asset-level override,
		// so it must not be presented as a default inferred for the pipeline.
		if connection, err := apiConnectionNameForAsset(asset, nil); err != nil || strings.TrimSpace(connection) != "" {
			return "", false
		}
		assetType = parsed.GetMajorityAssetTypesFromSQLAssets(pipeline.AssetTypeDuckDBQuery)
	case isLoadAsset(asset):
		assetType = parsed.GetMajorityAssetTypesFromSQLAssets(pipeline.AssetTypeDuckDBQuery)
	case assetType == pipeline.AssetTypePython || assetType == pipeline.AssetTypeEmpty:
		assetType = parsed.GetMajorityAssetTypesFromSQLAssets(pipeline.AssetTypeBigqueryQuery)
	case assetType == pipeline.AssetTypeIngestr:
		if connection, ok := asset.Parameters.GetString("destination_connection"); ok && strings.TrimSpace(connection) != "" {
			return "", false
		}
		destination, _ := asset.Parameters.GetString("destination")
		var ok bool
		assetType, ok = pipeline.IngestrTypeConnectionMapping[strings.ToLower(strings.TrimSpace(destination))]
		if !ok {
			return "", false
		}
	}

	platform, ok := pipeline.AssetTypeConnectionMapping[assetType]
	return platform, ok && strings.TrimSpace(platform) != ""
}

func buildNotifications(slack, teams webmodel.PipelineConfigNotification) pipeline.Notifications {
	result := pipeline.Notifications{}
	if slack.Enabled && strings.TrimSpace(slack.Channel) != "" {
		result.Slack = []pipeline.SlackNotification{{
			Channel: strings.TrimSpace(slack.Channel),
			NotificationCommon: pipeline.NotificationCommon{
				Success: boolToDefaultTrue(slack.Success),
				Failure: boolToDefaultTrue(slack.Failure),
			},
		}}
	}
	if teams.Enabled && strings.TrimSpace(teams.Connection) != "" {
		result.MSTeams = []pipeline.MSTeamsNotification{{
			Connection: strings.TrimSpace(teams.Connection),
			NotificationCommon: pipeline.NotificationCommon{
				Success: boolToDefaultTrue(teams.Success),
				Failure: boolToDefaultTrue(teams.Failure),
			},
		}}
	}
	return result
}

func boolToDefaultTrue(value bool) pipeline.DefaultTrueBool {
	copyValue := value
	return pipeline.DefaultTrueBool{Value: &copyValue}
}

func buildDefaultValues(input webmodel.PipelineConfigDefaults) (*pipeline.DefaultValues, error) {
	var values *pipeline.DefaultValues
	if input.RerunCooldown != nil {
		values = &pipeline.DefaultValues{RerunCooldown: normalizeOptionalInt(input.RerunCooldown)}
	}

	intervals := pipeline.IntervalModifiers{}
	start, ok, err := parseTimeModifierString(input.StartOffsetRaw)
	if err != nil {
		return nil, err
	}
	if ok {
		intervals.Start = start
		if values == nil {
			values = &pipeline.DefaultValues{}
		}
	}
	end, ok, err := parseTimeModifierString(input.EndOffsetRaw)
	if err != nil {
		return nil, err
	}
	if ok {
		intervals.End = end
		if values == nil {
			values = &pipeline.DefaultValues{}
		}
	}
	if values != nil {
		values.IntervalModifiers = intervals
	}

	if values == nil {
		return nil, nil
	}
	return values, nil
}

func buildVariables(input []webmodel.PipelineConfigVariable) (pipeline.Variables, error) {
	if len(input) == 0 {
		return nil, nil
	}

	result := make(pipeline.Variables)
	for _, variable := range input {
		name := strings.TrimSpace(variable.Name)
		variableType := strings.TrimSpace(variable.Type)
		if name == "" {
			continue
		}
		if variableType == "" {
			return nil, fmt.Errorf("variable %q must have a type", name)
		}
		definition := map[string]any{
			"type":    variableType,
			"default": variable.DefaultValue,
		}
		if description := strings.TrimSpace(variable.Description); description != "" {
			definition["description"] = description
		}
		for key, value := range variable.Extra {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" || trimmedKey == "type" || trimmedKey == "default" || trimmedKey == "description" {
				continue
			}
			definition[trimmedKey] = value
		}
		result[name] = definition
	}

	if len(result) == 0 {
		return nil, nil
	}
	if err := (&result).Validate(); err != nil {
		return nil, err
	}
	return result, nil
}

func parseTimeModifierString(raw string) (pipeline.TimeModifier, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return pipeline.TimeModifier{}, false, nil
	}

	var modifier pipeline.TimeModifier
	if strings.Contains(trimmed, "{{") || strings.Contains(trimmed, "{%") {
		modifier.Template = trimmed
		return modifier, true, nil
	}

	parts := strings.Fields(trimmed)
	if len(parts) > 1 {
		return pipeline.TimeModifier{}, false, fmt.Errorf("invalid interval modifier %q", trimmed)
	}

	suffix := trimmed[len(trimmed)-1]
	numeric := trimmed[:len(trimmed)-1]
	if len(trimmed) >= 3 {
		twoCharSuffix := trimmed[len(trimmed)-2:]
		if twoCharSuffix == "ms" || twoCharSuffix == "ns" {
			numeric = trimmed[:len(trimmed)-2]
			suffix = 0
			switch twoCharSuffix {
			case "ms":
				if value, err := parseNumericModifier(numeric); err == nil {
					modifier.Milliseconds = value
					return modifier, true, nil
				}
			case "ns":
				if value, err := parseNumericModifier(numeric); err == nil {
					modifier.Nanoseconds = value
					return modifier, true, nil
				}
			}
			return pipeline.TimeModifier{}, false, fmt.Errorf("invalid interval modifier %q", trimmed)
		}
	}

	value, err := parseNumericModifier(numeric)
	if err != nil {
		return pipeline.TimeModifier{}, false, fmt.Errorf("invalid interval modifier %q", trimmed)
	}
	switch suffix {
	case 'h':
		modifier.Hours = value
	case 'm':
		modifier.Minutes = value
	case 's':
		modifier.Seconds = value
	case 'd':
		modifier.Days = value
	case 'M':
		modifier.Months = value
	default:
		return pipeline.TimeModifier{}, false, fmt.Errorf("invalid interval modifier %q", trimmed)
	}
	return modifier, true, nil
}

func parseNumericModifier(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

func timeModifierString(modifier pipeline.TimeModifier) string {
	if modifier.Template != "" {
		return modifier.Template
	}
	if modifier.Months != 0 {
		return fmt.Sprintf("%dM", modifier.Months)
	}
	if modifier.Days != 0 {
		return fmt.Sprintf("%dd", modifier.Days)
	}
	if modifier.Hours != 0 {
		return fmt.Sprintf("%dh", modifier.Hours)
	}
	if modifier.Minutes != 0 {
		return fmt.Sprintf("%dm", modifier.Minutes)
	}
	if modifier.Seconds != 0 {
		return fmt.Sprintf("%ds", modifier.Seconds)
	}
	if modifier.Milliseconds != 0 {
		return fmt.Sprintf("%dms", modifier.Milliseconds)
	}
	if modifier.Nanoseconds != 0 {
		return fmt.Sprintf("%dns", modifier.Nanoseconds)
	}
	return ""
}

func normalizeOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func optionalIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func max(value, floor int) int {
	if value < floor {
		return floor
	}
	return value
}

func (s *PipelineService) newPipelineBuilder() *pipeline.Builder {
	return NewRenartPipelineBuilder(afero.NewOsFs())
}
