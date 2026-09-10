package secretstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestParseRefValidatesProviderSpecificForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  Ref
	}{
		{"env:WAREHOUSE_PASSWORD", Ref{Provider: "env", Key: "WAREHOUSE_PASSWORD"}},
		{"local:warehouse/password", Ref{Provider: "local", Key: "warehouse/password"}},
		{"vault:analytics/data/warehouse#password", Ref{Provider: "vault", Key: "analytics/data/warehouse", Field: "password"}},
	}
	for _, test := range tests {
		parsed, err := ParseRef(test.value)
		require.NoError(t, err)
		assert.Equal(t, test.want, parsed)
		assert.Equal(t, test.value, parsed.String())
	}

	for _, invalid := range []string{
		"WAREHOUSE_PASSWORD",
		"env:",
		"env:1INVALID",
		"env:NAME#field",
		"local:/absolute",
		"local:warehouse//password",
		"local:warehouse/../password",
	} {
		_, err := ParseRef(invalid)
		assert.Error(t, err, invalid)
	}
}

func TestManifestRoundTripAndStrictValidation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".renart", "secrets.yml")
	manifest := NewManifest()
	require.NoError(t, manifest.SetBinding("default", "warehouse", "password", Binding{
		Symbol:    "WAREHOUSE_PASSWORD",
		Reference: Ref{Provider: "local", Key: "warehouse/password"},
	}))
	require.NoError(t, SaveManifest(path, manifest))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	loaded, err := LoadManifest(path)
	require.NoError(t, err)
	binding, found := loaded.Binding("default", "warehouse", "password")
	require.True(t, found)
	assert.Equal(t, "WAREHOUSE_PASSWORD", binding.Symbol)
	assert.Equal(t, "local:warehouse/password", binding.Reference.String())

	require.NoError(t, os.WriteFile(path, []byte("version: 1\nunknown: true\n"), 0o644))
	_, err = LoadManifest(path)
	require.ErrorContains(t, err, "field unknown not found")
}

func TestManifestRejectsOversizedInput(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "secrets.yml")
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), maxManifestSize+1), 0o644))
	_, err := LoadManifest(path)
	require.ErrorContains(t, err, "exceed")
}

func TestEnvironmentProviderIsReadOnlyAndUsesLeases(t *testing.T) {
	t.Parallel()

	provider := &EnvironmentProvider{lookup: func(name string) (string, bool) {
		return map[string]string{"TOKEN": "canary"}[name], name == "TOKEN"
	}}
	request := ResolveRequest{
		Reference: Ref{Provider: "env", Key: "TOKEN"},
		Purpose:   PurposeQuery,
	}
	status, err := provider.Stat(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, StatusConfigured, status.State)

	lease, err := provider.Resolve(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, []byte("canary"), lease.Bytes())
	require.NoError(t, lease.Close(t.Context()))
	assert.Nil(t, lease.Bytes())

	_, err = provider.Put(t.Context(), PutRequest{})
	assert.ErrorIs(t, err, ErrReadOnly)
}

type memoryCredentialStore struct {
	values map[string]string
	err    error
}

func (s *memoryCredentialStore) Set(service, user, password string) error {
	if s.err != nil {
		return s.err
	}
	s.values[service+"\x00"+user] = password
	return nil
}

