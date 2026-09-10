package policy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectionAccessMatrix(t *testing.T) {
	p := EnvironmentPolicy{Connections: map[string]ConnectionPolicy{"source": {AccessMode: ReadOnly}}}
	for _, tc := range []struct {
		name   string
		effect Effect
		denied bool
	}{
		{"source", Read, false}, {"source", Write, true}, {"source", Unknown, true},
		{"destination", Write, false}, {"destination", Unknown, false},
	} {
		err := CheckAccess(p, "prod", Requirement{Connection: tc.name, Effect: tc.effect, Operation: "test"}, false)
		if tc.denied {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
	require.ErrorContains(t, CheckAccess(EnvironmentPolicy{}, "prod", Requirement{Connection: "native", Effect: Write}, true), "read-only")
}

func TestPolicyStrictAndFailClosed(t *testing.T) {
	for _, invalid := range []string{
		"environments: {prod: {protecetd: true}}",
		"environments: {prod: {connections: {db: {access_mode: readonly}}}}",
		"environments: {prod: {protected: true, protected: false}}",
		"environments: {}\n---\nenvironments: {}",
	} {
		t.Run(invalid, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "environments.yml")
			require.NoError(t, os.WriteFile(path, []byte(invalid), 0o644))
			_, err := Load(path)
			require.Error(t, err)
			loader := NewLoader(path)
			require.Error(t, Check(loader.For("prod"), RunRequest{Environment: "prod"}))
			require.Error(t, CheckAccess(loader.For("prod"), "prod", Requirement{Connection: "db", Effect: Read}, false))
		})
	}
}

func TestPolicySnapshotAndLegacyUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environments.yml")
	loader := NewLoader(path)
	_, err := loader.Set("prod", EnvironmentPolicy{Connections: map[string]ConnectionPolicy{"db": {AccessMode: ReadOnly}}})
	require.NoError(t, err)
	first, err := loader.Snapshot()
	require.NoError(t, err)
	require.False(t, first.Config.For("prod").Zero())
	_, err = loader.Set("prod", EnvironmentPolicy{Protected: true})
	require.NoError(t, err)
	require.Equal(t, ReadOnly, loader.For("prod").Connections["db"].AccessMode)
	copy := loader.For("prod")
	delete(copy.Connections, "db")
	require.Equal(t, ReadOnly, loader.For("prod").Connections["db"].AccessMode)
	next, err := loader.Snapshot()
	require.NoError(t, err)
	require.NotEqual(t, first.Revision, next.Revision)
	require.NoError(t, os.WriteFile(path, []byte("environments: [invalid]"), 0o644))
	_, err = loader.Snapshot()
	require.Error(t, err)
	require.Error(t, Check(loader.For("prod"), RunRequest{}))
}

func TestPolicyUnreadableIsNotMissing(t *testing.T) {
	loader := NewLoader(t.TempDir()) // A directory cannot be read as a policy file.
	_, err := loader.Snapshot()
	require.Error(t, err)
	require.Error(t, Check(loader.For("prod"), RunRequest{}))
}

func TestInterruptedConfigurationFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environments.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(PendingPath(path)), 0o700))
	require.NoError(t, os.WriteFile(PendingPath(path), []byte("interrupted"), 0o600))
	loader := NewLoader(path)
	_, err := loader.Snapshot()
	require.ErrorContains(t, err, "incomplete")
	require.Error(t, Check(loader.For("prod"), RunRequest{}))
	_, err = loader.Set("prod", EnvironmentPolicy{})
	require.Error(t, err)
}
