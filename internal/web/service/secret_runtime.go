package service

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/mask"
	"renart/internal/web/secretstore"
)

var exactSecretSymbolPattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// ResolvedConnectionFactory is the only production path that should turn a
// persisted connection configuration into live connection objects. It overlays
// provider-backed values on a freshly loaded config and never changes process
// environment variables or persists the resolved config.
type ResolvedConnectionFactory struct {
	workspaceRoot          string
	configPath             string
	projectID              string
	resolver               *secretstore.Resolver
	duckDBFilesystemAccess bool
}

type ResolvedConnectionFactoryOption func(*ResolvedConnectionFactory)

// WithDuckDBFilesystemAccess controls LocalFileSystem access for every DuckDB
// client produced by the factory. The default is enabled.
func WithDuckDBFilesystemAccess(enabled bool) ResolvedConnectionFactoryOption {
	return func(factory *ResolvedConnectionFactory) {
		factory.duckDBFilesystemAccess = enabled
	}
}

func NewResolvedConnectionFactory(
	workspaceRoot string,
	configPath string,
	projectID string,
	resolver *secretstore.Resolver,
	options ...ResolvedConnectionFactoryOption,
) *ResolvedConnectionFactory {
	if resolver == nil {
		resolver = secretstore.NewDefaultResolver()
	}
	factory := &ResolvedConnectionFactory{
		workspaceRoot:          workspaceRoot,
		configPath:             configPath,
		projectID:              projectID,
		resolver:               resolver,
		duckDBFilesystemAccess: true,
	}
	for _, option := range options {
		if option != nil {
			option(factory)
		}
	}
	return factory
}

func (f *ResolvedConnectionFactory) NewConnectionManager(
	ctx context.Context,
	environment string,
) (config.ConnectionAndDetailsGetter, error) {
	cfg, err := loadSelectedConfig(f.configPath, environment)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	manager := newResolvedConnectionManager(
		ctx,
		f,
		cfg,
		environment,
		secretstore.PurposeFromContext(ctx, secretstore.PurposeQuery),
	)
	return WrapConnectionManagerForWorkspaceWithFilesystemAccess(manager, f.workspaceRoot, f.duckDBFilesystemAccess), nil
}

type ResolvedConnectionConfig struct {
	Config      *config.Config
	Redactor    *mask.Masker
	bundle      *secretstore.Bundle
	environment map[string]string
}

func (r *ResolvedConnectionConfig) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	for name := range r.environment {
		r.environment[name] = ""
		delete(r.environment, name)
	}
	if r.bundle == nil {
		return nil
	}
	return r.bundle.Close(ctx)
}

// EnvironmentVariables returns the symbolic secret overlay for a scoped child
// process. Callers must keep the result operation-local and close the resolved
// config as soon as the child exits.
func (r *ResolvedConnectionConfig) EnvironmentVariables() map[string]string {
	if r == nil || len(r.environment) == 0 {
		return nil
	}
	result := make(map[string]string, len(r.environment))
	for name, value := range r.environment {
		result[name] = value
	}
	return result
}

func (f *ResolvedConnectionFactory) ResolveConfig(
	ctx context.Context,
	cfg *config.Config,
	environment string,
	purpose secretstore.Purpose,
) (*ResolvedConnectionConfig, error) {
	return f.resolveConfig(ctx, cfg, environment, purpose, nil)
}

// ResolveConfigForConnections overlays secrets for only the named connections.
// This is used by connection-scoped operations such as validation and import;
// ResolveConfig remains the explicit environment-wide path for `secrets exec`.
func (f *ResolvedConnectionFactory) ResolveConfigForConnections(
	ctx context.Context,
	cfg *config.Config,
	environment string,
	purpose secretstore.Purpose,
	connectionNames ...string,
) (*ResolvedConnectionConfig, error) {
	allowedConnections := make(map[string]struct{}, len(connectionNames))
	for _, name := range connectionNames {
		name = strings.TrimSpace(name)
		if name != "" {
			allowedConnections[name] = struct{}{}
		}
	}
	selected, err := selectConfigEnvironment(cfg, environment)
	if err != nil {
		return nil, err
	}
	scoped, err := configForConnections(selected, connectionNames...)
	if err != nil {
		return nil, err
	}
	return f.resolveConfig(ctx, scoped, environment, purpose, allowedConnections)
}