func (s *memoryCredentialStore) Get(service, user string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	value, found := s.values[service+"\x00"+user]
	if !found {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (s *memoryCredentialStore) Delete(service, user string) error {
	if s.err != nil {
		return s.err
	}
	key := service + "\x00" + user
	if _, found := s.values[key]; !found {
		return keyring.ErrNotFound
	}
	delete(s.values, key)
	return nil
}

type probingCredentialStore struct {
	state       credentialStoreProbeState
	probeErr    error
	value       string
	getCalls    int
	setCalls    int
	deleteCalls int
}

func (s *probingCredentialStore) Probe(
	context.Context,
	string,
	string,
) (credentialStoreProbeState, error) {
	return s.state, s.probeErr
}

func (s *probingCredentialStore) Set(_, _, password string) error {
	s.setCalls++
	s.value = password
	s.state = credentialStoreProbeConfigured
	return nil
}

func (s *probingCredentialStore) Get(_, _ string) (string, error) {
	s.getCalls++
	if s.state == credentialStoreProbeMissing {
		return "", keyring.ErrNotFound
	}
	return s.value, nil
}

func (s *probingCredentialStore) Delete(_, _ string) error {
	s.deleteCalls++
	s.value = ""
	s.state = credentialStoreProbeMissing
	return nil
}

func TestLocalProviderScopesValuesAndFailsClosed(t *testing.T) {
	t.Parallel()

	store := &memoryCredentialStore{values: map[string]string{}}
	provider := newLocalProviderWithStore(store)
	ref := Ref{Provider: "local", Key: "warehouse/password"}
	put := PutRequest{
		ProjectID: "project-a", Environment: "default", Reference: ref,
		Value: []byte("secret"), Purpose: PurposeSecretAdministration,
	}
	status, err := provider.Put(t.Context(), put)
	require.NoError(t, err)
	assert.Equal(t, StatusConfigured, status.State)

	lease, err := provider.Resolve(t.Context(), ResolveRequest{
		ProjectID: "project-a", Environment: "default", Reference: ref, Purpose: PurposeQuery,
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("secret"), lease.Bytes())
	require.NoError(t, lease.Close(t.Context()))

	_, err = provider.Resolve(t.Context(), ResolveRequest{
		ProjectID: "project-b", Environment: "default", Reference: ref, Purpose: PurposeQuery,
	})
	assert.ErrorIs(t, err, ErrNotFound)

	store.err = errors.New("credential service unavailable")
	status, err = provider.Stat(t.Context(), ResolveRequest{
		ProjectID: "project-a", Environment: "default", Reference: ref, Purpose: PurposeQuery,
	})
	assert.Equal(t, StatusUnavailable, status.State)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestLocalProviderProbeAvoidsInteractiveCredentialStoreAccess(t *testing.T) {
	t.Parallel()

	request := ResolveRequest{
		ProjectID: "project-a", Environment: "default",
		Reference: Ref{Provider: "local", Key: "warehouse/password"},
		Purpose:   PurposeSecretAdministration,
	}

	t.Run("locked", func(t *testing.T) {
		t.Parallel()
		store := &probingCredentialStore{state: credentialStoreProbePermissionRequired}
		provider := newLocalProviderWithStore(store)

		status, err := provider.Stat(t.Context(), request)
		assert.Equal(t, StatusPermissionRequired, status.State)
		assert.False(t, status.Writable)
		assert.ErrorIs(t, err, ErrPermissionRequired)

		_, err = provider.Resolve(t.Context(), request)
		assert.ErrorIs(t, err, ErrPermissionRequired)
		_, err = provider.Put(t.Context(), PutRequest{
			ProjectID: request.ProjectID, Environment: request.Environment,
			Reference: request.Reference, Purpose: request.Purpose, Value: []byte("secret"),
		})
		assert.ErrorIs(t, err, ErrPermissionRequired)
		err = provider.Delete(t.Context(), DeleteRequest{
			ProjectID: request.ProjectID, Environment: request.Environment,
			Reference: request.Reference, Purpose: request.Purpose,
		})
		assert.ErrorIs(t, err, ErrPermissionRequired)

		assert.Zero(t, store.getCalls)
		assert.Zero(t, store.setCalls)
		assert.Zero(t, store.deleteCalls)
	})

	t.Run("unavailable", func(t *testing.T) {
		t.Parallel()
		store := &probingCredentialStore{
			state:    credentialStoreProbeUnknown,
			probeErr: errors.New("session bus unavailable"),
		}
		provider := newLocalProviderWithStore(store)

		status, err := provider.Stat(t.Context(), request)
		assert.Equal(t, StatusUnavailable, status.State)
		assert.False(t, status.Writable)
		assert.ErrorIs(t, err, ErrUnavailable)
		assert.Zero(t, store.getCalls)
	})

	t.Run("unlocked", func(t *testing.T) {
		t.Parallel()
		store := &probingCredentialStore{state: credentialStoreProbeMissing}
		provider := newLocalProviderWithStore(store)

		status, err := provider.Stat(t.Context(), request)
		require.NoError(t, err)
		assert.Equal(t, StatusMissing, status.State)
		assert.True(t, status.Writable)
		assert.Zero(t, store.getCalls)

		_, err = provider.Resolve(t.Context(), request)
		assert.ErrorIs(t, err, ErrNotFound)
		assert.Zero(t, store.getCalls)

		_, err = provider.Put(t.Context(), PutRequest{
			ProjectID: request.ProjectID, Environment: request.Environment,
			Reference: request.Reference, Purpose: request.Purpose, Value: []byte("secret"),
		})
		require.NoError(t, err)
		assert.Equal(t, 1, store.setCalls)

		lease, err := provider.Resolve(t.Context(), request)
		require.NoError(t, err)
		assert.Equal(t, []byte("secret"), lease.Bytes())
		assert.Equal(t, 1, store.getCalls)
		require.NoError(t, lease.Close(t.Context()))
	})
}

func TestLocalProviderRecoversWithoutRestartOrCredentialReplacement(t *testing.T) {
	t.Parallel()
	request := ResolveRequest{
		ProjectID: "project-a", Environment: "default",
		Reference: Ref{Provider: "local", Key: "storage/secret_access_key"},
		Purpose:   PurposeQuery,
	}
	store := &probingCredentialStore{value: "recovery-canary"}
	provider := newLocalProviderWithStore(store)
	for _, state := range []credentialStoreProbeState{
		credentialStoreProbePermissionRequired, credentialStoreProbeUnknown,
	} {
		store.state = state
		_, err := provider.Resolve(t.Context(), request)
		require.Error(t, err)
		store.state = credentialStoreProbeConfigured
		status, err := provider.Stat(t.Context(), request)
		require.NoError(t, err)
		require.Equal(t, StatusConfigured, status.State)
		require.Equal(t, "local", status.Provider)
		lease, err := provider.Resolve(t.Context(), request)
		require.NoError(t, err)
		require.Equal(t, []byte("recovery-canary"), lease.Bytes())
		require.NoError(t, lease.Close(t.Context()))
	}
	require.Equal(t, 2, store.getCalls)
	require.Zero(t, store.setCalls)
	require.Zero(t, store.deleteCalls)
}

func TestResolverClosesPartialBundleOnFailure(t *testing.T) {
	t.Parallel()

	provider := &EnvironmentProvider{lookup: func(name string) (string, bool) {
		return "first-secret", name == "FIRST"
	}}
	resolver, err := NewResolver(provider)
	require.NoError(t, err)
	_, err = resolver.ResolveAll(context.Background(), []NamedRequest{
		{Name: "first", Request: ResolveRequest{Reference: Ref{Provider: "env", Key: "FIRST"}, Purpose: PurposeQuery}},
		{Name: "missing", Request: ResolveRequest{Reference: Ref{Provider: "env", Key: "MISSING"}, Purpose: PurposeQuery}},
	})
	require.Error(t, err)
}
