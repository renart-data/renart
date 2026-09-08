package service

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/spf13/afero"
	"renart/internal/web/policy"
)

func nativeConnectionReadOnly(connection any) bool {
	var duck config.DuckDBConnection
	switch value := connection.(type) {
	case *config.DuckDBConnection:
		if value == nil {
			return false
		}
		duck = *value
	case config.DuckDBConnection:
		duck = value
	default:
		return false
	}
	if duck.ReadOnly {
		return true
	}
	_, query, found := strings.Cut(duck.Path, "?")
	if !found {
		return false
	}
	values, err := url.ParseQuery(query)
	return err == nil && strings.EqualFold(values.Get("access_mode"), "read_only")
}

func (s *ConfigService) environmentPolicyPath() string {
	return filepath.Join(s.workspaceRoot, ".renart", "environments.yml")
}

// ConnectionAccessModes is a secret-free projection for discovery clients.
func (s *ConfigService) ConnectionAccessModes(environment string) (map[string]policy.AccessMode, error) {
	cfg, err := loadSelectedConfigReadOnlyFS(afero.NewOsFs(), s.configPath, environment)
	if err != nil {
		return nil, err
	}
	p, _, err := connectionEnvironmentPolicy(s.workspaceRoot, cfg)
	if err != nil {
		return nil, err
	}
	modes := map[string]policy.AccessMode{}
	for name := range selectedConnectionSummaries(cfg) {
		connection, _ := selectedConfigurationConnection(cfg, name)
		modes[name] = policy.EffectiveMode(p, name, nativeConnectionReadOnly(connection))
	}
	return modes, nil
}

func (s *ConfigService) lockConfiguration() (func(), error) {
	unlock, err := policy.Lock(s.environmentPolicyPath())
	if err != nil {
		return nil, err
	}
	if err := policy.CheckPending(s.environmentPolicyPath()); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

type configurationPolicyChange func(*policy.Config) error

func environmentPolicyChange(source, target string, removeSource bool) configurationPolicyChange {
	return func(cfg *policy.Config) error {
		if source == target {
			return nil
		}
		if target != "" {
			cfg.Environments[target] = cfg.For(source)
		}
		if removeSource {
			delete(cfg.Environments, source)
		}
		return nil
	}
}

func connectionPolicyChange(environment, source, target string, mode *policy.AccessMode) configurationPolicyChange {
	return func(cfg *policy.Config) error {
		environment, source, target = strings.TrimSpace(environment), strings.TrimSpace(source), strings.TrimSpace(target)
		p := cfg.For(environment)
		if p.Connections == nil {
			p.Connections = map[string]policy.ConnectionPolicy{}
		}
		if source != target {
			if entry, ok := p.Connections[source]; ok && target != "" {
				p.Connections[target] = entry
			}
			delete(p.Connections, source)
		}
		if mode != nil {
			if *mode != policy.ReadOnly && *mode != policy.ReadWrite {
				return fmt.Errorf("invalid access_mode %q", *mode)
			}
			p.Connections[target] = policy.ConnectionPolicy{AccessMode: *mode}
		}
		cfg.Environments[environment] = p
		return nil
	}
}
