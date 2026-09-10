package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"

	"renart/internal/web/policy"
	"renart/internal/web/secretstore"
)

// ConnectionDiscoveryEntry contains no values or secret references. The
// revision returned alongside these entries is an opaque, non-monotonic token.
type ConnectionDiscoveryEntry struct {
	Name       string
	Type       string
	AccessMode policy.AccessMode
}

// ConnectionDiscovery scopes browser references to connection configuration,
// not the workspace revision (which advances on notebook/pipeline edits).
// Values are fingerprinted server-side only: a private per-service HMAC key
// prevents the public token from exposing a guessable hash of inline secrets.
// No credential provider is contacted and no project files are written.
func (s *ConfigService) ConnectionDiscovery(environment string) (string, []ConnectionDiscoveryEntry, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, environment, err := s.connectionDiscoveryConfig(environment)
	if err != nil {
		return "", nil, 0, err
	}
	p, _, err := connectionEnvironmentPolicy(s.workspaceRoot, cfg)
	if err != nil {
		return "", nil, 0, err
	}
	manifest, err := secretstore.LoadManifest(filepath.Join(s.workspaceRoot, ".renart", "secrets.yml"))
	if err != nil {
		return "", nil, 0, err
	}
	summaries := selectedConnectionSummaries(cfg)
	names := make([]string, 0, len(summaries))
	for name := range summaries {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]ConnectionDiscoveryEntry, 0, len(names))
	mac := hmac.New(sha256.New, []byte(s.discoveryKey))
	encoder := json.NewEncoder(mac)
	// Encode framed values, not concatenated connection strings. The selected
	// environment only is relevant; edits to another environment stay isolated.
	for _, value := range []any{s.workspaceRoot, s.configPath, environment, p, manifest.Environments[environment]} {
		if err := encoder.Encode(value); err != nil {
			return "", nil, 0, fmt.Errorf("fingerprint connection configuration")
		}
	}
	for _, name := range names {
		connection, ok := selectedConfigurationConnection(cfg, name)
		if !ok {
			return "", nil, 0, fmt.Errorf("connection configuration is incomplete")
		}
		entry := ConnectionDiscoveryEntry{Name: name, Type: summaries[name], AccessMode: policy.EffectiveMode(p, name, nativeConnectionReadOnly(connection))}
		for _, value := range []any{entry, connectionDiscoveryValues(reflect.ValueOf(connection))} {
			if err := encoder.Encode(value); err != nil {
				// Never include serialized values or provider errors in browser errors.
				return "", nil, 0, fmt.Errorf("fingerprint connection configuration")
			}
		}
		entries = append(entries, entry)
	}
	return environment, entries, int64(binary.BigEndian.Uint64(mac.Sum(nil)[:8])), nil
}

// Bruin's transport JSON marshalers can open private-key/credential files.
// Project raw fields into plain values instead: fingerprint declarations, not
// resolved credentials. Keep every field, including maps and optional limits.
func connectionDiscoveryValues(value reflect.Value) any {
	if value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return connectionDiscoveryValues(value.Elem())
	}
	if value.Kind() == reflect.Struct {
		fields := make(map[string]any)
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.IsExported() {
				fields[field.Name] = connectionDiscoveryValues(value.Field(i))
			}
		}
		return fields
	}
	return value.Interface()
}
