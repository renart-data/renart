package service

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"renart/internal/web/identity"
)

func TestPrepareDraftConnectionReplacesExistingConnection(t *testing.T) {
	svc := NewConfigService("/tmp/workspace", "/tmp/workspace/.bruin.yml")
	cfg := &config.Config{
		DefaultEnvironmentName:  "default",
		SelectedEnvironmentName: "default",
		Environments: map[string]config.Environment{
			"default": {
				Connections: &config.Connections{},
			},
		},
	}

	require.NoError(t, svc.prepareDraftConnection(cfg, TestWorkspaceConnectionParams{
		EnvironmentName: "default",
		Name:            "postgres-default",
		Type:            "postgres",
		Values: map[string]any{
			"host":     "127.0.0.1",
			"port":     5432,
			"database": "bruin",
			"username": "postgres",
		},
		SecretChanges: map[string]WorkspaceConnectionSecretChange{
			"password": {Action: "replace", Value: "secret"},
		},
	}))

	require.NoError(t, svc.prepareDraftConnection(cfg, TestWorkspaceConnectionParams{
		EnvironmentName: "default",
		CurrentName:     "postgres-default",
		Name:            "postgres-default",
		Type:            "postgres",
		Values: map[string]any{
			"host":     "localhost",
			"port":     5433,
			"database": "bruin",
			"username": "postgres",
		},
		SecretChanges: map[string]WorkspaceConnectionSecretChange{
			"password": {Action: "replace", Value: "updated"},
		},
	}))

	env := cfg.Environments["default"]
	require.Len(t, env.Connections.Postgres, 1)
	assert.Equal(t, "postgres-default", env.Connections.Postgres[0].Name)
	assert.Equal(t, "localhost", env.Connections.Postgres[0].Host)
	assert.Equal(t, 5433, env.Connections.Postgres[0].Port)
	assert.Equal(t, "updated", env.Connections.Postgres[0].Password)
}

func TestUpdateConnectionRejectsConnectionTypeMutation(t *testing.T) {
	t.Parallel()
	svc := NewConfigService("/tmp/workspace", "/tmp/workspace/.bruin.yml")
	cfg := &config.Config{
		DefaultEnvironmentName:  "default",
		SelectedEnvironmentName: "default",
		Environments: map[string]config.Environment{
			"default": {Connections: &config.Connections{}},
		},
	}
	require.NoError(t, svc.AddConnection(cfg, UpsertWorkspaceConnectionParams{
		EnvironmentName: "default",
		Name:            "warehouse",
		Type:            "duckdb",
		Values:          map[string]any{"path": "warehouse.duckdb"},
	}))

	err := svc.UpdateConnection(cfg, UpsertWorkspaceConnectionParams{
		EnvironmentName: "default",
		CurrentName:     "warehouse",
		Name:            "warehouse",
		Type:            "postgres",
		Values: map[string]any{
			"host": "localhost", "port": 5432, "database": "analytics",
			"username": "renart", "password": "renart",
		},
	})
	require.ErrorContains(t, err, "connection type is immutable")
	environment := cfg.Environments["default"]
	assert.Equal(t, "duckdb", environment.Connections.ConnectionsSummaryList()["warehouse"])
}

func TestAddConnectionOmitsBlankOptionalIntegerValues(t *testing.T) {
	t.Parallel()
	svc := NewConfigService("/tmp/workspace", "/tmp/workspace/.bruin.yml")
	cfg := &config.Config{
		DefaultEnvironmentName:  "default",
		SelectedEnvironmentName: "default",
		Environments: map[string]config.Environment{
			"default": {Connections: &config.Connections{}},
		},
	}

	require.NoError(t, svc.AddConnection(cfg, UpsertWorkspaceConnectionParams{
		EnvironmentName: "default",
		Name:            "warehouse",
		Type:            "duckdb",
		Values: map[string]any{
			"path":                  "warehouse.duckdb",
			"max_concurrent_assets": "",
		},
	}))

	environment := cfg.Environments["default"]
	require.Len(t, environment.Connections.DuckDB, 1)
	assert.Nil(t, environment.Connections.DuckDB[0].MaxConcurrentAssets)
}

