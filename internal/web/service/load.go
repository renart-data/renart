package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	bruinuv "github.com/bruin-data/bruin/pkg/uv"
	"gopkg.in/yaml.v3"
)

const (
	loadAssetType            = "load"
	defaultSlingPackage      = "sling==1.5.22"
	defaultDatabricksPort    = 443
	slingLoadedAtColumn      = "_sling_loaded_at"
	slingLoadedAtDisabledEnv = "SLING_LOADED_AT_COLUMN=false"
	slingSourceConnectionEnv = "RENART_SLING_SOURCE"
	slingTargetConnectionEnv = "RENART_SLING_TARGET"
)

// ingestrURIConnection is the bruin connection capability that yields a standard
// connection URI (e.g. postgresql://…, s3://…, duckdb://…). The method name comes
// from bruin; the URI it returns is a plain DSN that the Sling CLI also resolves,
// so renart reuses it to bridge a named bruin connection to a Sling --src-conn /
// --tgt-conn value (and to feed `sling conns discover`).
type ingestrURIConnection interface {
	GetIngestrURI() (string, error)
}

// loadConnectionURI resolves a named bruin connection to a Sling-usable
// connection URI. It works for any direction (source, target, discovery).
func loadConnectionURI(manager config.ConnectionGetter, connectionName string) (string, error) {
	uri, _, err := loadConnectionURIWithWarning(manager, connectionName)
	return uri, err
}

// loadConnectionURIWithWarning applies compatibility changes that belong only
// at the Sling boundary. The authored Bruin connection and every direct
// warehouse execution path retain their configured values.
func loadConnectionURIWithWarning(manager config.ConnectionGetter, connectionName string) (string, string, error) {
	uri, err := resolveLoadConnectionURI(manager, connectionName)
	if err != nil {
		return "", "", err
	}
	normalized, changed := normalizeSlingPostgresSSLMode(uri)
	if !changed {
		return normalized, "", nil
	}
	warning := fmt.Sprintf(
		"Warning: Sling does not support PostgreSQL sslmode %q; using %q for connection %q. Set ssl_mode on this connection to a Sling-supported value to choose a different policy.",
		"allow", "verify-ca", strings.TrimSpace(connectionName),
	)
	return normalized, warning, nil
}

func resolveLoadConnectionURI(manager config.ConnectionGetter, connectionName string) (string, error) {
	if manager == nil {
		return "", errors.New("connection manager is required")
	}
	name := strings.TrimSpace(connectionName)
	conn, err := resolveRuntimeConnection(manager, name)
	if err != nil {
		return "", err
	}
	if conn == nil {
		return "", fmt.Errorf("connection %q was not found", name)
	}
	if details, ok := manager.(config.ConnectionDetailsGetter); ok {
		switch connection := details.GetConnectionDetails(name).(type) {
		case *config.ClickHouseConnection:
			return slingClickHouseConnectionURI(*connection)
		case config.ClickHouseConnection:
			return slingClickHouseConnectionURI(connection)
		case *config.DatabricksConnection:
			return slingDatabricksConnectionPayload(*connection)
		case config.DatabricksConnection:
			return slingDatabricksConnectionPayload(connection)
		case *config.DuckDBConnection:
			if connection.Lakehouse != nil {
				return slingDuckLakeConnectionURI(*connection)
			}
		case config.DuckDBConnection:
			if connection.Lakehouse != nil {
				return slingDuckLakeConnectionURI(connection)
			}
		case *config.StarRocksConnection:
			return slingStarRocksConnectionURI(*connection)
		case config.StarRocksConnection:
			return slingStarRocksConnectionURI(connection)
		case *config.TrinoConnection:
			return slingTrinoConnectionURI(*connection)
		case config.TrinoConnection:
			return slingTrinoConnectionURI(connection)
		}
	}
	if uriGetter, ok := conn.(ingestrURIConnection); ok {
		uri, err := uriGetter.GetIngestrURI()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(uri) == "" {
			return "", fmt.Errorf("connection %q produced an empty URI", name)
		}
		return uri, nil
	}
	if raw, ok := conn.(string); ok && strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw), nil
	}
	return "", fmt.Errorf("connection %q cannot be converted to a Load connection URI", name)
}

