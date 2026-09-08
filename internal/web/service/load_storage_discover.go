package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"renart/internal/web/databrowser"
)

type storageRoot struct {
	url    url.URL
	prefix string
}

func storageBrowseRoot(uri, connectionType string) (storageRoot, error) {
	if strings.HasPrefix(uri, "{") {
		var payload struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}
		if json.Unmarshal([]byte(uri), &payload) != nil || payload.Type != connectionType {
			return storageRoot{}, fmt.Errorf("this connection does not support storage browsing")
		}
		uri = payload.URL
	}
	u, err := url.Parse(uri)
	if err != nil || u == nil || u.Scheme != connectionType || (connectionType != "s3" && connectionType != "sftp") {
		return storageRoot{}, fmt.Errorf("this connection does not support storage browsing")
	}
	if u.Hostname() == "" {
		return storageRoot{}, fmt.Errorf("choose a bucket in this S3 connection before browsing its objects")
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	if connectionType == "sftp" && (u.Port() == "" || u.Port() == "0") {
		u.Host = net.JoinHostPort(u.Hostname(), "22")
	}
	prefix := strings.Trim(u.Path, "/")
	if err := databrowser.ValidateStoragePath(prefix, true); err != nil {
		return storageRoot{}, err
	}
	if prefix != "" {
		prefix += "/"
	}
	u.Path, u.RawPath = "/"+prefix, ""
	return storageRoot{url: *u, prefix: prefix}, nil
}

func (r storageRoot) pattern(relative string) (string, error) {
	if err := databrowser.ValidateStoragePath(relative, true); err != nil {
		return "", err
	}
	u := r.url
	u.Path = "/" + r.prefix + relative
	if u.Scheme == "sftp" && u.Path == "/" {
		// Sling strips one URL separator before Stat. Preserve the absolute
		// root slash so it lists root children rather than returning root itself.
		u.Path = "//"
	}
	return u.String(), nil
}

