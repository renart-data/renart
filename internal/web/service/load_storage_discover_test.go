package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"renart/internal/web/databrowser"
	"renart/internal/web/secretstore"
)

type failingStorageConnectionManager struct {
	loadConnectionManagerWithDetails
	err error
}

func (m failingStorageConnectionManager) ResolveConnection(string) (any, error) { return nil, m.err }

func TestStorageBrowseReportsSafeSecretFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		hint string
	}{
		{"missing", secretstore.ErrNotFound, "secret is missing"},
		{"locked", secretstore.ErrPermissionRequired, "locked or requires permission"},
		{"unavailable", secretstore.ErrUnavailable, "secret provider is unavailable"},
		{"unknown provider", secretstore.ErrUnknownProvider, "secret provider is not installed"},
		{"driver", errors.New("invalid driver"), "could not resolve the storage connection"},
	} {
		for _, lazy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/lazy=%t", tc.name, lazy), func(t *testing.T) {
				cause := fmt.Errorf("private-uri-and-password-canary: %w", tc.err)
				s := NewLoadService(LoadDependencies{NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
					if lazy {
						return failingStorageConnectionManager{err: cause}, nil
					}
					return nil, cause
				}})
				_, err := s.BrowseStorage(t.Context(), "storage", databrowser.StorageQuery{}, "default")
				require.ErrorContains(t, err, tc.hint)
				require.NotContains(t, err.Error(), "private-uri-and-password-canary")
			})
		}
	}
}

func TestStorageDiscoveryCaptureCancelsOversizedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &storageDiscoveryCapture{cancel: cancel}
	_, err := w.Write(make([]byte, 1<<20))
	require.NoError(t, err)
	_, err = w.Write([]byte("x"))
	require.Error(t, err)
	require.True(t, w.overflow)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Equal(t, 1<<20, w.Len())
}

func TestStorageLoadPinsConnectionAfterSlingFlagParsing(t *testing.T) {
	for _, stream := range []string{"s3://bucket/orders.csv", "sftp://host:22/incoming/orders.csv"} {
		args, env := slingCommandConnectionEnv([]string{"run", "--src-conn", "secret", "--src-stream", stream, "--tgt-conn", "target-secret", "--tgt-object", stream})
		require.NotContains(t, strings.Join(args, " "), "secret")
		var task map[string]map[string]string
		for _, entry := range env {
			if value, ok := strings.CutPrefix(entry, "SLING_TASK_CONFIG="); ok {
				require.NoError(t, json.Unmarshal([]byte(value), &task))
			}
		}
		require.Equal(t, map[string]string{"conn": slingSourceConnectionEnv, "stream": stream}, task["source"])
		require.Equal(t, map[string]string{"conn": slingTargetConnectionEnv, "object": stream}, task["target"])
	}
}

func TestStorageBrowseScopeAndSlingRows(t *testing.T) {
	root, err := storageBrowseRoot("s3://bucket/imports/?access_key_id=secret", "s3")
	require.NoError(t, err)
	pattern, err := root.pattern("2026/")
	require.NoError(t, err)
	assert.Equal(t, "s3://bucket/imports/2026/", pattern)
	for _, invalid := range []string{"../", "/etc/", "s3://other/", "x/../", "**/", "x|y/", "a\x00b"} {
		_, err := root.pattern(invalid)
		require.Error(t, err, invalid)
	}
	output := `{"fields":["#","Name","Type"],"rows":[[1,"imports/2026/orders.csv","file"],[2,"imports/2026/daily/","folder"],[3,"outside/private.csv","file"]]}`
	listing, err := parseStorageDiscovery(output, root, "2026/")
	require.NoError(t, err)
	require.Len(t, listing.Entries, 2)
	assert.Equal(t, "2026/daily/", listing.Entries[0].Path)
	assert.True(t, listing.Entries[0].Directory)
	assert.Equal(t, "s3://bucket/imports/2026/orders.csv", listing.Entries[1].Reference)
	assert.NotContains(t, listing.Entries[1].Reference, "secret")
	_, err = parseStorageDiscovery("not JSON", root, "")
	require.Error(t, err, "invalid provider output must not masquerade as an empty directory")
}

func TestSFTPBrowseUsesCredentialFreePaths(t *testing.T) {
	root, err := storageBrowseRoot("sftp://alice:secret@example.test:22", "sftp")
	require.NoError(t, err)
	pattern, err := root.pattern("imports/")
	require.NoError(t, err)
	assert.Equal(t, "sftp://example.test:22/imports/", pattern)
	rootPattern, err := root.pattern("")
	require.NoError(t, err)
	assert.Equal(t, "sftp://example.test:22//", rootPattern)
	listing, err := parseStorageDiscovery(`{"fields":["Name","Type"],"rows":[["sftp://example.test:22//imports/a.csv","file"],["/imports/private/","folder"]]}`, root, "imports/")
	require.NoError(t, err)
	require.Len(t, listing.Entries, 2)
	assert.Equal(t, "imports/a.csv", listing.Entries[1].Path)
	assert.NotContains(t, listing.Entries[1].Reference, "secret")
}

func TestStorageSlingConnectionPayload(t *testing.T) {
	manager := loadConnectionManagerWithDetails{connection: "unused", connectionType: "s3", details: config.S3Connection{
		BucketName: "bucket", PathToFile: "imports/", AccessKeyID: "key", SecretAccessKey: "secret", EndpointURL: "http://localhost:9000",
	}}
	encoded, err := loadConnectionURI(manager, "lake")
	require.NoError(t, err)
	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(encoded), &payload))
	assert.Equal(t, "http://localhost:9000", payload["endpoint"])
	assert.Equal(t, "secret", payload["secret_access_key"])
	assert.Equal(t, "s3://bucket/imports/", payload["url"])
	root, err := storageBrowseRoot(encoded, "s3")
	require.NoError(t, err)
	pattern, err := root.pattern("daily/")
	require.NoError(t, err)
	assert.Equal(t, "s3://bucket/imports/daily/", pattern)
}