// Bruin's ingestr URI uses Databricks' HTTP path as a query option and omits
// the port. Sling's native Databricks driver expects the SQL warehouse or
// cluster path in the DSN path and requires a port. Keep the driver-native URL
// inside Sling's structured connection payload so PAT and OAuth M2M both work,
// while target execution separately disables Sling bulk loading so ordinary
// API, Seed, Load, and Python materialization flows do not require a Unity
// Catalog staging volume.
func slingDatabricksConnectionPayload(connection config.DatabricksConnection) (string, error) {
	name := strings.TrimSpace(connection.Name)
	host := strings.TrimSpace(connection.Host)
	if host == "" {
		return "", fmt.Errorf("Databricks connection %q requires a host for Sling", name)
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, ":/?#@") {
		return "", fmt.Errorf("Databricks connection %q host must be a hostname without a scheme, port, or path", name)
	}

	path := strings.TrimLeft(strings.TrimSpace(connection.Path), "/")
	if path == "" {
		return "", fmt.Errorf("Databricks connection %q requires an HTTP path for Sling", name)
	}
	if strings.ContainsAny(path, "?#") {
		return "", fmt.Errorf("Databricks connection %q HTTP path cannot contain a query or fragment", name)
	}

	port := connection.Port
	if port == 0 {
		port = defaultDatabricksPort
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("Databricks connection %q has invalid port %d for Sling", name, port)
	}

	clientID := strings.TrimSpace(connection.ClientID)
	clientSecret := strings.TrimSpace(connection.ClientSecret)
	if (clientID == "") != (clientSecret == "") {
		return "", fmt.Errorf("Databricks connection %q requires both client_id and client_secret for OAuth M2M", name)
	}

	u := &url.URL{
		Scheme: "databricks",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + path,
	}
	query := url.Values{}
	if catalog := strings.TrimSpace(connection.Catalog); catalog != "" {
		query.Set("catalog", catalog)
	}
	if schema := strings.TrimSpace(connection.Schema); schema != "" {
		query.Set("schema", schema)
	}
	if clientID != "" {
		query.Set("authType", "OAuthM2M")
		query.Set("clientID", clientID)
		query.Set("clientSecret", clientSecret)
	} else {
		if strings.TrimSpace(connection.Token) == "" {
			return "", fmt.Errorf("Databricks connection %q requires a token or OAuth M2M client credentials for Sling", name)
		}
		u.User = url.UserPassword("token", connection.Token)
	}
	u.RawQuery = query.Encode()

	payload, err := json.Marshal(map[string]any{
		"type": "databricks",
		"url":  u.String(),
	})
	if err != nil {
		return "", fmt.Errorf("encode Databricks connection %q for Sling: %w", name, err)
	}
	return string(payload), nil
}

// Sling's PostgreSQL driver does not accept libpq's opportunistic `allow`
// mode. At this boundary only, prefer the supported mode that also verifies the
// server's certificate authority rather than weakening the connection.
func normalizeSlingPostgresSSLMode(rawURI string) (string, bool) {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return rawURI, false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "postgres" && scheme != "postgresql" {
		return rawURI, false
	}

	query := parsed.Query()
	sslModeKey := ""
	for key, values := range query {
		if strings.EqualFold(key, "sslmode") && len(values) > 0 && strings.EqualFold(strings.TrimSpace(values[0]), "allow") {
			sslModeKey = key
			break
		}
	}
	if sslModeKey == "" {
		return rawURI, false
	}

	query.Del(sslModeKey)
	query.Set("sslmode", "verify-ca")
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}

func writeSlingConnectionWarning(writer io.Writer, warning string) {
	if writer == nil || warning == "" {
		return
	}
	_, _ = fmt.Fprintln(writer, warning)
}

