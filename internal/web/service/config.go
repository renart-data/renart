package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/mask"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"renart/internal/web/identity"
	"renart/internal/web/policy"
	"renart/internal/web/secretstore"
)

type WorkspaceConfigFieldDef struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	DefaultValue    string `json:"default_value,omitempty"`
	IsRequired      bool   `json:"is_required"`
	IsSensitive     bool   `json:"is_sensitive"`
	IsSensitiveFile bool   `json:"is_sensitive_file"`
}

type WorkspaceConfigSecretField struct {
	Status    string `json:"status"`
	Provider  string `json:"provider,omitempty"`
	Reference string `json:"reference,omitempty"`
	Message   string `json:"message,omitempty"`
	Writable  bool   `json:"writable"`
	Rotatable bool   `json:"rotatable"`
}

type WorkspaceConfigConnectionType struct {
	TypeName string                    `json:"type_name"`
	Fields   []WorkspaceConfigFieldDef `json:"fields"`
	// Category is "warehouse" for connection types that can run SQL assets;
	// "storage" for object stores usable by native Load/Seed flows; everything
	// else (ingestr source connections, SaaS APIs) is "source". The UI hides
	// only "source" types unless the ingestr feature is enabled.
	Category string `json:"category"`
}

type WorkspaceLocalVault struct {
	State       string `json:"state"`
	SecretCount int    `json:"secret_count"`
	Message     string `json:"message,omitempty"`
}

type WorkspaceConfigConnection struct {
	Name         string                                `json:"name"`
	Type         string                                `json:"type"`
	Values       map[string]any                        `json:"values"`
	SecretFields map[string]WorkspaceConfigSecretField `json:"secret_fields,omitempty"`
	// LoadCategory is "database", "storage", "file" for connections a Load
	// asset can move data between, or "" for connections that are not Load-movable
	// data stores. The asset editor's source/target pickers filter on it.
	LoadCategory string `json:"load_category,omitempty"`
}

type WorkspaceConfigEnvironment struct {
	Name         string                      `json:"name"`
	SchemaPrefix string                      `json:"schema_prefix,omitempty"`
	Connections  []WorkspaceConfigConnection `json:"connections"`
}

type WorkspaceRetentionWindow struct {
	Days               int `json:"days"`
	MinimumPerPipeline int `json:"minimum_per_pipeline"`
}

type WorkspaceRetentionSettings struct {
	RunMetadata               WorkspaceRetentionWindow `json:"run_metadata"`
	FullLogs                  WorkspaceRetentionWindow `json:"full_logs"`
	MaterializationFactsDays  int                      `json:"materialization_facts_days"`
	ScheduleHistoryDays       int                      `json:"schedule_history_days"`
	Deployments               WorkspaceRetentionWindow `json:"deployments"`
	TemporaryDirectoriesHours int                      `json:"temporary_directories_hours"`
}

// renart:web
type WorkspaceConfigResponse struct {
	Status              string                          `json:"status"`
	Path                string                          `json:"path"`
	WorkspacePath       string                          `json:"workspace_path,omitempty"`
	ProjectID           string                          `json:"project_id,omitempty"`
	ProjectName         string                          `json:"project_name,omitempty"`
	DefaultEnvironment  string                          `json:"default_environment,omitempty"`
	SelectedEnvironment string                          `json:"selected_environment,omitempty"`
	Environments        []WorkspaceConfigEnvironment    `json:"environments"`
	ConnectionTypes     []WorkspaceConfigConnectionType `json:"connection_types"`
	Features            map[string]bool                 `json:"features,omitempty"`
	Retention           WorkspaceRetentionSettings      `json:"retention"`
	SecretVault         WorkspaceLocalVault             `json:"secret_vault"`
	ParseError          string                          `json:"parse_error,omitempty"`
	SecretBindingsError string                          `json:"secret_bindings_error,omitempty"`
}

// renart:web
type WorkspaceEnvironmentPolicyResponse struct {
	Status      string                   `json:"status"`
	Environment string                   `json:"environment"`
	Policy      policy.EnvironmentPolicy `json:"policy"`
}

type UpsertWorkspaceConnectionParams struct {
	EnvironmentName string
	CurrentName     string
	Name            string
	Type            string
	Values          map[string]any
	SecretChanges   map[string]WorkspaceConnectionSecretChange
}

type TestWorkspaceConnectionParams struct {
	EnvironmentName string
	CurrentName     string
	Name            string
	Type            string
	Values          map[string]any
	SecretChanges   map[string]WorkspaceConnectionSecretChange
}

type WorkspaceConnectionSecretChange struct {
	Action  string                            `json:"action"`
	Value   string                            `json:"value,omitempty"`
	Binding *WorkspaceConnectionSecretBinding `json:"binding,omitempty"`
}

