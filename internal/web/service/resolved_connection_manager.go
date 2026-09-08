package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/spf13/afero"

	"renart/internal/web/policy"
	"renart/internal/web/secretstore"
)

// resolvedConnectionManager resolves and constructs each connection on first
// use. A missing credential on one configured connection therefore cannot
// prevent unrelated connections in the same environment from being used.
type resolvedConnectionManager struct {
	ctx         context.Context
	factory     *ResolvedConnectionFactory
	cfg         *config.Config
	environment string
	purpose     secretstore.Purpose
	entries     map[string]*resolvedConnectionEntry
}

type resolvedConnectionEntry struct {
	connectionType string

	once       sync.Once
	connection any
	details    any
	err        error
	accessMode policy.AccessMode
}

func newResolvedConnectionManager(
	ctx context.Context,
	factory *ResolvedConnectionFactory,
	cfg *config.Config,
	environment string,
	purpose secretstore.Purpose,
) *resolvedConnectionManager {
	if ctx == nil {
		ctx = context.Background()
	}
	return &resolvedConnectionManager{
		ctx:         ctx,
		factory:     factory,
		cfg:         cfg,
		environment: environment,
		purpose:     purpose,
		entries:     configuredConnectionEntries(cfg),
	}
}

func (m *resolvedConnectionManager) GetConnection(name string) any {
	connection, _ := m.ResolveConnection(name)
	return connection
}

func (m *resolvedConnectionManager) ResolveConnection(name string) (any, error) {
	name = strings.TrimSpace(name)
	entry := m.entries[name]
	if entry == nil {
		return nil, nil
	}
	snapshot, err := policy.NewLoader(filepath.Join(m.factory.workspaceRoot, ".renart", "environments.yml")).Snapshot()
	if err != nil {
		return nil, policy.InvalidError(err.Error())
	}
	details, _ := selectedConfigurationConnection(m.cfg, name)
	current := m.cfg
	currentPath := filepath.Join(m.factory.workspaceRoot, ".bruin.yml")
	if _, statErr := os.Stat(currentPath); !os.IsNotExist(statErr) {
		current, err = loadSelectedConfigReadOnlyFS(afero.NewOsFs(), currentPath, m.cfg.SelectedEnvironmentName)
		if err != nil {
			return nil, policy.InvalidError(err.Error())
		}
		currentDetails, exists := selectedConfigurationConnection(current, name)
		if !exists || nativeConnectionReadOnly(currentDetails) != nativeConnectionReadOnly(details) {
			return nil, newAPIError(409, "connection_policy_stale", "Connection access changed during this operation. Start a new operation to load the current connection configuration.")
		}
		details = currentDetails
	}
	mode := policy.EffectiveMode(snapshot.Config.For(current.SelectedEnvironmentName), name, nativeConnectionReadOnly(details))
	entry.once.Do(func() {
		entry.accessMode = mode
		entry.connection, entry.details, entry.err = m.resolve(name)
	})
	if entry.accessMode != mode {
		return nil, newAPIError(409, "connection_policy_stale", "Connection access changed during this operation. Start a new operation to open a compatible connection.")
	}
	return entry.connection, entry.err
}

func (m *resolvedConnectionManager) GetConnectionDetails(name string) any {
	entry := m.entries[strings.TrimSpace(name)]
	if entry == nil {
		return nil
	}
	if _, err := m.ResolveConnection(name); err != nil {
		return nil
	}
	return entry.details
}

func (m *resolvedConnectionManager) GetConnectionType(name string) string {
	entry := m.entries[strings.TrimSpace(name)]
	if entry == nil {
		return ""
	}
	return entry.connectionType
}

