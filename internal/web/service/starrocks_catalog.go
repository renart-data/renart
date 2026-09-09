package service

import (
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/starrocks"
	"github.com/go-sql-driver/mysql"
)

// Keep the native *starrocks.Client (Bruin's operator requires that concrete
// type), replacing only its DSN configuration. Every new physical connection
// receives these defaults; no one-off SET is sent to an arbitrary pooled session.
type starRocksCatalogConfig struct{ starrocks.Config }

func (c starRocksCatalogConfig) ToDBConnectionURI() (string, error) {
	if strings.ContainsAny(c.Catalog+c.Database, "\x00\r\n\\") {
		return "", fmt.Errorf("invalid StarRocks catalog or database")
	}
	dsn, err := c.Config.ToDBConnectionURI()
	if err != nil {
		return "", err
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", err
	}
	if c.Catalog != "" {
		if c.Database != "" {
			// StarRocks accepts catalog.database in the MySQL initial-database field.
			parsed.DBName = c.Catalog + "." + c.Database
		} else {
			// The catalog session variable is supported since StarRocks 3.2.4. Driver
			// parameters initialize every new connection, including a growing pool.
			if parsed.Params == nil {
				parsed.Params = map[string]string{}
			}
			parsed.Params["catalog"] = "'" + strings.ReplaceAll(c.Catalog, "'", "''") + "'"
		}
	}
	return parsed.FormatDSN(), nil
}

type catalogConnectionManager struct {
	config.ConnectionAndDetailsGetter
	starRocks map[string]*starrocks.Client
}

func (m *catalogConnectionManager) GetConnection(name string) any {
	if client := m.starRocks[name]; client != nil {
		return client
	}
	return m.ConnectionAndDetailsGetter.GetConnection(name)
}
func withStarRocksCatalogs(manager config.ConnectionAndDetailsGetter, cfg *config.Config) (config.ConnectionAndDetailsGetter, error) {
	if cfg == nil || cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return manager, nil
	}
	clients := map[string]*starrocks.Client{}
	for _, c := range cfg.SelectedEnvironment.Connections.StarRocks {
		if c.Catalog == "" {
			continue
		}
		native := starRocksCatalogConfig{Config: starrocks.Config{Username: c.Username, Password: c.Password, Host: c.Host, Port: c.Port, Database: c.Database, Catalog: c.Catalog, SSL: c.SSL, HTTPPort: c.HTTPPort, ReplicationNum: c.ReplicationNum}}
		if _, err := native.ToDBConnectionURI(); err != nil {
			return nil, err
		}
		client, err := starrocks.NewClient(native)
		if err != nil {
			return nil, err
		}
		clients[c.Name] = client
	}
	if len(clients) == 0 {
		return manager, nil
	}
	return &catalogConnectionManager{ConnectionAndDetailsGetter: manager, starRocks: clients}, nil
}