type WorkspaceConnectionSecretBinding struct {
	Ref      string `json:"ref,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type ConfigService struct {
	workspaceRoot  string
	configPath     string
	secretResolver *secretstore.Resolver
	secretVault    *secretstore.LocalVaultProvider
	mu             sync.Mutex
}

type ConfigServiceOption func(*ConfigService)

func WithSecretResolver(resolver *secretstore.Resolver) ConfigServiceOption {
	return func(service *ConfigService) {
		if resolver != nil {
			service.secretResolver = resolver
		}
	}
}

func WithSecretVault(vault *secretstore.LocalVaultProvider) ConfigServiceOption {
	return func(service *ConfigService) {
		if vault != nil {
			service.secretVault = vault
		}
	}
}

func NewConfigService(workspaceRoot, configPath string, options ...ConfigServiceOption) *ConfigService {
	if strings.TrimSpace(configPath) == "" {
		configPath = filepath.Join(workspaceRoot, ".bruin.yml")
	}

	vault := secretstore.NewLocalVaultProvider("")
	service := &ConfigService{
		workspaceRoot:  workspaceRoot,
		configPath:     configPath,
		secretResolver: secretstore.NewDefaultResolverWithLocalVault(vault),
		secretVault:    vault,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *ConfigService) InitializeLocalVault(ctx context.Context, passphrase []byte) error {
	return s.secretVault.Initialize(ctx, s.ProjectIdentity().ID, passphrase)
}

func (s *ConfigService) UnlockLocalVault(ctx context.Context, passphrase []byte) error {
	return s.secretVault.Unlock(ctx, s.ProjectIdentity().ID, passphrase)
}

func (s *ConfigService) LockLocalVault() {
	s.secretVault.Lock(s.ProjectIdentity().ID)
}

func (s *ConfigService) ChangeLocalVaultPassphrase(
	ctx context.Context,
	passphrase []byte,
) error {
	return s.secretVault.ChangePassphrase(ctx, s.ProjectIdentity().ID, passphrase)
}

func (s *ConfigService) ConfigPath() string {
	return s.configPath
}

func (s *ConfigService) projectYmlPath() string {
	return filepath.Join(s.workspaceRoot, ".renart", "project.yml")
}

func (s *ConfigService) defaultProjectName() string {
	return filepath.Base(filepath.Clean(s.workspaceRoot))
}

// ProjectIdentity self-assigns .renart/project.yml on first use. Errors
// degrade to a nameless-but-usable identity so a corrupt project.yml never
// takes the config API down.
func (s *ConfigService) ProjectIdentity() identity.Project {
	project, err := identity.EnsureProject(afero.NewOsFs(), s.projectYmlPath(), s.defaultProjectName())
	if err != nil {
		return identity.Project{Name: s.defaultProjectName()}
	}
	return project
}

func (s *ConfigService) RenameProject(name string) (identity.Project, error) {
	fs := afero.NewOsFs()
	project, err := identity.EnsureProject(fs, s.projectYmlPath(), s.defaultProjectName())
	if err != nil {
		return identity.Project{}, err
	}
	project.Name = name
	if err := identity.SaveProject(fs, s.projectYmlPath(), project); err != nil {
		return identity.Project{}, err
	}
	return project, nil
}

// SetProjectFeatures merges the given feature flags into .renart/project.yml.
func (s *ConfigService) SetProjectFeatures(features map[string]bool) (identity.Project, error) {
	fs := afero.NewOsFs()
	project, err := identity.EnsureProject(fs, s.projectYmlPath(), s.defaultProjectName())
	if err != nil {
		return identity.Project{}, err
	}
	if project.Features == nil {
		project.Features = map[string]bool{}
	}
	for name, enabled := range features {
		if enabled {
			project.Features[name] = true
		} else {
			delete(project.Features, name)
		}
	}
	if len(project.Features) == 0 {
		project.Features = nil
	}
	if err := identity.SaveProject(fs, s.projectYmlPath(), project); err != nil {
		return identity.Project{}, err
	}
	return project, nil
}

// SetProjectRetention validates and persists the complete tracked retention
// policy. The API always sends the effective values shown in settings, so the
// resulting project.yml is self-explanatory and reviewable.
func (s *ConfigService) SetProjectRetention(settings WorkspaceRetentionSettings) (identity.Project, error) {
	fs := afero.NewOsFs()
	project, err := identity.EnsureProject(fs, s.projectYmlPath(), s.defaultProjectName())
	if err != nil {
		return identity.Project{}, err
	}
	normalized, err := identity.NormalizeRetentionSettings(&identity.RetentionSettings{
		RunMetadata: identity.RetentionWindow{
			Days:               settings.RunMetadata.Days,
			MinimumPerPipeline: settings.RunMetadata.MinimumPerPipeline,
		},
		FullLogs: identity.RetentionWindow{
			Days:               settings.FullLogs.Days,
			MinimumPerPipeline: settings.FullLogs.MinimumPerPipeline,
		},
		MaterializationFactsDays: settings.MaterializationFactsDays,
		ScheduleHistoryDays:      settings.ScheduleHistoryDays,
		Deployments: identity.RetentionWindow{
			Days:               settings.Deployments.Days,
			MinimumPerPipeline: settings.Deployments.MinimumPerPipeline,
		},
		TemporaryDirectoriesHours: settings.TemporaryDirectoriesHours,
	})
	if err != nil {
		return identity.Project{}, err
	}
	project.Retention = &normalized
	if err := identity.SaveProject(fs, s.projectYmlPath(), project); err != nil {
		return identity.Project{}, err
	}
	return project, nil
}

func (s *ConfigService) LoadForEditing() (*config.Config, string, error) {
	cfg, err := config.LoadOrCreateWithoutPathAbsolutization(afero.NewOsFs(), s.configPath)
	if err != nil {
		return nil, s.configPath, err
	}
	if err := restoreManagedSecretPlaceholders(
		afero.NewOsFs(),
		s.configPath,
		cfg,
		false,
	); err != nil {
		return nil, s.configPath, err
	}

	return cfg, s.configPath, nil
}

// LoadReadOnly loads the effective config without creating project files or
// updating .gitignore. Runtime paths may be made absolute by Bruin; callers
// must not persist this representation.
func (s *ConfigService) LoadReadOnly() (*config.Config, string, error) {
	fs := afero.NewOsFs()
	cfg, err := config.LoadFromFileOrEnv(fs, s.configPath)
	if err != nil {
		return nil, s.configPath, err
	}
	if cfg == nil {
		cfg = &config.Config{}
	}
	if err := restoreManagedSecretPlaceholders(fs, s.configPath, cfg, true); err != nil {
		return nil, s.configPath, err
	}
	return cfg, s.configPath, nil
}

// ConnectionSummaries returns only connection names and types for one
// environment. It deliberately avoids resolving or exposing connection values,
// making it suitable for discovery UIs such as the Data Browser.
func (s *ConfigService) ConnectionSummaries(environment string) (string, map[string]string, error) {
	cfg, _, err := s.LoadReadOnly()
	if err != nil {
		return "", nil, err
	}

	resolvedEnvironment := strings.TrimSpace(environment)
	if resolvedEnvironment == "" {
		resolvedEnvironment = strings.TrimSpace(cfg.SelectedEnvironmentName)
	}
	if resolvedEnvironment == "" {
		resolvedEnvironment = strings.TrimSpace(cfg.DefaultEnvironmentName)
	}
	if resolvedEnvironment == "" {
		environments := cfg.GetEnvironmentNames()
		sort.Strings(environments)
		if len(environments) > 0 {
			resolvedEnvironment = environments[0]
		}
	}
	if resolvedEnvironment == "" {
		return "", map[string]string{}, nil
	}
	if err := cfg.SelectEnvironment(resolvedEnvironment); err != nil {
		return "", nil, err
	}
	if cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return resolvedEnvironment, map[string]string{}, nil
	}

	connections := cfg.SelectedEnvironment.Connections.ConnectionsSummaryList()
	result := make(map[string]string, len(connections))
	for name, connectionType := range connections {
		result[name] = connectionType
	}
	return resolvedEnvironment, result, nil
}

func (s *ConfigService) Persist(cfg *config.Config) (string, error) {
	if err := afero.NewOsFs().MkdirAll(filepath.Dir(s.configPath), 0o755); err != nil {
		return "", err
	}
	if err := cfg.Persist(); err != nil {
		return "", err
	}
	// The config should contain only secret placeholders after managed writes,
	// but legacy inline credentials remain supported until they are replaced.
	// Keep either form private on disk.
	if err := os.Chmod(s.configPath, 0o600); err != nil {
		return "", fmt.Errorf("secure config permissions: %w", err)
	}

	relPath, err := filepath.Rel(s.workspaceRoot, s.configPath)
	if err != nil {
		relPath = filepath.Base(s.configPath)
	}

	return filepath.ToSlash(relPath), nil
}

func (s *ConfigService) BuildResponse(configPath string, cfg *config.Config) WorkspaceConfigResponse {
	project := s.ProjectIdentity()
	manifest, manifestErr := secretstore.LoadManifest(filepath.Join(s.workspaceRoot, ".renart", "secrets.yml"))
	response := WorkspaceConfigResponse{
		Status:              "ok",
		Path:                filepath.Base(configPath),
		WorkspacePath:       filepath.Clean(s.workspaceRoot),
		ProjectID:           project.ID,
		ProjectName:         project.Name,
		DefaultEnvironment:  cfg.DefaultEnvironmentName,
		SelectedEnvironment: cfg.SelectedEnvironmentName,
		Environments:        []WorkspaceConfigEnvironment{},
		ConnectionTypes:     BuildWorkspaceConfigConnectionTypes(),
		Features:            project.Features,
		Retention:           workspaceRetentionSettings(project.Retention),
		SecretVault:         workspaceLocalVaultStatus(s.secretVault.Status(project.ID)),
	}
	if manifestErr != nil {
		response.SecretBindingsError = manifestErr.Error()
		manifest = secretstore.NewManifest()
	}

	environmentNames := cfg.GetEnvironmentNames()
	sort.Strings(environmentNames)
	for _, envName := range environmentNames {
		env := cfg.Environments[envName]
		connections := buildWorkspaceConfigConnections(env.Connections)
		s.decorateWorkspaceConnectionSecrets(project.ID, envName, env.Connections, connections, manifest)
		response.Environments = append(response.Environments, WorkspaceConfigEnvironment{
			Name:         envName,
			SchemaPrefix: env.SchemaPrefix,
			Connections:  connections,
		})
	}

	return response
}

func (s *ConfigService) decorateWorkspaceConnectionSecrets(
	projectID string,
	environmentName string,
	connections *config.Connections,
	items []WorkspaceConfigConnection,
	manifest secretstore.Manifest,
) {
	if connections == nil {
		return
	}
	for itemIndex := range items {
		item := &items[itemIndex]
		rawValues := buildWorkspaceConfigConnectionRawValues(
			connections.GetConnection(item.Name),
			item.Type,
		)
		for _, fieldDef := range workspaceConnectionFieldDefsForType(item.Type) {
			if !fieldDef.IsSensitive && !fieldDef.IsSensitiveFile {
				continue
			}
			descriptor := item.SecretFields[fieldDef.Name]
			rawValue, _ := rawValues[fieldDef.Name].(string)
			symbolMatch := exactSecretSymbolPattern.FindStringSubmatch(rawValue)
			binding, hasBinding := manifest.Binding(environmentName, item.Name, fieldDef.Name)
			if len(symbolMatch) != 2 {
				switch {
				case rawValue == "" && fieldDef.IsSensitiveFile:
					descriptor.Status = string(secretstore.StatusMissing)
					descriptor.Provider = "file"
					descriptor.Writable = true
					descriptor.Rotatable = true
				case rawValue == "":
					descriptor = s.describeWorkspaceSecret(
						projectID,
						environmentName,
						secretstore.Ref{
							Provider: "local",
							Key:      managedSecretAlias(item.Name, fieldDef.Name),
						},
					)
				case fieldDef.IsSensitiveFile:
					descriptor.Status = string(secretstore.StatusConfigured)
					descriptor.Provider = "file"
					descriptor.Writable = true
					descriptor.Rotatable = true
				default:
					descriptor.Status = string(secretstore.StatusConfigured)
					descriptor.Provider = "inline"
					descriptor.Writable = true
					descriptor.Rotatable = true
					localDescriptor := s.describeWorkspaceSecret(
						projectID,
						environmentName,
						secretstore.Ref{
							Provider: "local",
							Key:      managedSecretAlias(item.Name, fieldDef.Name),
						},
					)
					if localDescriptor.Status == string(secretstore.StatusPermissionRequired) ||
						localDescriptor.Status == string(secretstore.StatusUnavailable) {
						descriptor.Message = "The current inline value remains usable. " + localDescriptor.Message
					} else {
						descriptor.Message = "Replace this value to move it from the local config into the system credential store."
					}
				}
				if hasBinding {
					descriptor.Message = fmt.Sprintf(
						"The tracked binding expects ${%s}, but this connection does not use that placeholder.",
						binding.Symbol,
					)
				}
				item.SecretFields[fieldDef.Name] = descriptor
				continue
			}

			symbol := symbolMatch[1]
			if !hasBinding {
				binding = secretstore.Binding{
					Symbol:    symbol,
					Reference: secretstore.Ref{Provider: "env", Key: symbol},
				}
			}
			if binding.Symbol != symbol {
				descriptor.Provider = binding.Reference.Provider
				descriptor.Reference = binding.Reference.String()
				descriptor.Status = string(secretstore.StatusUnavailable)
				descriptor.Writable = false
				descriptor.Rotatable = false
				descriptor.Message = fmt.Sprintf(
					"The tracked binding expects ${%s}, but the connection uses ${%s}.",
					binding.Symbol,
					symbol,
				)
				item.SecretFields[fieldDef.Name] = descriptor
				continue
			}

			descriptor = s.describeWorkspaceSecret(projectID, environmentName, binding.Reference)
			item.SecretFields[fieldDef.Name] = descriptor
		}
	}
}

func (s *ConfigService) describeWorkspaceSecret(
	projectID string,
	environmentName string,
	reference secretstore.Ref,
) WorkspaceConfigSecretField {
	descriptor := WorkspaceConfigSecretField{
		Provider:  reference.Provider,
		Reference: reference.String(),
	}
	status, err := s.secretResolver.Stat(context.Background(), secretstore.ResolveRequest{
		ProjectID:   projectID,
		Environment: environmentName,
		Reference:   reference,
		Purpose:     secretstore.PurposeSecretAdministration,
	})
	if status.State == "" {
		status.State = secretstore.StatusUnavailable
	}
	descriptor.Status = string(status.State)
	descriptor.Writable = status.Writable
	descriptor.Rotatable = status.Rotatable
	if err != nil {
		descriptor.Message = safeSecretStatusMessage(reference.Provider, err)
	}
	return descriptor
}

func safeSecretStatusMessage(provider string, err error) string {
	switch {
	case errors.Is(err, secretstore.ErrPermissionRequired):
		if provider == "local" {
			return "The system credential store is locked. Unlock it in this user session or choose Environment for SSH and headless sessions."
		}
		if provider == "local-vault" {
			return "The encrypted local vault is locked. Unlock it in Connection settings for this Renart session."
		}
		return fmt.Sprintf("%s credential access requires permission.", provider)
	case errors.Is(err, secretstore.ErrUnavailable):
		if provider == "local" {
			return "The system credential store is unavailable. Choose Encrypted vault or Environment for SSH and headless sessions."
		}
		if provider == "local-vault" {
			return "The encrypted local vault is unavailable or has not been set up."
		}
		return fmt.Sprintf("The %s secret provider is unavailable.", provider)
	case errors.Is(err, secretstore.ErrUnknownProvider):
		return fmt.Sprintf("The %s secret provider is not installed.", provider)
	default:
		return "The secret status could not be checked."
	}
}

func safeSecretResolutionMessage(err error) string {
	switch {
	case errors.Is(err, secretstore.ErrNotFound):
		return "A required secret is missing. Add it in the connection settings or provide the referenced environment variable."
	case errors.Is(err, secretstore.ErrPermissionRequired):
		return "A required credential needs permission from the system credential store."
	case errors.Is(err, secretstore.ErrUnavailable):
		return "The required secret provider is unavailable or locked."
	case errors.Is(err, secretstore.ErrUnknownProvider):
		return "A required secret uses a provider that is not available in this Renart installation."
	default:
		return "The connection secrets could not be resolved."
	}
}

func (s *ConfigService) BuildParseErrorResponse(parseErr error) WorkspaceConfigResponse {
	project := s.ProjectIdentity()
	return WorkspaceConfigResponse{
		Status:          "ok",
		Path:            filepath.Base(s.configPath),
		WorkspacePath:   filepath.Clean(s.workspaceRoot),
		ProjectID:       project.ID,
		ProjectName:     project.Name,
		Environments:    []WorkspaceConfigEnvironment{},
		ConnectionTypes: BuildWorkspaceConfigConnectionTypes(),
		Features:        project.Features,
		Retention:       workspaceRetentionSettings(project.Retention),
		SecretVault:     workspaceLocalVaultStatus(s.secretVault.Status(project.ID)),
		ParseError:      parseErr.Error(),
	}
}

func workspaceLocalVaultStatus(status secretstore.LocalVaultStatus) WorkspaceLocalVault {
	return WorkspaceLocalVault{
		State:       string(status.State),
		SecretCount: status.SecretCount,
		Message:     status.Message,
	}
}

func workspaceRetentionSettings(settings *identity.RetentionSettings) WorkspaceRetentionSettings {
	normalized, err := identity.NormalizeRetentionSettings(settings)
	if err != nil {
		normalized = identity.DefaultRetentionSettings()
	}
	return WorkspaceRetentionSettings{
		RunMetadata: WorkspaceRetentionWindow{
			Days:               normalized.RunMetadata.Days,
			MinimumPerPipeline: normalized.RunMetadata.MinimumPerPipeline,
		},
		FullLogs: WorkspaceRetentionWindow{
			Days:               normalized.FullLogs.Days,
			MinimumPerPipeline: normalized.FullLogs.MinimumPerPipeline,
		},
		MaterializationFactsDays: normalized.MaterializationFactsDays,
		ScheduleHistoryDays:      normalized.ScheduleHistoryDays,
		Deployments: WorkspaceRetentionWindow{
			Days:               normalized.Deployments.Days,
			MinimumPerPipeline: normalized.Deployments.MinimumPerPipeline,
		},
		TemporaryDirectoriesHours: normalized.TemporaryDirectoriesHours,
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

	values, err := assembleWorkspaceConnectionValues(typeName, params.Values, params.SecretChanges, nil)
	if err != nil {
		return err
	}

	return cfg.AddConnection(environmentName, name, typeName, values)
}

func (s *ConfigService) UpdateConnection(cfg *config.Config, params UpsertWorkspaceConnectionParams) error {
	environmentName := strings.TrimSpace(params.EnvironmentName)
	name := strings.TrimSpace(params.Name)
	if environmentName == "" || name == "" {
		return fmt.Errorf("environment and name are required")
	}
	currentName := strings.TrimSpace(params.CurrentName)
	if currentName == "" {
		currentName = name
	}
	environment, exists := cfg.Environments[environmentName]
	if !exists || environment.Connections == nil {
		return fmt.Errorf("environment %q does not contain connection %q", environmentName, currentName)
	}
	currentType := normalizeConnectionType(environment.Connections.ConnectionsSummaryList()[currentName])
	nextType := normalizeConnectionType(params.Type)
	if currentType != "" && nextType != "" && currentType != nextType {
		return fmt.Errorf("connection type is immutable; create a new %s connection instead of changing %q from %s", nextType, currentName, currentType)
	}
	if nextType == "" {
		nextType = currentType
	}
	if nextType == "" {
		return fmt.Errorf("connection type is required")
	}

	existingValues := buildWorkspaceConfigConnectionRawValues(environment.Connections.GetConnection(currentName), currentType)
	values, err := assembleWorkspaceConnectionValues(nextType, params.Values, params.SecretChanges, existingValues)
	if err != nil {
		return err
	}

	if err := cfg.DeleteConnection(environmentName, currentName); err != nil {
		return err
	}

	return cfg.AddConnection(environmentName, name, nextType, values)
}

func (s *ConfigService) TestConnection(ctx context.Context, cfg *config.Config, params TestWorkspaceConnectionParams) (string, error) {
	environmentName, err := requireEnvironmentName(cfg, params.EnvironmentName)
	if err != nil {
		return "", err
	}

	connectionName := strings.TrimSpace(params.Name)
	if connectionName == "" {
		return "", fmt.Errorf("connection name is required")
	}

	resolvedSecretChanges, draftSecretBundle, err := s.resolveDraftConnectionSecretChanges(
		ctx,
		environmentName,
		params.SecretChanges,
	)
	if err != nil {
		return "", err
	}
	if draftSecretBundle != nil {
		defer draftSecretBundle.Close(ctx)
	}
	params.SecretChanges = resolvedSecretChanges

	if strings.TrimSpace(params.Type) != "" {
		if err := s.prepareDraftConnection(cfg, TestWorkspaceConnectionParams{
			EnvironmentName: environmentName,
			CurrentName:     params.CurrentName,
			Name:            connectionName,
			Type:            params.Type,
			Values:          params.Values,
			SecretChanges:   params.SecretChanges,
		}); err != nil {
			return "", err
		}
	}

	selectedCfg, err := selectConfigEnvironment(cfg, environmentName)
	if err != nil {
		return "", err
	}
	factory := NewResolvedConnectionFactory(
		s.workspaceRoot,
		s.configPath,
		s.ProjectIdentity().ID,
		s.secretResolver,
	)
	resolved, err := factory.ResolveConfigForConnections(
		ctx,
		selectedCfg,
		environmentName,
		secretstore.PurposeConnectionValidation,
		connectionName,
	)
	if err != nil {
		return "", err
	}
	defer resolved.Close(ctx)
	redactor := resolved.Redactor

	manager, err := newConnectionManagerFromConfig(ctx, resolved.Config)
	if err != nil {
		return "", errors.New(redactor.Mask(err.Error()))
	}

	conn, err := resolveRuntimeConnection(manager, connectionName)
	if err != nil {
		return "", err
	}
	if conn == nil {
		return "", fmt.Errorf("connection %q not found", connectionName)
	}

	tester, ok := conn.(interface{ Ping(context.Context) error })
	if !ok {
		return fmt.Sprintf("Connection '%s' does not support validation yet.", connectionName), nil
	}

	if err := tester.Ping(ctx); err != nil {
		return "", fmt.Errorf("failed to test connection '%s': %s", connectionName, redactor.Mask(err.Error()))
	}

	return fmt.Sprintf("Successfully validated connection '%s' in environment %s.", connectionName, environmentName), nil
}

func (s *ConfigService) resolveDraftConnectionSecretChanges(
	ctx context.Context,
	environmentName string,
	changes map[string]WorkspaceConnectionSecretChange,
) (map[string]WorkspaceConnectionSecretChange, *secretstore.Bundle, error) {
	result := cloneWorkspaceSecretChanges(changes)
	requests := make([]secretstore.NamedRequest, 0)
	fieldsByRequest := make(map[string]string)

	for fieldName, change := range result {
		if strings.ToLower(strings.TrimSpace(change.Action)) != "replace" ||
			change.Binding == nil ||
			strings.TrimSpace(change.Binding.Ref) == "" {
			continue
		}
		reference, err := secretstore.ParseRef(change.Binding.Ref)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid binding for connection secret field %q: %w", fieldName, err)
		}
		switch reference.Provider {
		case "local", "local-vault":
			// Stored replacements carry the write-only value in this request.
			// The connection test uses it in memory but does not persist it.
			continue
		case "env":
			if change.Value != "" {
				return nil, nil, fmt.Errorf(
					"environment binding for connection secret field %q cannot include a secret value",
					fieldName,
				)
			}
		default:
			return nil, nil, fmt.Errorf(
				"connection secret field %q supports only local:, local-vault:, and env: bindings in the editor",
				fieldName,
			)
		}

		requestName := "draft." + fieldName
		requests = append(requests, secretstore.NamedRequest{
			Name: requestName,
			Request: secretstore.ResolveRequest{
				ProjectID:   s.ProjectIdentity().ID,
				Environment: environmentName,
				Reference:   reference,
				Purpose:     secretstore.PurposeConnectionValidation,
			},
		})
		fieldsByRequest[requestName] = fieldName
	}
	if len(requests) == 0 {
		return result, nil, nil
	}

	bundle, err := s.secretResolver.ResolveAll(ctx, requests)
	if err != nil {
		return nil, nil, err
	}
	for requestName, fieldName := range fieldsByRequest {
		change := result[fieldName]
		change.Value = string(bundle.Value(requestName))
		result[fieldName] = change
	}
	return result, bundle, nil
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

	environment := cfg.Environments[environmentName]
	existingType := normalizeConnectionType(environment.Connections.ConnectionsSummaryList()[currentName])
	var existingValues map[string]any
	if existingType == normalizeConnectionType(typeName) {
		existingValues = buildWorkspaceConfigConnectionRawValues(environment.Connections.GetConnection(currentName), existingType)
	}

	values, err := assembleWorkspaceConnectionValues(typeName, params.Values, params.SecretChanges, existingValues)
	if err != nil {
		return err
	}

	if environment.Connections.Exists(currentName) {
		if err := cfg.DeleteConnection(environmentName, currentName); err != nil {
			return err
		}
	}

	return cfg.AddConnection(environmentName, name, typeName, values)
}

// warehouseConnectionTypes is the set of connection types that can run SQL
// assets, derived from bruin's asset-type→connection mapping. These are the
// connection types the UI always offers; the rest are ingestr/SaaS source
// connections hidden behind the ingestr feature flag.
func warehouseConnectionTypes() map[string]bool {
	out := map[string]bool{}
	for assetType, connectionType := range pipeline.AssetTypeConnectionMapping {
		if strings.HasSuffix(string(assetType), ".sql") {
			out[connectionType] = true
		}
	}
	return out
}

func BuildWorkspaceConfigConnectionTypes() []WorkspaceConfigConnectionType {
	warehouse := warehouseConnectionTypes()
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

		category := "source"
		if warehouse[typeName] {
			category = "warehouse"
		} else if loadConnectionCategory(typeName) == LoadCategoryStorage {
			category = "storage"
		}
		items = append(items, WorkspaceConfigConnectionType{
			TypeName: typeName,
			Fields:   buildWorkspaceConfigFieldDefs(elementType),
			Category: category,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].TypeName < items[j].TypeName
	})

	return items
}

func buildWorkspaceConfigFieldDefs(connectionType reflect.Type) []WorkspaceConfigFieldDef {
	fields := make([]WorkspaceConfigFieldDef, 0, connectionType.NumField())
	appendWorkspaceConfigFieldDefs(connectionType, &fields)
	sort.SliceStable(fields, func(left, right int) bool {
		return workspaceConnectionFieldOrder(fields[left]) < workspaceConnectionFieldOrder(fields[right])
	})
	return fields
}

// workspaceConnectionFieldOrder keeps connector-specific fields in their
// declared order inside broad user-facing groups: destination details first,
// authentication next, and tuning controls last.
func workspaceConnectionFieldOrder(field WorkspaceConfigFieldDef) int {
	name := strings.ToLower(field.Name)
	if name == "max_concurrent_assets" {
		return 100
	}
	if strings.HasPrefix(name, "max_") ||
		strings.HasPrefix(name, "pool_") ||
		strings.Contains(name, "timeout") ||
		strings.Contains(name, "page_size") ||
		strings.Contains(name, "prefetch") ||
		strings.HasPrefix(name, "disable_") {
		return 80
	}
	if field.IsSensitive || field.IsSensitiveFile {
		return 60
	}
	switch name {
	case "username", "user", "profile", "client_id", "tenant_id":
		return 50
	default:
		return 20
	}
}

func appendWorkspaceConfigFieldDefs(connectionType reflect.Type, fields *[]WorkspaceConfigFieldDef) {
	for index := 0; index < connectionType.NumField(); index++ {
		structField := connectionType.Field(index)
		if !structField.IsExported() {
			continue
		}
		if structField.Anonymous {
			embeddedType := structField.Type
			if embeddedType.Kind() == reflect.Pointer {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct {
				appendWorkspaceConfigFieldDefs(embeddedType, fields)
				continue
			}
		}

		mapstructureTag := structField.Tag.Get("mapstructure")
		if separator := strings.Index(mapstructureTag, ","); separator >= 0 {
			mapstructureTag = mapstructureTag[:separator]
		}
		if mapstructureTag == "" || mapstructureTag == "name" {
			continue
		}

		fieldType := buildWorkspaceConfigFieldType(structField.Type)
		if fieldType == "" {
			continue
		}

		defaultValues := make([]string, 0)
		if jsonschemaTag := structField.Tag.Get("jsonschema"); jsonschemaTag != "" {
			for part := range strings.SplitSeq(jsonschemaTag, ",") {
				part = strings.TrimSpace(part)
				if value, ok := strings.CutPrefix(part, "default="); ok {
					defaultValues = append(defaultValues, value)
				}
			}
		}
		defaultValue := strings.Join(defaultValues, ",")
		if defaultValue == "" {
			defaultValue = structField.Tag.Get("default")
		}

		yamlTag := structField.Tag.Get("yaml")
		*fields = append(*fields, WorkspaceConfigFieldDef{
			Name:            mapstructureTag,
			Type:            fieldType,
			DefaultValue:    defaultValue,
			IsRequired:      !strings.Contains(yamlTag, "omitempty"),
			IsSensitive:     structField.Tag.Get("sensitive") == "true",
			IsSensitiveFile: structField.Tag.Get("sensitive_file") == "true",
		})
	}
}

func buildWorkspaceConfigFieldType(fieldType reflect.Type) string {
	if fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	switch fieldType.Kind() { //nolint:exhaustive
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Bool:
		return "bool"
	case reflect.Slice:
		if fieldType.Elem().Kind() == reflect.String {
			return "string_array"
		}
		return ""
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
				Name:         named.GetName(),
				Type:         typeName,
				Values:       buildWorkspaceConfigConnectionValues(connectionInterface, typeName),
				SecretFields: buildWorkspaceConfigConnectionSecretFields(connectionInterface, typeName),
				LoadCategory: loadConnectionCategory(typeName),
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
	return buildWorkspaceConfigConnectionValuesMatching(connectionValue, typeName, func(field WorkspaceConfigFieldDef) bool {
		return !field.IsSensitive && !field.IsSensitiveFile
	})
}

func buildWorkspaceConfigConnectionRawValues(connectionValue any, typeName string) map[string]any {
	return buildWorkspaceConfigConnectionValuesMatching(connectionValue, typeName, func(WorkspaceConfigFieldDef) bool {
		return true
	})
}

func buildWorkspaceConfigConnectionValuesMatching(
	connectionValue any,
	typeName string,
	include func(WorkspaceConfigFieldDef) bool,
) map[string]any {
	result := make(map[string]any)
	fieldDefs := workspaceConnectionFieldDefsForType(typeName)
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

	for _, fieldDef := range fieldDefs {
		if !include(fieldDef) {
			continue
		}
		fieldValue, ok := workspaceConnectionFieldValue(value, fieldDef.Name)
		if !ok {
			continue
		}
		for fieldValue.Kind() == reflect.Pointer {
			if fieldValue.IsNil() {
				ok = false
				break
			}
			fieldValue = fieldValue.Elem()
		}
		if !ok {
			continue
		}
		switch fieldValue.Kind() { //nolint:exhaustive
		case reflect.String:
			result[fieldDef.Name] = fieldValue.String()
		case reflect.Bool:
			result[fieldDef.Name] = fieldValue.Bool()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result[fieldDef.Name] = fieldValue.Int()
		case reflect.Slice:
			if fieldValue.Type().Elem().Kind() != reflect.String {
				continue
			}
			values := make([]string, 0, fieldValue.Len())
			for itemIndex := 0; itemIndex < fieldValue.Len(); itemIndex++ {
				values = append(values, fieldValue.Index(itemIndex).String())
			}
			result[fieldDef.Name] = values
		}
	}

	return result
}

func workspaceConnectionFieldValue(value reflect.Value, fieldName string) (reflect.Value, bool) {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	valueType := value.Type()
	for index := 0; index < value.NumField(); index++ {
		structField := valueType.Field(index)
		if !structField.IsExported() {
			continue
		}
		if structField.Anonymous {
			if nested, ok := workspaceConnectionFieldValue(value.Field(index), fieldName); ok {
				return nested, true
			}
		}
		mapstructureTag := structField.Tag.Get("mapstructure")
		if separator := strings.Index(mapstructureTag, ","); separator >= 0 {
			mapstructureTag = mapstructureTag[:separator]
		}
		if mapstructureTag == fieldName {
			return value.Field(index), true
		}
	}
	return reflect.Value{}, false
}

func buildWorkspaceConfigConnectionSecretFields(connectionValue any, typeName string) map[string]WorkspaceConfigSecretField {
	result := make(map[string]WorkspaceConfigSecretField)
	rawValues := buildWorkspaceConfigConnectionRawValues(connectionValue, typeName)
	for _, fieldDef := range workspaceConnectionFieldDefsForType(typeName) {
		if !fieldDef.IsSensitive && !fieldDef.IsSensitiveFile {
			continue
		}
		status := "missing"
		if workspaceConnectionValueConfigured(rawValues[fieldDef.Name]) {
			status = "configured"
		}
		result[fieldDef.Name] = WorkspaceConfigSecretField{
			Status:    status,
			Writable:  true,
			Rotatable: true,
		}
	}
	return result
}

func workspaceConnectionValueConfigured(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return typed != ""
	case []string:
		return len(typed) > 0
	default:
		return !reflect.ValueOf(value).IsZero()
	}
}

func assembleWorkspaceConnectionValues(
	typeName string,
	values map[string]any,
	secretChanges map[string]WorkspaceConnectionSecretChange,
	existingValues map[string]any,
) (map[string]any, error) {
	fieldDefs := workspaceConnectionFieldDefsForType(typeName)
	fieldsByName := make(map[string]WorkspaceConfigFieldDef, len(fieldDefs))
	for _, fieldDef := range fieldDefs {
		fieldsByName[fieldDef.Name] = fieldDef
	}

	result := make(map[string]any, len(values)+len(secretChanges))
	for name, value := range values {
		fieldDef, exists := fieldsByName[name]
		if !exists {
			// Preserve the existing config-editor behavior: provider-specific
			// discovery drafts may carry harmless coordinates (for example a
			// selected database) that are not persisted by this connection type.
			continue
		}
		if fieldDef.IsSensitive || fieldDef.IsSensitiveFile {
			return nil, fmt.Errorf("sensitive connection field %q must use secret_changes", name)
		}
		result[name] = value
	}

	for name, change := range secretChanges {
		fieldDef, exists := fieldsByName[name]
		if !exists {
			return nil, fmt.Errorf("unknown connection secret field %q for type %q", name, typeName)
		}
		if !fieldDef.IsSensitive && !fieldDef.IsSensitiveFile {
			return nil, fmt.Errorf("connection field %q is not sensitive", name)
		}
		switch strings.ToLower(strings.TrimSpace(change.Action)) {
		case "keep":
			if existingValue, ok := existingValues[name]; ok {
				result[name] = existingValue
			}
		case "replace":
			if change.Value == "" {
				return nil, fmt.Errorf("replacement value for connection secret field %q is required", name)
			}
			result[name] = change.Value
		case "clear":
			if change.Value != "" {
				return nil, fmt.Errorf("clear action for connection secret field %q cannot include a value", name)
			}
		default:
			return nil, fmt.Errorf("connection secret field %q action must be keep, replace, or clear", name)
		}
	}

	for _, fieldDef := range fieldDefs {
		if (!fieldDef.IsSensitive && !fieldDef.IsSensitiveFile) || secretChanges[fieldDef.Name].Action != "" {
			continue
		}
		if existingValue, ok := existingValues[fieldDef.Name]; ok {
			result[fieldDef.Name] = existingValue
		}
	}

	return normalizeWorkspaceConnectionValues(typeName, result)
}

func normalizeWorkspaceConnectionValues(typeName string, values map[string]any) (map[string]any, error) {
	result := make(map[string]any)
	fieldDefs := workspaceConnectionFieldDefsForType(typeName)
	for _, fieldDef := range fieldDefs {
		rawValue, exists := values[fieldDef.Name]
		if !exists {
			continue
		}

		switch fieldDef.Type {
		case "string":
			stringValue := fmt.Sprint(rawValue)
			if !fieldDef.IsSensitive && !fieldDef.IsSensitiveFile {
				stringValue = strings.TrimSpace(stringValue)
			}
			result[fieldDef.Name] = stringValue
		case "bool":
			boolValue, err := normalizeWorkspaceBoolValue(rawValue)
			if err != nil {
				return nil, fmt.Errorf("invalid value for %s: %w", fieldDef.Name, err)
			}
			result[fieldDef.Name] = boolValue
		case "int":
			if stringValue, ok := rawValue.(string); ok &&
				strings.TrimSpace(stringValue) == "" &&
				!fieldDef.IsRequired {
				continue
			}
			intValue, err := normalizeWorkspaceIntValue(rawValue)
			if err != nil {
				return nil, fmt.Errorf("invalid value for %s: %w", fieldDef.Name, err)
			}
			result[fieldDef.Name] = intValue
		case "string_array":
			result[fieldDef.Name] = normalizeWorkspaceStringArrayValue(rawValue)
		}
	}

	return result, nil
}

func workspaceConnectionSensitiveValues(cfg *config.Config) []string {
	if cfg == nil || cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return nil
	}
	values, _ := mask.SensitiveValues(cfg.SelectedEnvironment.Connections)
	appendWorkspaceSensitiveFilePaths(reflect.ValueOf(cfg.SelectedEnvironment.Connections), &values)
	return values
}

func redactWorkspaceConnectionMessage(cfg *config.Config, message string) string {
	return mask.New(workspaceConnectionSensitiveValues(cfg)).Mask(message)
}

func appendWorkspaceSensitiveFilePaths(value reflect.Value, values *[]string) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	switch value.Kind() { //nolint:exhaustive
	case reflect.Struct:
		valueType := value.Type()
		for index := 0; index < value.NumField(); index++ {
			structField := valueType.Field(index)
			if !structField.IsExported() {
				continue
			}
			fieldValue := value.Field(index)
			if structField.Tag.Get("sensitive_file") == "true" && fieldValue.Kind() == reflect.String {
				if path := fieldValue.String(); path != "" {
					*values = append(*values, path)
				}
				continue
			}
			appendWorkspaceSensitiveFilePaths(fieldValue, values)
		}
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			appendWorkspaceSensitiveFilePaths(value.Index(index), values)
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			appendWorkspaceSensitiveFilePaths(value.MapIndex(key), values)
		}
	}
}

func workspaceConnectionFieldDefsForType(typeName string) []WorkspaceConfigFieldDef {
	for _, connectionType := range BuildWorkspaceConfigConnectionTypes() {
		if connectionType.TypeName == typeName {
			return connectionType.Fields
		}
	}
	return nil
}

func normalizeWorkspaceStringArrayValue(rawValue any) []string {
	switch value := rawValue.(type) {
	case []string:
		return compactWorkspaceStringArray(value)
	case []any:
		items := make([]string, 0, len(value))
		for _, item := range value {
			items = append(items, fmt.Sprint(item))
		}
		return compactWorkspaceStringArray(items)
	case string:
		return compactWorkspaceStringArray(strings.Split(value, ","))
	default:
		return compactWorkspaceStringArray([]string{fmt.Sprint(value)})
	}
}

func compactWorkspaceStringArray(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
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