// Bruin's ClickHouse ingestr URI includes http_port as a native-driver query
// setting. Sling forwards unknown query parameters to ClickHouse, where that
// property is rejected. Its native connection contract uses the database path
// and only needs the secure query flag when TLS is enabled.
func slingClickHouseConnectionURI(connection config.ClickHouseConnection) (string, error) {
	database := strings.TrimSpace(connection.Database)
	if database == "" {
		return "", fmt.Errorf("ClickHouse connection %q requires a database for Sling", connection.Name)
	}

	u := &url.URL{
		Scheme: "clickhouse",
		Host:   net.JoinHostPort(strings.TrimSpace(connection.Host), strconv.Itoa(connection.Port)),
		Path:   "/" + database,
	}
	username := strings.TrimSpace(connection.Username)
	if username != "" {
		if connection.Password == "" {
			u.User = url.User(username)
		} else {
			u.User = url.UserPassword(username, connection.Password)
		}
	}
	if connection.Secure != nil && *connection.Secure == 1 {
		u.RawQuery = url.Values{"secure": []string{"true"}}.Encode()
	}
	return u.String(), nil
}

// The DuckLake connection keys accepted by Sling intentionally differ from the
// serialized lakehouse keys used by the upstream Go connection. Keep that
// translation at the Sling boundary so direct DuckDB/DuckLake execution keeps
// using the structured connection unchanged.
func slingDuckLakeConnectionURI(connection config.DuckDBConnection) (string, error) {
	lakehouse := connection.Lakehouse
	if lakehouse == nil || lakehouse.Format != config.LakehouseFormatDuckLake {
		return "", fmt.Errorf("DuckDB connection %q is not configured as DuckLake", connection.Name)
	}

	query := url.Values{
		"catalog_type": []string{string(lakehouse.Catalog.Type)},
	}
	switch lakehouse.Catalog.Type {
	case config.CatalogTypePostgres:
		catalog := lakehouse.Catalog
		if strings.TrimSpace(catalog.Host) == "" || catalog.Port == 0 || strings.TrimSpace(catalog.Database) == "" {
			return "", fmt.Errorf("DuckLake connection %q requires PostgreSQL catalog host, port, and database for Sling", connection.Name)
		}
		query.Set("catalog_conn_string", postgresCatalogConnectionURI(catalog))
	case config.CatalogTypeDuckDB, config.CatalogTypeSQLite:
		path := strings.TrimSpace(lakehouse.Catalog.Path)
		if path == "" {
			return "", fmt.Errorf("DuckLake connection %q requires a catalog path for Sling", connection.Name)
		}
		query.Set("catalog_conn_string", path)
	default:
		return "", fmt.Errorf("DuckLake connection %q has unsupported Sling catalog type %q", connection.Name, lakehouse.Catalog.Type)
	}

	storage := lakehouse.Storage
	if storage.Path != "" {
		query.Set("data_path", storage.Path)
	}
	if storage.Region != "" {
		query.Set("s3_region", storage.Region)
	}
	if storage.Endpoint != "" {
		query.Set("s3_endpoint", storage.Endpoint)
	}
	if storage.URLStyle != "" {
		query.Set("url_style", storage.URLStyle)
	}
	if storage.UseSSL != nil {
		query.Set("use_ssl", strconv.FormatBool(*storage.UseSSL))
	}
	switch storage.Type {
	case config.StorageTypeS3:
		if storage.Auth.AccessKey != "" {
			query.Set("s3_access_key_id", storage.Auth.AccessKey)
		}
		if storage.Auth.SecretKey != "" {
			query.Set("s3_secret_access_key", storage.Auth.SecretKey)
		}
		if storage.Auth.SessionToken != "" {
			query.Set("s3_session_token", storage.Auth.SessionToken)
		}
	case config.StorageTypeGCS:
		if storage.Auth.AccessKey != "" {
			query.Set("gcs_access_key_id", storage.Auth.AccessKey)
		}
		if storage.Auth.SecretKey != "" {
			query.Set("gcs_secret_access_key", storage.Auth.SecretKey)
		}
	default:
		return "", fmt.Errorf("DuckLake connection %q has unsupported Sling storage type %q", connection.Name, storage.Type)
	}

	return "ducklake://?" + query.Encode(), nil
}