func TestConnectionSecretsAreWriteOnlyAndUseExplicitChanges(t *testing.T) {
	t.Parallel()
	svc := NewConfigService("/tmp/workspace", "/tmp/workspace/.bruin.yml")
	cfg := &config.Config{
		DefaultEnvironmentName:  "default",
		SelectedEnvironmentName: "default",
		Environments: map[string]config.Environment{
			"default": {Connections: &config.Connections{}},
		},
	}

	err := svc.AddConnection(cfg, UpsertWorkspaceConnectionParams{
		EnvironmentName: "default",
		Name:            "warehouse",
		Type:            "postgres",
		Values: map[string]any{
			"host": "localhost", "port": 5432, "database": "analytics",
			"username": "renart", "password": "must-not-be-accepted",
		},
	})
	require.ErrorContains(t, err, `sensitive connection field "password" must use secret_changes`)

	require.NoError(t, svc.AddConnection(cfg, UpsertWorkspaceConnectionParams{
		EnvironmentName: "default",
		Name:            "warehouse",
		Type:            "postgres",
		Values: map[string]any{
			"host": "localhost", "port": 5432, "database": "analytics", "username": "renart",
		},
		SecretChanges: map[string]WorkspaceConnectionSecretChange{
			"password": {Action: "replace", Value: "write-only-canary"},
		},
	}))

	response := svc.BuildResponse("/tmp/workspace/.bruin.yml", cfg)
	require.Len(t, response.Environments, 1)
	require.Len(t, response.Environments[0].Connections, 1)
	connection := response.Environments[0].Connections[0]
	assert.NotContains(t, connection.Values, "password")
	assert.Equal(t, "configured", connection.SecretFields["password"].Status)
	payload, err := json.Marshal(response)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "write-only-canary")

	require.NoError(t, svc.UpdateConnection(cfg, UpsertWorkspaceConnectionParams{
		EnvironmentName: "default",
		CurrentName:     "warehouse",
		Name:            "warehouse",
		Type:            "postgres",
		Values: map[string]any{
			"host": "db.internal", "port": 5432, "database": "analytics", "username": "renart",
		},
		SecretChanges: map[string]WorkspaceConnectionSecretChange{
			"password": {Action: "keep"},
		},
	}))
	environment := cfg.Environments["default"]
	require.Len(t, environment.Connections.Postgres, 1)
	assert.Equal(t, "write-only-canary", environment.Connections.Postgres[0].Password)

	require.NoError(t, svc.UpdateConnection(cfg, UpsertWorkspaceConnectionParams{
		EnvironmentName: "default",
		CurrentName:     "warehouse",
		Name:            "warehouse",
		Type:            "postgres",
		Values: map[string]any{
			"host": "db.internal", "port": 5432, "database": "analytics", "username": "renart",
		},
		SecretChanges: map[string]WorkspaceConnectionSecretChange{
			"password": {Action: "clear"},
		},
	}))
	environment = cfg.Environments["default"]
	require.Len(t, environment.Connections.Postgres, 1)
	assert.Empty(t, environment.Connections.Postgres[0].Password)
}

func TestConnectionFieldMetadataClassifiesAndOmitsEveryTaggedSecret(t *testing.T) {
	t.Parallel()
	const canary = "renart-secret-api-canary"
	connectionTypes := make(map[string]WorkspaceConfigConnectionType)
	for _, connectionType := range BuildWorkspaceConfigConnectionTypes() {
		connectionTypes[connectionType.TypeName] = connectionType
	}

	connectionsType := reflect.TypeFor[config.Connections]()
	for index := 0; index < connectionsType.NumField(); index++ {
		connectionSetField := connectionsType.Field(index)
		if connectionSetField.Type.Kind() != reflect.Slice {
			continue
		}
		typeName := strings.Split(connectionSetField.Tag.Get("yaml"), ",")[0]
		elementType := connectionSetField.Type.Elem()
		if elementType.Kind() == reflect.Pointer {
			elementType = elementType.Elem()
		}
		if elementType.Kind() != reflect.Struct {
			continue
		}
		definition, exists := connectionTypes[typeName]
		require.True(t, exists, "missing connection definition for %s", typeName)
		definitionsByName := make(map[string]WorkspaceConfigFieldDef, len(definition.Fields))
		for _, field := range definition.Fields {
			definitionsByName[field.Name] = field
		}

		value := reflect.New(elementType)
		taggedFields := plantConnectionSecretCanary(value.Elem(), canary)
		for fieldName, sensitiveFile := range taggedFields {
			field, exists := definitionsByName[fieldName]
			require.True(t, exists, "%s.%s is tagged but absent from API metadata", typeName, fieldName)
			if sensitiveFile {
				assert.True(t, field.IsSensitiveFile, "%s.%s", typeName, fieldName)
			} else {
				assert.True(t, field.IsSensitive, "%s.%s", typeName, fieldName)
			}
		}

		publicValues := buildWorkspaceConfigConnectionValues(value.Interface(), typeName)
		payload, err := json.Marshal(publicValues)
		require.NoError(t, err)
		assert.NotContains(t, string(payload), canary, "connection type %s leaked a tagged field", typeName)
	}
}

