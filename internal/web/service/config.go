package service

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/connection"
	"github.com/spf13/afero"
)

type WorkspaceConfigFieldDef struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	DefaultValue string `json:"default_value,omitempty"`
	IsRequired   bool   `json:"is_required"`
}

type WorkspaceConfigConnectionType struct {
	TypeName string                    `json:"type_name"`
	Fields   []WorkspaceConfigFieldDef `json:"fields"`
}

type WorkspaceConfigConnection struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Values map[string]any `json:"values"`
}

type WorkspaceConfigEnvironment struct {
	Name         string                      `json:"name"`
	SchemaPrefix string                      `json:"schema_prefix,omitempty"`
	Connections  []WorkspaceConfigConnection `json:"connections"`
}

type WorkspaceConfigResponse struct {
	Status              string                          `json:"status"`
	Path                string                          `json:"path"`
	DefaultEnvironment  string                          `json:"default_environment,omitempty"`
	SelectedEnvironment string                          `json:"selected_environment,omitempty"`
	Environments        []WorkspaceConfigEnvironment    `json:"environments"`
	ConnectionTypes     []WorkspaceConfigConnectionType `json:"connection_types"`
	ParseError          string                          `json:"parse_error,omitempty"`
}

type UpsertWorkspaceConnectionParams struct {
	EnvironmentName string
	CurrentName     string
	Name            string
	Type            string
	Values          map[string]any
}

type TestWorkspaceConnectionParams struct {
	EnvironmentName string
	CurrentName     string
	Name            string
	Type            string
	Values          map[string]any
}

type ConfigService struct {
	workspaceRoot string
	configPath    string
}

func NewConfigService(workspaceRoot, configPath string) *ConfigService {
	if strings.TrimSpace(configPath) == "" {
		configPath = filepath.Join(workspaceRoot, ".bruin.yml")
	}

	return &ConfigService{workspaceRoot: workspaceRoot, configPath: configPath}
}

func (s *ConfigService) ConfigPath() string {
	return s.configPath
}

func (s *ConfigService) LoadForEditing() (*config.Config, string, error) {
	cfg, err := config.LoadOrCreateWithoutPathAbsolutization(afero.NewOsFs(), s.configPath)
	if err != nil {
		return nil, s.configPath, err
	}

	return cfg, s.configPath, nil
}

func (s *ConfigService) Persist(cfg *config.Config) (string, error) {
	if err := afero.NewOsFs().MkdirAll(filepath.Dir(s.configPath), 0o755); err != nil {
		return "", err
	}
	if err := cfg.Persist(); err != nil {
		return "", err
	}

	relPath, err := filepath.Rel(s.workspaceRoot, s.configPath)
	if err != nil {
		relPath = filepath.Base(s.configPath)
	}

	return filepath.ToSlash(relPath), nil
}

func (s *ConfigService) BuildResponse(configPath string, cfg *config.Config) WorkspaceConfigResponse {
	response := WorkspaceConfigResponse{
		Status:              "ok",
		Path:                filepath.Base(configPath),
		DefaultEnvironment:  cfg.DefaultEnvironmentName,
		SelectedEnvironment: cfg.SelectedEnvironmentName,
		Environments:        []WorkspaceConfigEnvironment{},
		ConnectionTypes:     BuildWorkspaceConfigConnectionTypes(),
	}

	environmentNames := cfg.GetEnvironmentNames()
	sort.Strings(environmentNames)
	for _, envName := range environmentNames {
		env := cfg.Environments[envName]
		response.Environments = append(response.Environments, WorkspaceConfigEnvironment{
			Name:         envName,
			SchemaPrefix: env.SchemaPrefix,
			Connections:  buildWorkspaceConfigConnections(env.Connections),
		})
	}

	return response
}

func (s *ConfigService) BuildParseErrorResponse(parseErr error) WorkspaceConfigResponse {
	return WorkspaceConfigResponse{
		Status:          "ok",
		Path:            filepath.Base(s.configPath),
		Environments:    []WorkspaceConfigEnvironment{},
		ConnectionTypes: BuildWorkspaceConfigConnectionTypes(),
		ParseError:      parseErr.Error(),
	}
}