// Sling expects the Stream Load endpoint as a top-level fe_url connection
// property. Putting it in the URL query makes the MySQL driver treat it as a
// StarRocks session variable, so use Sling's environment-variable JSON form.
// replication_num remains native-only and must not cross this boundary either.
func slingStarRocksConnectionURI(connection config.StarRocksConnection) (string, error) {
	host := strings.TrimSpace(connection.Host)
	database := strings.TrimSpace(connection.Database)
	if host == "" || database == "" {
		return "", fmt.Errorf("StarRocks connection %q requires a host and database for Sling", connection.Name)
	}
	port := connection.Port
	if port == 0 {
		port = 9030
	}
	properties := map[string]any{
		"type":     "starrocks",
		"host":     host,
		"port":     port,
		"database": database,
	}
	if connection.Username != "" {
		properties["user"] = connection.Username
	}
	if connection.Password != "" {
		properties["password"] = connection.Password
	}
	if connection.HTTPPort != 0 {
		httpScheme := "http"
		if strings.EqualFold(strings.TrimSpace(connection.SSL), "true") {
			httpScheme = "https"
		}
		properties["fe_url"] = (&url.URL{
			Scheme: httpScheme,
			Host:   net.JoinHostPort(host, strconv.Itoa(connection.HTTPPort)),
		}).String()
	}
	payload, err := json.Marshal(properties)
	if err != nil {
		return "", fmt.Errorf("encode StarRocks connection %q for Sling: %w", connection.Name, err)
	}
	return string(payload), nil
}

func postgresCatalogConnectionURI(catalog config.CatalogConfig) string {
	connectionURI := &url.URL{
		Scheme: "postgresql",
		Host:   net.JoinHostPort(strings.TrimSpace(catalog.Host), strconv.Itoa(catalog.Port)),
		Path:   "/" + strings.TrimPrefix(strings.TrimSpace(catalog.Database), "/"),
	}
	if catalog.Auth.Username != "" {
		if catalog.Auth.Password == "" {
			connectionURI.User = url.User(catalog.Auth.Username)
		} else {
			connectionURI.User = url.UserPassword(catalog.Auth.Username, catalog.Auth.Password)
		}
	}
	return connectionURI.String()
}

// Bruin's Trino ingestr URI encodes the catalog as a path segment. Sling's
// Trino connector instead requires catalog (and optionally schema) as query
// properties, so build its URI from the retained connection details.
func slingTrinoConnectionURI(connection config.TrinoConnection) (string, error) {
	catalog := strings.TrimSpace(connection.Catalog)
	if catalog == "" {
		return "", fmt.Errorf("Trino connection %q requires a catalog for Sling", connection.Name)
	}

	u := &url.URL{
		Scheme: "trino",
		Host:   net.JoinHostPort(strings.TrimSpace(connection.Host), strconv.Itoa(connection.Port)),
	}
	username := strings.TrimSpace(connection.Username)
	if username != "" {
		if connection.Password == "" {
			u.User = url.User(username)
		} else {
			u.User = url.UserPassword(username, connection.Password)
		}
	}
	query := url.Values{"catalog": []string{catalog}}
	if schema := strings.TrimSpace(connection.Schema); schema != "" {
		query.Set("schema", schema)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

var loadUvChecker = &bruinuv.Checker{}

func isLoadAssetType(assetType string) bool {
	return strings.EqualFold(strings.TrimSpace(assetType), loadAssetType)
}

func isLoadAsset(asset *pipeline.Asset) bool {
	return asset != nil && isLoadAssetType(string(asset.Type))
}

// Flat parameter keys for a Load asset. They live under the asset's
// `parameters:` (bruin parses that as map[string]string, so they must be flat),
// which keeps a Load asset a single bruin-loadable .asset.yml — no .sling.yml
// replication sidecar.
const (
	loadParamSourceConnection  = "source_connection"
	loadParamSourceTable       = "source_table"
	loadParamDestinationObject = "destination_object"
)

// loadRunParams is the resolved, flat replication intent of a Load asset.
type loadRunParams struct {
	SourceConnection      string
	SourceTable           string
	DestinationConnection string
	DestinationObject     string
	AssetName             string
}

// loadParamsFromAsset reads the flat replication parameters off an asset.
func loadParamsFromAsset(asset *pipeline.Asset) loadRunParams {
	params := loadRunParams{}
	if asset == nil {
		return params
	}
	get := func(key string) string {
		value, _ := asset.Parameters.GetString(key)
		return strings.TrimSpace(value)
	}
	params.SourceConnection = get(loadParamSourceConnection)
	params.SourceTable = get(loadParamSourceTable)
	params.DestinationConnection = strings.TrimSpace(asset.Connection)
	params.DestinationObject = get(loadParamDestinationObject)
	params.AssetName = strings.TrimSpace(asset.Name)
	return params
}

func resolvedLoadParams(asset *pipeline.Asset, pl *pipeline.Pipeline) (loadRunParams, error) {
	params := loadParamsFromAsset(asset)
	connectionName, err := loadConnectionNameForAsset(asset, pl)
	if err != nil {
		return params, err
	}
	params.DestinationConnection = connectionName
	return params, nil
}

// loadLocalConnectionName is the synthetic "connection" that marks a Load
// source or target as a local file on the same machine. It is NOT a bruin
// connection: the file path lives in the corresponding _table parameter and the
// run omits --src-conn/--tgt-conn, letting Load resolve the file:// stream.
const loadLocalConnectionName = "local"

func isLocalLoadConnection(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), loadLocalConnectionName)
}