func TestConnectionFieldsPutDetailsBeforeCredentialsAndTuningLast(t *testing.T) {
	t.Parallel()

	var postgres WorkspaceConfigConnectionType
	for _, connectionType := range BuildWorkspaceConfigConnectionTypes() {
		if connectionType.TypeName == "postgres" {
			postgres = connectionType
		}
		hasConcurrencySetting := false
		for _, field := range connectionType.Fields {
			hasConcurrencySetting = hasConcurrencySetting || field.Name == "max_concurrent_assets"
		}
		if hasConcurrencySetting {
			assert.Equal(
				t,
				"max_concurrent_assets",
				connectionType.Fields[len(connectionType.Fields)-1].Name,
				"%s should put its shared concurrency setting last",
				connectionType.TypeName,
			)
		}
	}
	require.NotEmpty(t, postgres.Fields)
	indexByName := make(map[string]int, len(postgres.Fields))
	for index, field := range postgres.Fields {
		indexByName[field.Name] = index
	}
	assert.Less(t, indexByName["host"], indexByName["username"])
	assert.Less(t, indexByName["database"], indexByName["password"])
	assert.Less(t, indexByName["password"], indexByName["pool_max_conns"])
}

func TestConnectionTypeCategoriesExposeObjectStorageWithoutIngestr(t *testing.T) {
	t.Parallel()
	categories := make(map[string]string)
	for _, connectionType := range BuildWorkspaceConfigConnectionTypes() {
		categories[connectionType.TypeName] = connectionType.Category
	}

	assert.Equal(t, "storage", categories["s3"])
	assert.Equal(t, "storage", categories["gcs"])
	assert.Equal(t, "warehouse", categories["postgres"])
	assert.Equal(t, "source", categories["stripe"])
}

func TestWorkspaceConnectionErrorsAreRedacted(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		DefaultEnvironmentName:  "default",
		SelectedEnvironmentName: "default",
		Environments: map[string]config.Environment{
			"default": {
				Connections: &config.Connections{
					Postgres: []config.PostgresConnection{{
						ConnectionMetadata: config.ConnectionMetadata{Name: "warehouse"},
						Password:           "error-secret-canary",
					}},
				},
			},
		},
	}
	require.NoError(t, cfg.SelectEnvironment("default"))

	redacted := redactWorkspaceConnectionMessage(
		cfg,
		"driver failed for postgres://renart:error-secret-canary@db.internal/analytics",
	)
	assert.NotContains(t, redacted, "error-secret-canary")
	assert.Contains(t, redacted, "****")
}

func plantConnectionSecretCanary(value reflect.Value, canary string) map[string]bool {
	result := make(map[string]bool)
	valueType := value.Type()
	for index := 0; index < value.NumField(); index++ {
		structField := valueType.Field(index)
		if !structField.IsExported() {
			continue
		}
		fieldValue := value.Field(index)
		if structField.Anonymous {
			embedded := fieldValue
			if embedded.Kind() == reflect.Pointer {
				if embedded.IsNil() {
					embedded.Set(reflect.New(embedded.Type().Elem()))
				}
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				for name, isFile := range plantConnectionSecretCanary(embedded, canary) {
					result[name] = isFile
				}
			}
		}
		isSensitive := structField.Tag.Get("sensitive") == "true"
		isSensitiveFile := structField.Tag.Get("sensitive_file") == "true"
		if (!isSensitive && !isSensitiveFile) || fieldValue.Kind() != reflect.String {
			continue
		}
		fieldValue.SetString(canary)
		fieldName := strings.Split(structField.Tag.Get("mapstructure"), ",")[0]
		if fieldName != "" {
			result[fieldName] = isSensitiveFile
		}
	}
	return result
}

func TestSetProjectRetentionPersistsValidatedTrackedSettings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := NewConfigService(root, filepath.Join(root, ".bruin.yml"))

	project, err := svc.SetProjectRetention(WorkspaceRetentionSettings{
		RunMetadata:               WorkspaceRetentionWindow{Days: 60, MinimumPerPipeline: 12},
		FullLogs:                  WorkspaceRetentionWindow{Days: 14, MinimumPerPipeline: 5},
		MaterializationFactsDays:  45,
		ScheduleHistoryDays:       120,
		Deployments:               WorkspaceRetentionWindow{Days: 30, MinimumPerPipeline: 7},
		TemporaryDirectoriesHours: 48,
	})
	require.NoError(t, err)
	require.NotNil(t, project.Retention)
	assert.Equal(t, 60, project.Retention.RunMetadata.Days)

	loaded, err := identity.LoadProject(
		afero.NewOsFs(),
		filepath.Join(root, ".renart", "project.yml"),
	)
	require.NoError(t, err)
	require.NotNil(t, loaded.Retention)
	assert.Equal(t, identity.RetentionWindow{Days: 14, MinimumPerPipeline: 5}, loaded.Retention.FullLogs)
	assert.Equal(t, 48, loaded.Retention.TemporaryDirectoriesHours)

	_, err = svc.SetProjectRetention(WorkspaceRetentionSettings{
		RunMetadata: WorkspaceRetentionWindow{Days: -1},
	})
	require.ErrorContains(t, err, "run metadata retention")
}