// BrowseStorage uses resolved Load credentials for metadata-only S3/SFTP listing.
// S3 filters in ListObjectsV2 before the cap; SFTP retains bounded Sling discovery.
func (s *LoadService) BrowseStorage(ctx context.Context, connection string, query databrowser.StorageQuery, environment string) (databrowser.StorageListing, error) {
	if s.deps.NewConnectionManager == nil {
		return databrowser.StorageListing{}, fmt.Errorf("storage browsing is unavailable")
	}
	if err := query.Validate(); err != nil {
		return databrowser.StorageListing{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manager, err := s.deps.NewConnectionManager(ctx, environment)
	if err != nil {
		return databrowser.StorageListing{}, fmt.Errorf("could not resolve the storage connection; check its settings")
	}
	uri, err := loadConnectionURI(manager, connection)
	if err != nil {
		return databrowser.StorageListing{}, fmt.Errorf("could not resolve the storage connection; check its settings")
	}
	root, err := storageBrowseRoot(uri, manager.GetConnectionType(connection))
	if err != nil {
		return databrowser.StorageListing{}, err
	}
	if root.url.Scheme == "s3" {
		release, err := sharedSlingProcessLimiter.acquire(ctx)
		if err != nil {
			return databrowser.StorageListing{}, fmt.Errorf("storage browsing was cancelled")
		}
		defer release()
		client, err := newStorageS3Client(ctx, uri)
		if err == nil {
			var listing databrowser.StorageListing
			listing, err = listS3Storage(ctx, client, root, query)
			if err == nil {
				return listing, nil
			}
		}
		return databrowser.StorageListing{}, fmt.Errorf("could not list this storage location; check connection settings and list permissions")
	}
	if query.NamePrefix != "" || query.Exact {
		return databrowser.StorageListing{}, fmt.Errorf("name-prefix filtering is supported for S3 connections")
	}
	pattern, err := root.pattern(query.Prefix)
	if err != nil {
		return databrowser.StorageListing{}, err
	}
	name, args, err := loadCommand(ctx, []string{"conns", "discover", loadDiscoverEnvName, "-o", "json", "--pattern", pattern}, nil)
	if err != nil {
		return databrowser.StorageListing{}, fmt.Errorf("could not start Sling for storage browsing")
	}
	cmd := newStreamingCommand(ctx, name, args, s.deps.WorkspaceRoot, nil)
	cmd.Env = append(cmd.Env, loadDiscoverEnvName+"="+uri, "SLING_RECURSIVE_LIMIT=501")
	capture := &storageDiscoveryCapture{cancel: cancel}
	cmd.Stdout, cmd.Stderr = capture, capture
	if err := runSlingCommand(ctx, cmd); err != nil {
		if capture.overflow {
			return databrowser.StorageListing{}, fmt.Errorf("this directory listing is too large; narrow the connection's storage prefix")
		}
		return databrowser.StorageListing{}, fmt.Errorf("could not list this storage location; check connection settings and list permissions")
	}
	return parseStorageDiscovery(capture.String(), root, query.Prefix)
}

type storageDiscoveryCapture struct {
	bytes.Buffer
	cancel   context.CancelFunc
	overflow bool
}

func (w *storageDiscoveryCapture) Write(p []byte) (int, error) {
	if w.Len()+len(p) > 1<<20 {
		w.overflow = true
		w.cancel()
		return 0, io.ErrShortWrite
	}
	return w.Buffer.Write(p)
}

func parseStorageDiscovery(output string, root storageRoot, prefix string) (databrowser.StorageListing, error) {
	payload, ok := decodeLoadDiscoverPayload(output)
	if !ok {
		return databrowser.StorageListing{}, fmt.Errorf("Sling returned an invalid storage listing")
	}
	nameIndex, typeIndex := -1, -1
	for i, field := range payload.Fields {
		switch strings.ToLower(field) {
		case "name":
			nameIndex = i
		case "type":
			typeIndex = i
		}
	}
	if nameIndex < 0 || typeIndex < 0 {
		return databrowser.StorageListing{}, fmt.Errorf("Sling returned an unsupported storage listing")
	}
	result := databrowser.StorageListing{Entries: []databrowser.StorageEntry{}, Truncated: len(payload.Rows) > maxLoadDiscoveryStreams}
	seen := map[string]bool{}
	for _, row := range payload.Rows {
		if len(row) <= max(nameIndex, typeIndex) {
			return databrowser.StorageListing{}, fmt.Errorf("Sling returned an incomplete storage listing")
		}
		name, ok := row[nameIndex].(string)
		if !ok {
			return databrowser.StorageListing{}, fmt.Errorf("Sling returned an invalid object name")
		}
		kind := strings.ToLower(loadCellString(row[typeIndex]))
		directory := kind == "folder" || kind == "directory" || kind == "dir" || kind == "prefix"
		if !directory && kind != "file" && kind != "object" {
			continue
		}
		if strings.Contains(name, "://") {
			u, err := url.Parse(name)
			if err != nil || u.Scheme != root.url.Scheme || u.Host != root.url.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				continue
			}
			name = u.Path
		}
		// Sling's SFTP provider emits // between authority and absolute paths.
		// Authority was checked above; normalize only this leading separator.
		name = strings.TrimLeft(name, "/")
		if !strings.HasPrefix(name, root.prefix) {
			continue
		}
		relative := strings.TrimPrefix(name, root.prefix)
		if directory && !strings.HasSuffix(relative, "/") {
			relative += "/"
		}
		if err := databrowser.ValidateStoragePath(relative, false); err != nil {
			continue
		}
		parent := ""
		if index := strings.LastIndexByte(strings.TrimSuffix(relative, "/"), '/'); index >= 0 {
			parent = relative[:index+1]
		}
		if parent != prefix || seen[relative] {
			continue
		}
		seen[relative] = true
		reference, err := root.pattern(relative)
		if err != nil {
			continue
		}
		result.Entries = append(result.Entries, databrowser.StorageEntry{Path: relative, Directory: directory, Reference: reference})
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