// loadFileStreamURI turns a (possibly workspace-relative) file path into a
// file:// URI Load can read/write. Paths that already carry a scheme (file://,
// s3://, …) pass through unchanged.
func loadFileStreamURI(workspaceRoot, rawPath string) string {
	path := strings.TrimSpace(rawPath)
	if strings.Contains(path, "://") {
		return path
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspaceRoot, path)
	}
	return "file://" + filepath.ToSlash(path)
}

// loadSourceArgs builds the --src-* flags for a run: a file:// stream (no
// connection) for a local source, otherwise the bridged connection URI + stream.
func (e *HybridBruinExecutor) loadSourceArgs(manager config.ConnectionGetter, params loadRunParams) ([]string, error) {
	return e.loadSourceArgsWithWarning(manager, params, nil)
}

func (e *HybridBruinExecutor) loadSourceArgsWithWarning(manager config.ConnectionGetter, params loadRunParams, reportWarning func(string)) ([]string, error) {
	if isLocalLoadConnection(params.SourceConnection) {
		if params.SourceTable == "" {
			return nil, errors.New("a local load source requires a source_table file path")
		}
		return []string{"--src-stream", loadFileStreamURI(e.workspaceRoot, params.SourceTable)}, nil
	}
	if params.SourceConnection == "" {
		return nil, errors.New("load asset requires a source_connection parameter")
	}
	if params.SourceTable == "" {
		return nil, errors.New("load asset requires a source_table parameter")
	}
	uri, warning, err := loadConnectionURIWithWarning(manager, params.SourceConnection)
	if err != nil {
		return nil, err
	}
	if reportWarning != nil && warning != "" {
		reportWarning(warning)
	}
	return []string{"--src-conn", uri, "--src-stream", params.SourceTable}, nil
}

// loadTargetArgs builds the --tgt-* flags for a run. Database destinations are
// always named after the asset; file and object-storage destinations use the
// explicit destination_object parameter.
func (e *HybridBruinExecutor) loadTargetArgs(manager config.ConnectionGetter, params loadRunParams) ([]string, error) {
	return e.loadTargetArgsWithWarning(manager, params, nil)
}

