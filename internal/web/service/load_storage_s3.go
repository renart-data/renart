package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"renart/internal/web/databrowser"
)

// Unlike Sling's glob discovery, ListObjectsV2 applies Prefix before MaxKeys
// and Delimiter. This remains one level of metadata, never a recursive scan.
type storageS3Lister interface {
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

type storageS3Transport struct{}
type storageS3Body struct {
	io.Reader
	io.Closer
}

func (storageS3Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(req)
	if err == nil {
		response.Body = &storageS3Body{Reader: io.LimitReader(response.Body, 1<<20), Closer: response.Body}
	}
	return response, err
}

func newStorageS3Client(ctx context.Context, uri string) (*s3.Client, error) {
	var connection struct {
		URL       string `json:"url"`
		AccessKey string `json:"access_key_id"`
		SecretKey string `json:"secret_access_key"`
		Endpoint  string `json:"endpoint"`
	}
	if strings.HasPrefix(uri, "{") {
		if err := json.Unmarshal([]byte(uri), &connection); err != nil {
			return nil, err
		}
	} else {
		u, err := url.Parse(uri)
		if err != nil {
			return nil, err
		}
		connection.URL = uri
		connection.AccessKey, connection.SecretKey = u.Query().Get("access_key_id"), u.Query().Get("secret_access_key")
		connection.Endpoint = u.Query().Get("endpoint_url")
	}
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithHTTPClient(&http.Client{Transport: storageS3Transport{}}),
		awsconfig.WithRetryMaxAttempts(2),
	}
	if connection.AccessKey != "" || connection.SecretKey != "" {
		if connection.AccessKey == "" || connection.SecretKey == "" {
			return nil, fmt.Errorf("configure both S3 access key and secret key")
		}
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(connection.AccessKey, connection.SecretKey, "")))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	discoverRegion := cfg.Region == "" && connection.Endpoint == ""
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	endpoint := connection.Endpoint
	if endpoint != "" && !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid S3 endpoint")
		}
		if strings.HasSuffix(u.Hostname(), ".cloudflarestorage.com") {
			cfg.Region = "auto"
		}
		if region, ok := strings.CutSuffix(u.Hostname(), ".digitaloceanspaces.com"); ok {
			cfg.Region = region
		}
	}
	clientOptions := func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	}
	client := s3.NewFromConfig(cfg, clientOptions)
	if discoverRegion {
		root, err := url.Parse(connection.URL)
		if err != nil {
			return nil, err
		}
		if region, err := manager.GetBucketRegion(ctx, client, root.Host); err == nil {
			cfg.Region = region
			client = s3.NewFromConfig(cfg, clientOptions)
		}
	}
	return client, nil
}

func listS3Storage(ctx context.Context, client storageS3Lister, root storageRoot, query databrowser.StorageQuery) (databrowser.StorageListing, error) {
	result := databrowser.StorageListing{Entries: []databrowser.StorageEntry{}}
	if err := query.Validate(); err != nil {
		return result, err
	}
	searchPrefix := root.prefix + query.Prefix + query.NamePrefix
	input := &s3.ListObjectsV2Input{Bucket: aws.String(root.url.Host), Prefix: aws.String(searchPrefix), Delimiter: aws.String("/"), MaxKeys: aws.Int32(maxLoadDiscoveryStreams + 1)}
	if query.Exact {
		input.MaxKeys = aws.Int32(1)
	}
	seen := map[string]bool{}
	remaining := maxLoadDiscoveryStreams + 1
	add := func(key string, directory bool) {
		if !strings.HasPrefix(key, searchPrefix) || !strings.HasPrefix(key, root.prefix) {
			return
		}
		relative := strings.TrimPrefix(key, root.prefix)
		if directory && !strings.HasSuffix(relative, "/") {
			relative += "/"
		}
		if databrowser.ValidateStoragePath(relative, false) != nil || seen[relative] {
			return
		}
		parent := ""
		if index := strings.LastIndexByte(strings.TrimSuffix(relative, "/"), '/'); index >= 0 {
			parent = relative[:index+1]
		}
		if parent != query.Prefix {
			return
		}
		reference, err := root.pattern(relative)
		if err != nil {
			return
		}
		seen[relative] = true
		result.Entries = append(result.Entries, databrowser.StorageEntry{Path: relative, Reference: reference, Directory: directory})
	}
	// Bound page count too: a broken provider must not loop on empty pages.
	for page := 0; page < 32; page++ {
		output, err := client.ListObjectsV2(ctx, input)
		if err != nil {
			return result, err
		}
		if query.Exact {
			directory := strings.HasSuffix(query.NamePrefix, "/")
			for _, item := range output.CommonPrefixes {
				if directory && strings.HasPrefix(aws.ToString(item.Prefix), searchPrefix) {
					add(searchPrefix, true)
				}
			}
			for _, item := range output.Contents {
				key := aws.ToString(item.Key)
				if (directory && strings.HasPrefix(key, searchPrefix)) || key == searchPrefix {
					add(searchPrefix, directory)
				}
			}
			return result, nil
		}
		for _, item := range output.CommonPrefixes {
			add(aws.ToString(item.Prefix), true)
		}
		for _, item := range output.Contents {
			key := aws.ToString(item.Key)
			add(key, strings.HasSuffix(key, "/"))
		}
		remaining -= len(output.CommonPrefixes) + len(output.Contents)
		result.Truncated = remaining <= 0 || aws.ToBool(output.IsTruncated)
		if remaining <= 0 || !aws.ToBool(output.IsTruncated) {
			break
		}
		if aws.ToString(output.NextContinuationToken) == "" || aws.ToString(output.NextContinuationToken) == aws.ToString(input.ContinuationToken) {
			return result, fmt.Errorf("invalid S3 continuation token")
		}
		input.ContinuationToken = output.NextContinuationToken
		input.MaxKeys = aws.Int32(int32(remaining))
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		if result.Entries[i].Directory != result.Entries[j].Directory {
			return result.Entries[i].Directory
		}
		return result.Entries[i].Path < result.Entries[j].Path
	})
	if len(result.Entries) > maxLoadDiscoveryStreams {
		result.Entries = result.Entries[:maxLoadDiscoveryStreams]
	}
	return result, nil
}
