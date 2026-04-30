package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	webmodel "renart/internal/web/model"
)

type PipelineService struct {
	workspaceRoot string
}

func NewPipelineService(workspaceRoot string) *PipelineService {
	return &PipelineService{workspaceRoot: workspaceRoot}
}

func (s *PipelineService) Create(ctx context.Context, relPath, name, content string) (string, error) {
	absPath, err := SafeJoin(s.workspaceRoot, relPath)
	if err != nil {
		return "", err
	}
	fs := afero.NewOsFs()

	if err := fs.MkdirAll(absPath, 0o755); err != nil {
		return "", err
	}

	if strings.TrimSpace(content) == "" {
		if strings.TrimSpace(name) == "" {
			name = filepath.Base(absPath)
		}
		content = fmt.Sprintf("name: %s\n", name)
	}

	if err := afero.WriteFile(fs, filepath.Join(absPath, "pipeline.yml"), []byte(content), 0o644); err != nil {
		return "", err
	}

	return filepath.ToSlash(relPath), nil
}

func (s *PipelineService) Update(ctx context.Context, pipelineID, name, content string) (string, error) {
	relPath, err := DecodeID(pipelineID)
	if err != nil {
		return "", err
	}

	absPath, err := SafeJoin(s.workspaceRoot, relPath)
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
	resp.Status = "ok"
	return resp, nil
}

func (s *PipelineService) UpdateConfig(ctx context.Context, pipelineID string, req webmodel.UpdatePipelineConfigRequest) (string, *webmodel.PipelineConfigResponse, error) {
	parsed, relPath, err := s.loadPipeline(ctx, pipelineID)
	if err != nil {
		return "", nil, err
	}

	parsed.Name = strings.TrimSpace(req.Name)
	parsed.Schedule = pipeline.Schedule(strings.TrimSpace(req.Schedule))
	parsed.StartDate = strings.TrimSpace(req.StartDate)
	parsed.Owner = strings.TrimSpace(req.Owner)
	parsed.Tags = normalizeStringArray(req.Tags)
	parsed.Domains = normalizeStringArray(req.Domains)
	parsed.DefaultConnections = buildDefaultConnections(req.DefaultConnections)
	parsed.Catchup = req.Catchup
	parsed.MetadataPush = pipeline.MetadataPush{BigQuery: req.MetadataPushBigQuery}
	parsed.Retries = req.Retries
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
	resp.Status = "ok"
	return filepath.ToSlash(relPath), resp, nil
}

func (s *PipelineService) Delete(pipelineID string) (string, error) {
	relPath, err := DecodeID(pipelineID)
	if err != nil {
		return "", err
	}

	absPath, err := SafeJoin(s.workspaceRoot, relPath)
	if err != nil {
		return "", err
	}

	if err := afero.NewOsFs().RemoveAll(absPath); err != nil {
		return "", err
	}

	return filepath.ToSlash(relPath), nil
}

func (s *PipelineService) loadPipeline(ctx context.Context, pipelineID string) (*pipeline.Pipeline, string, error) {
	relPath, err := DecodeID(pipelineID)
	if err != nil {
		return nil, "", err
	}

	absPath, err := SafeJoin(s.workspaceRoot, relPath)
	if err != nil {
		return nil, "", err
	}

	parsed, err := s.newPipelineBuilder().CreatePipelineFromPath(ctx, absPath, pipeline.WithMutate(), pipeline.WithOnlyPipeline())
	if err != nil {
		return nil, "", err
	}

	return parsed, relPath, nil
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
		Catchup:              parsed.Catchup,
		MetadataPushBigQuery: parsed.MetadataPush.BigQuery,
		Retries:              parsed.Retries,
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

func buildDefaultConnections(input []webmodel.PipelineConfigConnection) pipeline.EmptyStringMap {
	result := make(map[string]string)
	for _, item := range input {
		platform := strings.TrimSpace(item.Platform)
		name := strings.TrimSpace(item.Name)
		if platform == "" || name == "" {
			continue
		}
		result[platform] = name
	}
	if len(result) == 0 {
		return nil
	}
	return result
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

func max(value, floor int) int {
	if value < floor {
		return floor
	}
	return value
}

func (s *PipelineService) newPipelineBuilder() *pipeline.Builder {
	osFS := afero.NewOsFs()
	return pipeline.NewBuilder(
		BuilderConfig,
		pipeline.CreateTaskFromYamlDefinition(osFS),
		pipeline.CreateTaskFromFileComments(osFS),
		osFS,
		DefaultGlossaryReader,
	)
}