func (e *HybridBruinExecutor) loadTargetArgsWithWarning(manager config.ConnectionGetter, params loadRunParams, reportWarning func(string)) ([]string, error) {
	if isLocalLoadConnection(params.DestinationConnection) {
		if params.DestinationObject == "" {
			return nil, errors.New("a local load target requires a destination_object file path")
		}
		return []string{"--tgt-object", loadFileStreamURI(e.workspaceRoot, params.DestinationObject)}, nil
	}
	if params.DestinationConnection == "" {
		return nil, errors.New("load asset requires a target connection")
	}
	uri, warning, err := loadConnectionURIWithWarning(manager, params.DestinationConnection)
	if err != nil {
		return nil, err
	}
	if reportWarning != nil && warning != "" {
		reportWarning(warning)
	}
	targetObject := strings.TrimSpace(params.AssetName)
	if details, ok := manager.(config.ConnectionDetailsGetter); ok {
		connectionType := details.GetConnectionType(params.DestinationConnection)
		switch loadConnectionCategory(connectionType) {
		case LoadCategoryDatabase:
			// The database table is the asset's canonical name.
		case LoadCategoryStorage, LoadCategoryFile:
			targetObject = strings.TrimSpace(params.DestinationObject)
			if targetObject == "" {
				return nil, fmt.Errorf("load target connection %q requires a destination_object", params.DestinationConnection)
			}
		default:
			return nil, fmt.Errorf("connection %q is not a supported Load target", params.DestinationConnection)
		}
	} else if destinationObject := strings.TrimSpace(params.DestinationObject); destinationObject != "" {
		// Minimal test/custom managers cannot report a category. Preserve their
		// ability to target an explicit object while real managers enforce the
		// database-vs-storage distinction above.
		targetObject = destinationObject
	}
	if targetObject == "" {
		return nil, errors.New("load asset requires a name for its destination table")
	}
	return []string{"--tgt-conn", uri, "--tgt-object", targetObject}, nil
}

type loadAssetYAML struct {
	Type            string                       `yaml:"type"`
	Connection      string                       `yaml:"connection,omitempty"`
	Depends         []string                     `yaml:"depends,omitempty"`
	Parameters      loadAssetParametersYAML      `yaml:"parameters"`
	Materialization loadAssetMaterializationYAML `yaml:"materialization"`
}

type loadAssetParametersYAML struct {
	SourceConnection  string `yaml:"source_connection"`
	SourceTable       string `yaml:"source_table"`
	DestinationObject string `yaml:"destination_object,omitempty"`
}

type loadAssetMaterializationYAML struct {
	Type     string `yaml:"type"`
	Strategy string `yaml:"strategy"`
}

func renderLoadAssetContent(connection, sourceConnection, sourceTable, destinationObject string, depends []string) (string, error) {
	if strings.TrimSpace(sourceConnection) == "" {
		return "", errors.New("load asset requires a source connection")
	}
	if strings.TrimSpace(sourceTable) == "" {
		return "", errors.New("load asset requires a source table or object")
	}
	if isLocalLoadConnection(connection) && strings.TrimSpace(destinationObject) == "" {
		return "", errors.New("a local load target requires a destination object")
	}
	definition := loadAssetYAML{
		Type:       loadAssetType,
		Connection: strings.TrimSpace(connection),
		Depends:    depends,
		Parameters: loadAssetParametersYAML{
			SourceConnection:  strings.TrimSpace(sourceConnection),
			SourceTable:       strings.TrimSpace(sourceTable),
			DestinationObject: strings.TrimSpace(destinationObject),
		},
		Materialization: loadAssetMaterializationYAML{
			Type:     "table",
			Strategy: "create+replace",
		},
	}
	content, err := yaml.Marshal(definition)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// defaultLoadAssetContent is used by non-interactive callers that need a
// starter document. The create dialog sends concrete semantic fields instead.
func defaultLoadAssetContent(assetName string) string {
	leaf := assetNameLeafPath(assetName)
	content, _ := renderLoadAssetContent(
		"your_destination_connection",
		"your_source_connection",
		"public."+leaf,
		"",
		nil,
	)
	return content
}

func loadBinaryPath() string {
	if value := strings.TrimSpace(os.Getenv("RENART_SLING_BINARY")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("SLING_BINARY")); value != "" {
		return value
	}
	return ""

}

func loadPackageName() string {
	if value := strings.TrimSpace(os.Getenv("RENART_SLING_PACKAGE")); value != "" {
		return value
	}
	return defaultSlingPackage
}

func loadUvBinaryPath(ctx context.Context, output io.Writer) (string, error) {
	if value := strings.TrimSpace(os.Getenv("RENART_UV_BINARY")); value != "" {
		return value, nil
	}
	if output != nil {
		ctx = context.WithValue(ctx, bruinexecutor.KeyPrinter, output)
	}
	return loadUvChecker.EnsureUvInstalled(ctx)
}

func loadCommand(ctx context.Context, loadArgs []string, output io.Writer) (string, []string, error) {
	if binaryPath := loadBinaryPath(); binaryPath != "" {
		return binaryPath, loadArgs, nil
	}
	uvBinaryPath, err := loadUvBinaryPath(ctx, output)
	if err != nil {
		return "", nil, err
	}
	cmdline := []string{
		"tool",
		"run",
		"--no-config",
		"--python",
		"3.11",
		"--from",
		loadPackageName(),
		"sling",
	}
	return uvBinaryPath, append(cmdline, loadArgs...), nil
}

// slingCommandConnectionEnv keeps credentials out of argv and lets Sling parse
// both URL and structured YAML/JSON connection payloads through its documented
// environment-variable connection mechanism.
func slingCommandConnectionEnv(args []string) ([]string, []string) {
	normalized := append([]string(nil), args...)
	var env []string
	for i := 0; i+1 < len(normalized); i++ {
		var name string
		switch normalized[i] {
		case "--src-conn":
			name = slingSourceConnectionEnv
		case "--tgt-conn":
			name = slingTargetConnectionEnv
		default:
			continue
		}
		value := normalized[i+1]
		if value == "" {
			continue
		}
		normalized[i+1] = name
		env = append(env, name+"="+value)
		i++
	}
	return normalized, env
}

// slingTargetOptionsArgs applies destination-specific Sling behavior at the
// target-options boundary. In particular, Databricks' default bulk path creates
// and writes through a Unity Catalog volume. The ordinary Renart materializers
// use batched INSERTs instead so connecting a SQL warehouse does not also
// require volume privileges.
func slingTargetOptionsArgs(manager config.ConnectionGetter, connectionName string, base map[string]any) ([]string, error) {
	options := make(map[string]any, len(base)+1)
	for key, value := range base {
		options[key] = value
	}
	if details, ok := manager.(config.ConnectionDetailsGetter); ok && strings.EqualFold(details.GetConnectionType(connectionName), "databricks") {
		options["use_bulk"] = false
	}
	if len(options) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("encode Sling target options: %w", err)
	}
	return []string{"--tgt-options", string(payload)}, nil
}