func mapsClone(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (s *ConfigService) AddConnection(cfg *config.Config, params UpsertWorkspaceConnectionParams) error {
	environmentName := strings.TrimSpace(params.EnvironmentName)
	name := strings.TrimSpace(params.Name)
	typeName := strings.TrimSpace(params.Type)
	if environmentName == "" || name == "" || typeName == "" {
		return fmt.Errorf("environment, name, and type are required")
	}

	if _, exists := cfg.Environments[environmentName]; !exists {
		if err := cfg.AddEnvironment(environmentName, ""); err != nil {
			return err
		}
		if strings.TrimSpace(cfg.DefaultEnvironmentName) == "" {
			cfg.DefaultEnvironmentName = environmentName
		}
		if strings.TrimSpace(cfg.SelectedEnvironmentName) == "" {
			cfg.SelectedEnvironmentName = environmentName
		}
	}

	values, err := normalizeWorkspaceConnectionValues(typeName, params.Values)
	if err != nil {
		return err
	}

	return cfg.AddConnection(environmentName, name, typeName, values)
}

func (s *ConfigService) UpdateConnection(cfg *config.Config, params UpsertWorkspaceConnectionParams) error {
	environmentName := strings.TrimSpace(params.EnvironmentName)
	currentName := strings.TrimSpace(params.CurrentName)
	if currentName == "" {
		currentName = strings.TrimSpace(params.Name)
	}

	if err := cfg.DeleteConnection(environmentName, currentName); err != nil {
		return err
	}

	return s.AddConnection(cfg, params)
}

func (s *ConfigService) TestConnection(ctx context.Context, cfg *config.Config, params TestWorkspaceConnectionParams) (string, error) {
	environmentName := strings.TrimSpace(params.EnvironmentName)
	if environmentName == "" {
		environmentName = cfg.SelectedEnvironmentName
	}
	if environmentName == "" {
		environmentName = cfg.DefaultEnvironmentName
	}
	if environmentName == "" {
		return "", fmt.Errorf("no environment selected")
	}

	connectionName := strings.TrimSpace(params.Name)
	if connectionName == "" {
		return "", fmt.Errorf("connection name is required")
	}

	if strings.TrimSpace(params.Type) != "" {
		if err := s.prepareDraftConnection(cfg, TestWorkspaceConnectionParams{
			EnvironmentName: environmentName,
			CurrentName:     params.CurrentName,
			Name:            connectionName,
			Type:            params.Type,
			Values:          params.Values,
		}); err != nil {
			return "", err
		}
	}

	if err := cfg.SelectEnvironment(environmentName); err != nil {
		return "", err
	}

	manager, errs := connection.NewManagerFromConfigWithContext(ctx, cfg)
	if len(errs) > 0 {
		return "", errs[0]
	}

	conn := manager.GetConnection(connectionName)
	if conn == nil {
		return "", fmt.Errorf("connection %q not found", connectionName)
	}

	tester, ok := conn.(interface{ Ping(context.Context) error })
	if !ok {
		return fmt.Sprintf("Connection '%s' does not support validation yet.", connectionName), nil
	}

	if err := tester.Ping(ctx); err != nil {
		return "", fmt.Errorf("failed to test connection '%s': %w", connectionName, err)
	}

	return fmt.Sprintf("Successfully validated connection '%s' in environment %s.", connectionName, environmentName), nil
}

func (s *ConfigService) prepareDraftConnection(cfg *config.Config, params TestWorkspaceConnectionParams) error {
	environmentName := strings.TrimSpace(params.EnvironmentName)
	name := strings.TrimSpace(params.Name)
	typeName := strings.TrimSpace(params.Type)
	if environmentName == "" || name == "" || typeName == "" {
		return fmt.Errorf("environment, name, and type are required")
	}

	if _, exists := cfg.Environments[environmentName]; !exists {
		if err := cfg.AddEnvironment(environmentName, ""); err != nil {
			return err
		}
		if strings.TrimSpace(cfg.DefaultEnvironmentName) == "" {
			cfg.DefaultEnvironmentName = environmentName
		}
		if strings.TrimSpace(cfg.SelectedEnvironmentName) == "" {
			cfg.SelectedEnvironmentName = environmentName
		}
	}

	currentName := strings.TrimSpace(params.CurrentName)
	if currentName == "" {
		currentName = name
	}

	if err := cfg.DeleteConnection(environmentName, currentName); err != nil && !strings.Contains(err.Error(), "does not exist") {
		return err
	}

	values, err := normalizeWorkspaceConnectionValues(typeName, params.Values)
	if err != nil {
		return err
	}

	return cfg.AddConnection(environmentName, name, typeName, values)
}

func BuildWorkspaceConfigConnectionTypes() []WorkspaceConfigConnectionType {
	connectionsType := reflect.TypeFor[config.Connections]()
	items := make([]WorkspaceConfigConnectionType, 0, connectionsType.NumField())
	for index := 0; index < connectionsType.NumField(); index++ {
		structField := connectionsType.Field(index)
		if !structField.IsExported() || structField.Type.Kind() != reflect.Slice {
			continue
		}

		typeName := structField.Tag.Get("yaml")
		if separator := strings.Index(typeName, ","); separator >= 0 {
			typeName = typeName[:separator]
		}
		if typeName == "" {
			continue
		}

		elementType := structField.Type.Elem()
		if elementType.Kind() == reflect.Pointer {
			elementType = elementType.Elem()
		}
		if elementType.Kind() != reflect.Struct {
			continue
		}

		items = append(items, WorkspaceConfigConnectionType{
			TypeName: typeName,
			Fields:   buildWorkspaceConfigFieldDefs(elementType),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].TypeName < items[j].TypeName
	})

	return items
}

func buildWorkspaceConfigFieldDefs(connectionType reflect.Type) []WorkspaceConfigFieldDef {
	fields := make([]WorkspaceConfigFieldDef, 0, connectionType.NumField())
	for index := 0; index < connectionType.NumField(); index++ {
		structField := connectionType.Field(index)
		if !structField.IsExported() {
			continue
		}

		mapstructureTag := structField.Tag.Get("mapstructure")
		if separator := strings.Index(mapstructureTag, ","); separator >= 0 {
			mapstructureTag = mapstructureTag[:separator]
		}
		if mapstructureTag == "" || mapstructureTag == "name" {
			continue
		}

		fieldType := buildWorkspaceConfigFieldType(structField.Type.Kind())
		if fieldType == "" {
			continue
		}

		defaultValue := ""
		if jsonschemaTag := structField.Tag.Get("jsonschema"); jsonschemaTag != "" {
			for part := range strings.SplitSeq(jsonschemaTag, ",") {
				part = strings.TrimSpace(part)
				if value, ok := strings.CutPrefix(part, "default="); ok {
					defaultValue = value
				}
			}
		}
		if defaultValue == "" {
			defaultValue = structField.Tag.Get("default")
		}

		yamlTag := structField.Tag.Get("yaml")
		fields = append(fields, WorkspaceConfigFieldDef{
			Name:         mapstructureTag,
			Type:         fieldType,
			DefaultValue: defaultValue,
			IsRequired:   !strings.Contains(yamlTag, "omitempty"),
		})
	}

	return fields
}

func buildWorkspaceConfigFieldType(kind reflect.Kind) string {
	switch kind { //nolint:exhaustive
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Bool:
		return "bool"
	default:
		return ""
	}
}

func buildWorkspaceConfigConnections(connections *config.Connections) []WorkspaceConfigConnection {
	if connections == nil {
		return []WorkspaceConfigConnection{}
	}

	value := reflect.ValueOf(connections)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return []WorkspaceConfigConnection{}
	}

	valueType := value.Type()
	items := make([]WorkspaceConfigConnection, 0)
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		structField := valueType.Field(index)
		if field.Kind() != reflect.Slice {
			continue
		}

		typeName := structField.Tag.Get("yaml")
		if separator := strings.Index(typeName, ","); separator >= 0 {
			typeName = typeName[:separator]
		}
		if typeName == "" {
			continue
		}

		for itemIndex := 0; itemIndex < field.Len(); itemIndex++ {
			connectionValue := field.Index(itemIndex)
			connectionInterface := connectionValue.Interface()
			named, ok := connectionInterface.(interface{ GetName() string })
			if !ok {
				continue
			}

			items = append(items, WorkspaceConfigConnection{
				Name:   named.GetName(),
				Type:   typeName,
				Values: buildWorkspaceConfigConnectionValues(connectionInterface, typeName),
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Type == items[j].Type {
			return items[i].Name < items[j].Name
		}
		return items[i].Type < items[j].Type
	})

	return items
}

func buildWorkspaceConfigConnectionValues(connectionValue any, typeName string) map[string]any {
	result := make(map[string]any)
	fieldDefs := config.GetConnectionFieldsForType(typeName)
	if len(fieldDefs) == 0 {
		return result
	}

	value := reflect.ValueOf(connectionValue)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return result
	}

	valueType := value.Type()
	for _, fieldDef := range fieldDefs {
		for index := 0; index < value.NumField(); index++ {
			structField := valueType.Field(index)
			mapstructureTag := structField.Tag.Get("mapstructure")
			if separator := strings.Index(mapstructureTag, ","); separator >= 0 {
				mapstructureTag = mapstructureTag[:separator]
			}
			if mapstructureTag != fieldDef.Name {
				continue
			}

			fieldValue := value.Field(index)
			switch fieldValue.Kind() { //nolint:exhaustive
			case reflect.String:
				result[fieldDef.Name] = fieldValue.String()
			case reflect.Bool:
				result[fieldDef.Name] = fieldValue.Bool()
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				result[fieldDef.Name] = fieldValue.Int()
			}
			break
		}
	}

	return result
}