func (m *resolvedConnectionManager) resolve(name string) (any, any, error) {
	// Each lazy resolution gets its own selected config. ResolveConfig selects
	// environments and initializes connection lookup maps, so sharing the base
	// config across parallel pipeline tasks would otherwise introduce races.
	scopedConfig, err := configForConnections(m.cfg, name)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := m.factory.ResolveConfigForConnections(
		m.ctx,
		scopedConfig,
		m.environment,
		m.purpose,
		name,
	)
	if err != nil {
		return nil, nil, err
	}
	defer resolved.Close(m.ctx)

	manager, err := newConnectionManagerFromConfig(m.ctx, resolved.Config)
	if err != nil {
		return nil, nil, errors.New(resolved.Redactor.Mask(err.Error()))
	}
	connection := manager.GetConnection(name)
	if connection == nil {
		return nil, nil, fmt.Errorf("connection %q could not be initialized", name)
	}
	return connection, manager.GetConnectionDetails(name), nil
}

func configuredConnectionEntries(cfg *config.Config) map[string]*resolvedConnectionEntry {
	result := make(map[string]*resolvedConnectionEntry)
	if cfg == nil || cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return result
	}

	connections := reflect.ValueOf(cfg.SelectedEnvironment.Connections).Elem()
	connectionType := connections.Type()
	for fieldIndex := 0; fieldIndex < connections.NumField(); fieldIndex++ {
		field := connections.Field(fieldIndex)
		fieldDefinition := connectionType.Field(fieldIndex)
		if field.Kind() != reflect.Slice || fieldDefinition.PkgPath != "" {
			continue
		}
		typeName := strings.Split(fieldDefinition.Tag.Get("yaml"), ",")[0]
		for itemIndex := 0; itemIndex < field.Len(); itemIndex++ {
			item := field.Index(itemIndex)
			named, ok := item.Addr().Interface().(interface{ GetName() string })
			if !ok {
				continue
			}
			name := strings.TrimSpace(named.GetName())
			if name == "" {
				continue
			}
			result[name] = &resolvedConnectionEntry{connectionType: typeName}
		}
	}
	return result
}

func configForConnections(cfg *config.Config, connectionNames ...string) (*config.Config, error) {
	if cfg == nil {
		return nil, errors.New("connection config is required")
	}
	allowed := make(map[string]struct{}, len(connectionNames))
	for _, name := range connectionNames {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}

	scoped := *cfg
	scoped.Environments = make(map[string]config.Environment, len(cfg.Environments))
	for name, environment := range cfg.Environments {
		scoped.Environments[name] = environment
	}

	if cfg.SelectedEnvironment == nil {
		return &scoped, nil
	}
	selected := *cfg.SelectedEnvironment
	selected.Connections = filterConfiguredConnections(cfg.SelectedEnvironment.Connections, allowed)
	scoped.SelectedEnvironment = &selected
	if environmentName := strings.TrimSpace(cfg.SelectedEnvironmentName); environmentName != "" {
		scoped.Environments[environmentName] = selected
	}
	return &scoped, nil
}

func filterConfiguredConnections(
	source *config.Connections,
	allowed map[string]struct{},
) *config.Connections {
	target := &config.Connections{}
	if source == nil {
		return target
	}

	sourceValue := reflect.ValueOf(source).Elem()
	targetValue := reflect.ValueOf(target).Elem()
	sourceType := sourceValue.Type()
	for fieldIndex := 0; fieldIndex < sourceValue.NumField(); fieldIndex++ {
		fieldDefinition := sourceType.Field(fieldIndex)
		sourceField := sourceValue.Field(fieldIndex)
		targetField := targetValue.Field(fieldIndex)
		if sourceField.Kind() != reflect.Slice ||
			fieldDefinition.PkgPath != "" ||
			!targetField.CanSet() {
			continue
		}

		filtered := reflect.MakeSlice(sourceField.Type(), 0, sourceField.Len())
		for itemIndex := 0; itemIndex < sourceField.Len(); itemIndex++ {
			item := sourceField.Index(itemIndex)
			named, ok := item.Addr().Interface().(interface{ GetName() string })
			if !ok {
				continue
			}
			if _, include := allowed[strings.TrimSpace(named.GetName())]; include {
				filtered = reflect.Append(filtered, item)
			}
		}
		targetField.Set(filtered)
	}
	return target
}

var (
	_ config.ConnectionAndDetailsGetter = (*resolvedConnectionManager)(nil)
	_ config.ConnectionResolver         = (*resolvedConnectionManager)(nil)
)
