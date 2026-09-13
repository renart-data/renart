package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStorageLoadPatternRetainsRootAndWildcardsWithoutCredentials(t *testing.T) {
	for _, item := range []struct{ uri, kind, want string }{
		{"s3://bucket/private/root?access_key_id=secret", "s3", "s3://bucket/private/root/orders/day=*/part-?.csv"},
		{"sftp://user:password@host:2222/private/root", "sftp", "sftp://host:2222/private/root/orders/day=*/part-?.csv"},
	} {
		root, err := storageBrowseRoot(item.uri, item.kind)
		require.NoError(t, err)
		ref, err := root.loadPattern("orders/day=*/part-?.csv")
		require.NoError(t, err)
		require.Equal(t, item.want, ref)
		_, err = root.loadPattern("../*")
		require.Error(t, err)
		_, err = root.pattern("orders/*")
		require.Error(t, err, "Literal discovery must remain literal")
	}
}