func loadRunModeArgs(ctx context.Context) []string {
	fullRefresh, _ := ctx.Value(pipeline.RunConfigFullRefresh).(bool)
	if !fullRefresh {
		return nil
	}
	return []string{"--mode", "full-refresh"}
}

// slingMaterializationArgs maps Renart/Bruin materialization intent to Sling's
// loader flags. This is shared by native Load and HTTP API assets so the
// workbench never offers a strategy that silently executes as full refresh.
func slingMaterializationArgs(ctx context.Context, asset *pipeline.Asset) ([]string, error) {
	if asset == nil {
		return nil, errors.New("asset is required to resolve materialization")
	}
	strategy := strings.ToLower(strings.TrimSpace(string(asset.Materialization.Strategy)))
	if args := loadRunModeArgs(ctx); len(args) > 0 {
		if asset.RefreshRestricted == nil || !*asset.RefreshRestricted {
			// truncate+insert already reloads the complete source while preserving
			// the target relation. Replacing it with Sling full-refresh would swap
			// the table instead, breaking dependent Postgres views and grants.
			switch strategy {
			case "truncate+insert", "truncate_insert", "truncate":
				return []string{"--mode", "truncate"}, nil
			}
			return args, nil
		}
		addExecutionWarning(ctx, fmt.Sprintf("Full refresh is restricted for %s; running its configured materialization strategy instead.", asset.Name))
	}
	switch strategy {
	case "", "create+replace", "create_replace", "full-refresh", "full_refresh":
		return nil, nil
	case "truncate+insert", "truncate_insert", "truncate":
		return []string{"--mode", "truncate"}, nil
	case "append":
		if key := strings.TrimSpace(asset.Materialization.IncrementalKey); key != "" {
			return []string{"--mode", "incremental", "--update-key", key}, nil
		}
		return []string{"--mode", "snapshot"}, nil
	case "merge":
		primaryKeys := asset.ColumnNamesWithPrimaryKey()
		if len(primaryKeys) == 0 {
			return nil, errors.New("merge materialization needs at least one primary-key column")
		}
		args := []string{"--mode", "incremental", "--primary-key", strings.Join(primaryKeys, ",")}
		if key := strings.TrimSpace(asset.Materialization.IncrementalKey); key != "" {
			args = append(args, "--update-key", key)
		}
		return args, nil
	default:
		return nil, fmt.Errorf("materialization strategy %q is not supported for %s assets", strategy, asset.Type)
	}
}

