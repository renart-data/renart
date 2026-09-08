// Package policy evaluates per-environment run rules and connection effects.
// Services enforce these rules before dispatch and at physical-task boundaries
// (UI build, CLI and scheduler). UI-side disabling is not the enforcement.
//
// Locally these are guardrails — the user owns the credentials. The
// enforced version is the cloud permission model, where protected
// environments' credentials only decrypt for the scheduler identity. Flag
// names and semantics are kept identical so the cloud later enforces the
// same configuration harder rather than introducing a second vocabulary.
package policy

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
	"renart/internal/web/workspacefs"
)

// EnvironmentPolicy is the per-environment rule set.
type EnvironmentPolicy struct {
	// Protected forbids user-initiated UI/CLI execution, whether it targets
	// the working tree or a snapshot. Server-owned scheduled runs pass.
	Protected bool `yaml:"protected" json:"protected"`
	// DeployedOnly forbids any execution that is not a deployed snapshot,
	// including scheduled runs falling back to the working tree.
	DeployedOnly bool `yaml:"deployed_only" json:"deployed_only"`
	// ConfirmDestructive requires a typed environment-name confirmation for
	// destructive operations (full refresh, backfill, drop).
	ConfirmDestructive bool                        `yaml:"confirm_destructive" json:"confirm_destructive"`
	Connections        map[string]ConnectionPolicy `yaml:"connections,omitempty" json:"connections,omitempty"`
	// Invalid is carried through legacy PolicyFor ports so failed reads never
	// become a zero, unrestricted policy. It is not authored configuration.
	Invalid string `yaml:"-" json:"-"`
}

// Zero reports whether the policy has no flags set.
func (p EnvironmentPolicy) Zero() bool {
	return !p.Protected && !p.DeployedOnly && !p.ConfirmDestructive && len(p.Connections) == 0 && p.Invalid == ""
}

// Config is the on-disk policy file (.renart/environments.yml).
type Config struct {
	Environments map[string]EnvironmentPolicy `yaml:"environments" json:"environments"`
	invalid      string
}

// For returns the policy for an environment; absent environments have the
// zero (unrestricted) policy.
func (c Config) For(environment string) EnvironmentPolicy {
	p := c.Environments[environment]
	p.Invalid = c.invalid
	if p.Connections != nil {
		connections := make(map[string]ConnectionPolicy, len(p.Connections))
		for name, connection := range p.Connections {
			connections[name] = connection
		}
		p.Connections = connections
	}
	return p
}

// Load reads the policy file; a missing file is an empty config.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	return decode(path, data)
}

func decode(path string, data []byte) (Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && err != io.EOF {
		return Config{}, fmt.Errorf("policy: failed to parse %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("policy: expected one YAML document in %s", path)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	for env, p := range c.Environments {
		if strings.TrimSpace(env) == "" {
			return fmt.Errorf("policy: environment name is empty")
		}
		for name, connection := range p.Connections {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("policy: connection name in %q is empty", env)
			}
			if connection.AccessMode != ReadOnly && connection.AccessMode != ReadWrite {
				return fmt.Errorf("policy: invalid access_mode %q for %s/%s", connection.AccessMode, env, name)
			}
		}
	}
	return nil
}

// Save writes the environment policy file, creating its parent directory when
// needed. Zero policies are omitted so clearing every flag removes the
// environment from the policy file without affecting Bruin config.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	cleaned := Config{Environments: map[string]EnvironmentPolicy{}}
	for name, envPolicy := range cfg.Environments {
		if name == "" || envPolicy.Zero() {
			continue
		}
		cleaned.Environments[name] = envPolicy
	}

	data, err := yaml.Marshal(cleaned)
	if err != nil {
		return err
	}
	return workspacefs.WriteFileAtomic(path, data, 0o644)
}

// Loader reads a small authoritative file on every authorization. Stat-only
// caches miss same-size edits with restored mtimes, and last-good data is not
// permission to execute after a malformed or unreadable policy update.
type Loader struct {
	path string
}

