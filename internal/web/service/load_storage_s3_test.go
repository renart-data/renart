package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
	"renart/internal/web/databrowser"
)

func TestS3StorageClientUsesResolvedCredentialsAndLiteralHTTPPrefix(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bucket", r.URL.Path)
		require.Equal(t, "scoped/table/day=2026-09", r.URL.Query().Get("prefix"))
		require.Equal(t, "/", r.URL.Query().Get("delimiter"))
		require.Equal(t, "501", r.URL.Query().Get("max-keys"))
		require.Contains(t, r.Header.Get("Authorization"), "Credential=test-key/")
		require.NotContains(t, r.URL.String(), "test-secret")
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsTruncated>false</IsTruncated><CommonPrefixes><Prefix>scoped/table/day=2026-09-01/</Prefix></CommonPrefixes></ListBucketResult>`)
	}))
	defer server.Close()
	payload, _ := json.Marshal(map[string]string{"type": "s3", "url": "s3://bucket/scoped/", "endpoint": server.URL, "access_key_id": "test-key", "secret_access_key": "test-secret"})
	client, err := newStorageS3Client(t.Context(), string(payload))
	require.NoError(t, err)
	root, err := storageBrowseRoot(string(payload), "s3")
	require.NoError(t, err)
	result, err := listS3Storage(t.Context(), client, root, databrowser.StorageQuery{Prefix: "table/", NamePrefix: "day=2026-09"})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	require.False(t, result.Truncated)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = listS3Storage(ctx, client, root, databrowser.StorageQuery{})
	require.Error(t, err)
}

func TestS3StorageTransportBoundsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", (1<<20)+1)) }))
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), "GET", server.URL, nil)
	require.NoError(t, err)
	response, err := (storageS3Transport{}).RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Len(t, body, 1<<20)
}

type storageS3Stub func(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)

func (f storageS3Stub) ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return f(ctx, in, opts...)
}

func TestS3StorageFiltersBeforeLimitAndKeepsConfiguredRoot(t *testing.T) {
	root, err := storageBrowseRoot("s3://bucket/scoped/", "s3")
	require.NoError(t, err)
	calls := 0
	client := storageS3Stub(func(ctx context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
		calls++
		require.Equal(t, "bucket", aws.ToString(in.Bucket))
		require.Equal(t, "scoped/my_table/day=2026-09", aws.ToString(in.Prefix))
		require.Equal(t, "/", aws.ToString(in.Delimiter))
		require.EqualValues(t, 501, aws.ToInt32(in.MaxKeys))
		return &s3.ListObjectsV2Output{CommonPrefixes: []types.CommonPrefix{{Prefix: aws.String("scoped/my_table/day=2026-09-01/")}}}, nil
	})
	result, err := listS3Storage(t.Context(), client, root, databrowser.StorageQuery{Prefix: "my_table/", NamePrefix: "day=2026-09"})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.False(t, result.Truncated)
	require.Equal(t, []databrowser.StorageEntry{{Path: "my_table/day=2026-09-01/", Reference: "s3://bucket/scoped/my_table/day=2026-09-01/", Directory: true}}, result.Entries)
}

func TestS3StorageBoundsBothPrefixesAndObjects(t *testing.T) {
	root, err := storageBrowseRoot("s3://bucket/", "s3")
	require.NoError(t, err)
	for _, count := range []int{500, 501} {
		for _, directory := range []bool{false, true} {
			client := storageS3Stub(func(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
				out := &s3.ListObjectsV2Output{}
				for i := 0; i < count; i++ {
					key := fmt.Sprintf("day=%04d", i)
					if directory {
						out.CommonPrefixes = append(out.CommonPrefixes, types.CommonPrefix{Prefix: aws.String(key + "/")})
					} else {
						out.Contents = append(out.Contents, types.Object{Key: aws.String(key + ".csv")})
					}
				}
				return out, nil
			})
			result, err := listS3Storage(t.Context(), client, root, databrowser.StorageQuery{})
			require.NoError(t, err)
			require.Len(t, result.Entries, 500)
			require.Equal(t, count > 500, result.Truncated)
		}
	}
}

func TestS3StoragePaginationIsBoundedAndRespectsProviderTruncation(t *testing.T) {
	root, err := storageBrowseRoot("s3://bucket/", "s3")
	require.NoError(t, err)
	calls := 0
	client := storageS3Stub(func(ctx context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
		calls++
		if calls == 1 {
			return &s3.ListObjectsV2Output{Contents: []types.Object{{Key: aws.String("a.csv")}}, IsTruncated: aws.Bool(true), NextContinuationToken: aws.String("next")}, nil
		}
		require.Equal(t, "next", aws.ToString(in.ContinuationToken))
		require.EqualValues(t, 500, aws.ToInt32(in.MaxKeys))
		return &s3.ListObjectsV2Output{Contents: []types.Object{{Key: aws.String("b.csv")}}}, nil
	})
	result, err := listS3Storage(t.Context(), client, root, databrowser.StorageQuery{})
	require.NoError(t, err)
	require.Len(t, result.Entries, 2)
	require.False(t, result.Truncated)
	require.Equal(t, 2, calls)
}

func TestS3StorageExactHandoffDoesNotDependOnFirstPage(t *testing.T) {
	root, err := storageBrowseRoot("s3://bucket/scoped/", "s3")
	require.NoError(t, err)
	for _, name := range []string{"day=2026-09/", "last.csv"} {
		client := storageS3Stub(func(ctx context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			require.Equal(t, "scoped/"+name, aws.ToString(in.Prefix))
			require.EqualValues(t, 1, aws.ToInt32(in.MaxKeys))
			key := "scoped/" + name
			if name == "day=2026-09/" {
				key += "data.csv"
			}
			return &s3.ListObjectsV2Output{Contents: []types.Object{{Key: aws.String(key)}}}, nil
		})
		result, err := listS3Storage(t.Context(), client, root, databrowser.StorageQuery{NamePrefix: name, Exact: true})
		require.NoError(t, err)
		require.Len(t, result.Entries, 1)
		require.Equal(t, name, result.Entries[0].Path)
	}
}

func TestS3StorageNeverTreatsAnUnfinishedListingAsComplete(t *testing.T) {
	root, err := storageBrowseRoot("s3://bucket/", "s3")
	require.NoError(t, err)
	calls := 0
	client := storageS3Stub(func(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
		calls++
		return &s3.ListObjectsV2Output{IsTruncated: aws.Bool(true), NextContinuationToken: aws.String(fmt.Sprint(calls))}, nil
	})
	result, err := listS3Storage(t.Context(), client, root, databrowser.StorageQuery{})
	require.NoError(t, err)
	require.Equal(t, 32, calls)
	require.True(t, result.Truncated)
	repeated := storageS3Stub(func(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
		return &s3.ListObjectsV2Output{IsTruncated: aws.Bool(true), NextContinuationToken: aws.String("same")}, nil
	})
	_, err = listS3Storage(t.Context(), repeated, root, databrowser.StorageQuery{})
	require.ErrorContains(t, err, "continuation token")
}