func newStreamingCommand(ctx context.Context, name string, args []string, dir string, writer io.Writer) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), loadRunEnv(ctx)...)
	return cmd
}

func loadBaseEnv() []string {
	return []string{
		"SLING_DISABLE_TELEMETRY=true",
		// Renart materializations preserve the asset schema; Sling's optional
		// ingestion timestamp must not become an undeclared output column.
		slingLoadedAtDisabledEnv,
		"PYTHONUNBUFFERED=1",
		// Sling treats every inherited value containing :// as a potential
		// connection. DEBUGINFOD_URLS is unrelated and otherwise produces a
		// noisy "could not parse" warning before every invocation.
		"DEBUGINFOD_URLS=",
	}
}

func loadRunEnv(ctx context.Context) []string {
	env := loadBaseEnv()
	if start, ok := ctx.Value(pipeline.RunConfigStartDate).(time.Time); ok && !start.IsZero() {
		env = append(env, "START_DATE="+start.UTC().Format(time.RFC3339))
	}
	if end, ok := ctx.Value(pipeline.RunConfigEndDate).(time.Time); ok && !end.IsZero() {
		env = append(env, "END_DATE="+end.UTC().Format(time.RFC3339))
	}
	return env
}

func runStreamingCommand(cmd *exec.Cmd, writer *streamCaptureWriter) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(writer, stdout)
		done <- copyErr
	}()
	go func() {
		_, copyErr := io.Copy(writer, stderr)
		done <- copyErr
	}()
	copyErr1 := <-done
	copyErr2 := <-done
	waitErr := cmd.Wait()
	if copyErr1 != nil {
		return copyErr1
	}
	if copyErr2 != nil {
		return copyErr2
	}
	return waitErr
}

func (e *HybridBruinExecutor) runLoadAsset(ctx context.Context, pl *pipeline.Pipeline, asset *pipeline.Asset, manager config.ConnectionGetter, onChunk func([]byte)) ([]byte, error) {
	if asset == nil {
		return nil, errors.New("load asset is required")
	}
	writer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil), onChunk: onChunk}

	params, err := resolvedLoadParams(asset, pl)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	seenWarnings := make(map[string]struct{})
	reportWarning := func(warning string) {
		if _, ok := seenWarnings[warning]; ok {
			return
		}
		seenWarnings[warning] = struct{}{}
		writeSlingConnectionWarning(writer, warning)
	}
	srcArgs, err := e.loadSourceArgsWithWarning(manager, params, reportWarning)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	tgtArgs, err := e.loadTargetArgsWithWarning(manager, params, reportWarning)
	if err != nil {
		return writer.buffer.Bytes(), err
	}

	args := append([]string{"run"}, srcArgs...)
	args = append(args, tgtArgs...)
	modeArgs, err := slingMaterializationArgs(ctx, asset)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	args = append(args, modeArgs...)
	targetOptions, err := slingTargetOptionsArgs(manager, params.DestinationConnection, nil)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	args = append(args, targetOptions...)
	args, connectionEnv := slingCommandConnectionEnv(args)

	cmdName, cmdArgs, err := loadCommand(ctx, args, writer)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	cmd := newStreamingCommand(ctx, cmdName, cmdArgs, e.workspaceRoot, writer)
	cmd.Env = append(cmd.Env, connectionEnv...)
	lease, err := e.acquireDuckDBConnections(ctx, manager, []string{params.SourceConnection, params.DestinationConnection}, directTaskLeaseOwner(ctx, pl, asset), writer)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	defer lease.Release()
	if err := runStreamingCommand(cmd, writer); err != nil {
		return writer.buffer.Bytes(), err
	}
	return writer.buffer.Bytes(), nil
}