func NewLoader(path string) *Loader {
	return &Loader{path: path}
}

func (l *Loader) Path() string {
	return l.path
}

func (l *Loader) Config() Config {
	snapshot, err := l.Snapshot()
	if err != nil {
		return Config{invalid: err.Error()}
	}
	return snapshot.Config
}

type Snapshot struct {
	Config   Config
	Revision string
}

func (l *Loader) Snapshot() (Snapshot, error) {
	if err := CheckPending(l.path); err != nil {
		return Snapshot{}, err
	}
	data, err := os.ReadFile(l.path)
	if os.IsNotExist(err) {
		data, err = nil, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	cfg, err := decode(l.path, data)
	if err != nil {
		return Snapshot{}, err
	}
	if err := CheckPending(l.path); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Config: cfg, Revision: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
}

// PendingPath records an interrupted multi-file configuration transaction.
// It contains no credentials. Execution stays closed until the configuration
// is reconciled, including when the policy itself did not exist before a rename.
func PendingPath(path string) string {
	return filepath.Join(filepath.Dir(path), "runtime", "configuration.pending")
}

func CheckPending(path string) error {
	_, err := os.Stat(PendingPath(path))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("a configuration transaction is incomplete; reconcile .bruin.yml, .renart/secrets.yml and .renart/environments.yml before removing %s", PendingPath(path))
}

// Lock serializes configuration/policy transactions across services and CLI
// processes. Callers must hold it from reading the old state through commit.
func Lock(path string) (func(), error) {
	lockPath := filepath.Join(filepath.Dir(path), "runtime", "configuration.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		return nil, err
	}
	return func() { _ = lock.Unlock() }, nil
}

func (l *Loader) For(environment string) EnvironmentPolicy {
	return l.Config().For(environment)
}

func (l *Loader) Set(environment string, envPolicy EnvironmentPolicy) (Config, error) {
	unlock, err := Lock(l.path)
	if err != nil {
		return Config{}, err
	}
	defer unlock()
	if err := CheckPending(l.path); err != nil {
		return Config{}, err
	}

	cfg, err := Load(l.path)
	if err != nil {
		return Config{}, err
	}
	if cfg.Environments == nil {
		cfg.Environments = map[string]EnvironmentPolicy{}
	}
	// Old clients edit environment flags only. An omitted map preserves the
	// connection rules; an explicit empty map is an intentional replacement.
	if envPolicy.Connections == nil {
		envPolicy.Connections = cfg.For(environment).Connections
	}
	if envPolicy.Zero() {
		delete(cfg.Environments, environment)
	} else {
		cfg.Environments[environment] = envPolicy
	}
	if err := Save(l.path, cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// RunRequest describes one execution attempt for policy evaluation.
type RunRequest struct {
	Environment string
	// Interactive marks user-initiated UI/CLI execution. Only server-owned
	// scheduled occurrences are non-interactive, even when a manual run uses an
	// immutable deployment.
	Interactive bool
	// SnapshotBased marks execution of a deployed snapshot.
	SnapshotBased bool
	// Destructive marks full refresh / backfill / drop operations.
	Destructive bool
	// ConfirmedEnvironment carries the typed confirmation for destructive
	// operations.
	ConfirmedEnvironment string
}

// Check is the single enforcement point. Every execution path must pass
// through it; scattered UI-side checks are hints, not enforcement.
func Check(p EnvironmentPolicy, req RunRequest) error {
	if p.Invalid != "" {
		return InvalidError(p.Invalid)
	}
	if p.Protected && req.Interactive {
		return fmt.Errorf("environment %q is protected: interactive execution is disabled; deploy and schedule instead", req.Environment)
	}
	if p.DeployedOnly && !req.SnapshotBased {
		return fmt.Errorf("environment %q only executes deployed snapshots: deploy the pipeline first", req.Environment)
	}
	if p.ConfirmDestructive && req.Destructive && req.ConfirmedEnvironment != req.Environment {
		return fmt.Errorf("environment %q requires typing the environment name to confirm destructive operations", req.Environment)
	}
	return nil
}