func normalizeWorkspaceConnectionValues(typeName string, values map[string]any) (map[string]any, error) {
	result := make(map[string]any)
	fieldDefs := config.GetConnectionFieldsForType(typeName)
	for _, fieldDef := range fieldDefs {
		rawValue, exists := values[fieldDef.Name]
		if !exists {
			continue
		}

		switch fieldDef.Type {
		case "string":
			result[fieldDef.Name] = strings.TrimSpace(fmt.Sprint(rawValue))
		case "bool":
			boolValue, err := normalizeWorkspaceBoolValue(rawValue)
			if err != nil {
				return nil, fmt.Errorf("invalid value for %s: %w", fieldDef.Name, err)
			}
			result[fieldDef.Name] = boolValue
		case "int":
			intValue, err := normalizeWorkspaceIntValue(rawValue)
			if err != nil {
				return nil, fmt.Errorf("invalid value for %s: %w", fieldDef.Name, err)
			}
			result[fieldDef.Name] = intValue
		}
	}

	return result, nil
}

func normalizeWorkspaceBoolValue(rawValue any) (bool, error) {
	switch value := rawValue.(type) {
	case bool:
		return value, nil
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return false, nil
		}
		if strings.EqualFold(trimmed, "true") {
			return true, nil
		}
		if strings.EqualFold(trimmed, "false") {
			return false, nil
		}
	}

	return false, fmt.Errorf("expected boolean")
}

func normalizeWorkspaceIntValue(rawValue any) (int, error) {
	switch value := rawValue.(type) {
	case int:
		return value, nil
	case int8:
		return int(value), nil
	case int16:
		return int(value), nil
	case int32:
		return int(value), nil
	case int64:
		return int(value), nil
	case float64:
		return int(value), nil
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return 0, nil
		}
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	}

	return 0, fmt.Errorf("expected integer")
}