func (f *ResolvedConnectionFactory) resolveConfig(
	ctx context.Context,
	cfg *config.Config,
	environment string,
	purpose secretstore.Purpose,
	allowedConnections map[string]struct{},
) (*ResolvedConnectionConfig, error) {
	selected, err := selectConfigEnvironment(cfg, environment)
	if err != nil {
		return nil, err
	}
	if selected.SelectedEnvironment == nil || selected.SelectedEnvironment.Connections == nil {
		return &ResolvedConnectionConfig{
			Config:   selected,
			Redactor: mask.New(nil),
		}, nil
	}

	manifest, err := secretstore.LoadManifest(filepath.Join(f.workspaceRoot, ".renart", "secrets.yml"))
	if err != nil {
		return nil, err
	}
	requests, targets, err := f.connectionSecretRequests(
		selected,
		manifest,
		purpose,
		allowedConnections,
	)
	if err != nil {
		return nil, err
	}
	bundle, err := f.resolver.ResolveAll(ctx, requests)
	if err != nil {
		return nil, err
	}
	secretEnvironment := make(map[string]string, len(targets))
	for _, target := range targets {
		value := string(bundle.Value(target.name))
		if existing, found := secretEnvironment[target.symbol]; found && existing != value {
			_ = bundle.Close(ctx)
			return nil, fmt.Errorf(
				"secret symbol %s resolves to conflicting values; use a unique symbol for each binding",
				target.symbol,
			)
		}
		secretEnvironment[target.symbol] = value
	}
	for _, target := range targets {
		value := secretEnvironment[target.symbol]
		target.field.SetString(value)
	}

	redactionValues := workspaceConnectionSensitiveValues(selected)
	redactionValues = append(redactionValues, bundle.RedactionValues()...)
	return &ResolvedConnectionConfig{
		Config:      selected,
		Redactor:    mask.New(redactionValues),
		bundle:      bundle,
		environment: secretEnvironment,
	}, nil
}

type connectionSecretTarget struct {
	name   string
	symbol string
	field  reflect.Value
}

func (f *ResolvedConnectionFactory) connectionSecretRequests(
	cfg *config.Config,
	manifest secretstore.Manifest,
	purpose secretstore.Purpose,
	allowedConnections map[string]struct{},
) ([]secretstore.NamedRequest, []connectionSecretTarget, error) {
	connections := reflect.ValueOf(cfg.SelectedEnvironment.Connections)
	if connections.Kind() == reflect.Pointer {
		connections = connections.Elem()
	}
	connectionType := connections.Type()
	requests := make([]secretstore.NamedRequest, 0)
	targets := make([]connectionSecretTarget, 0)

	for typeIndex := 0; typeIndex < connections.NumField(); typeIndex++ {
		items := connections.Field(typeIndex)
		if items.Kind() != reflect.Slice {
			continue
		}
		typeName := strings.Split(connectionType.Field(typeIndex).Tag.Get("yaml"), ",")[0]
		fieldDefs := workspaceConnectionFieldDefsForType(typeName)
		for itemIndex := 0; itemIndex < items.Len(); itemIndex++ {
			item := items.Index(itemIndex)
			named, ok := item.Addr().Interface().(interface{ GetName() string })
			if !ok {
				continue
			}
			connectionName := named.GetName()
			if allowedConnections != nil {
				if _, allowed := allowedConnections[connectionName]; !allowed {
					continue
				}
			}
			for _, fieldDef := range fieldDefs {
				if !fieldDef.IsSensitive && !fieldDef.IsSensitiveFile {
					continue
				}
				field, found := workspaceConnectionFieldValue(item, fieldDef.Name)
				if !found || field.Kind() != reflect.String || !field.CanSet() {
					continue
				}
				symbolMatch := exactSecretSymbolPattern.FindStringSubmatch(field.String())
				if len(symbolMatch) != 2 {
					continue
				}
				symbol := symbolMatch[1]
				binding, found := manifest.Binding(cfg.SelectedEnvironmentName, connectionName, fieldDef.Name)
				if !found {
					binding = secretstore.Binding{
						Symbol:    symbol,
						Reference: secretstore.Ref{Provider: "env", Key: symbol},
					}
				}
				if binding.Symbol != symbol {
					return nil, nil, fmt.Errorf(
						"secret binding for %s.%s expects ${%s}, but the connection uses ${%s}",
						connectionName,
						fieldDef.Name,
						binding.Symbol,
						symbol,
					)
				}
				if fieldDef.IsSensitiveFile && binding.Reference.Provider != "env" {
					return nil, nil, fmt.Errorf(
						"secret binding for file field %s.%s must use an env: path reference",
						connectionName,
						fieldDef.Name,
					)
				}
				name := connectionName + "." + fieldDef.Name
				requests = append(requests, secretstore.NamedRequest{
					Name: name,
					Request: secretstore.ResolveRequest{
						ProjectID:   f.projectID,
						Environment: cfg.SelectedEnvironmentName,
						Reference:   binding.Reference,
						Purpose:     purpose,
					},
				})
				targets = append(targets, connectionSecretTarget{
					name: name, symbol: symbol, field: field,
				})
			}
		}
	}
	return requests, targets, nil
}
