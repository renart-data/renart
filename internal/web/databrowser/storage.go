package databrowser

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode"

	"renart/internal/web/apperror"
)

// Storage listing is metadata-only. Paths are relative to the configured root;
// references may contain a bucket/path, but never credentials or URL options.
type StorageEntry struct {
	Path      string
	Reference string
	Directory bool
}

type StorageListing struct {
	Entries   []StorageEntry
	Truncated bool
}

// Sling treats globs and pipe-separated patterns as executable selectors. Do
// not let an object locator become a pattern, URL override or parent traversal.
func ValidateStoragePath(value string, allowRoot bool) error {
	if value == "" && allowRoot {
		return nil
	}
	if value == "" || len(value) > 4096 || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\*?[]{}|:") || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("choose a relative storage path without wildcards or URL options")
	}
	for _, part := range strings.Split(strings.TrimSuffix(value, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("storage paths cannot escape or alias the configured root")
		}
	}
	return nil
}

// Prefix lists an explicitly typed location without enumerating its ancestors.
// It creates only scoped discovery references; object handoff still revalidates
// the provider listing. The configured connection root remains authoritative.
func (s *Service) Prefix(ctx context.Context, connectionID, prefix, environment string) (ChildrenResponse, *apperror.Error) {
	scope, apiErr := s.resolveScope(ctx, connectionID, environment)
	if apiErr != nil {
		return ChildrenResponse{}, apiErr
	}
	if scope.ref.SourceKind != "storage" {
		return ChildrenResponse{}, badRequest("data_browser_prefix_unsupported", "Choose a storage connection to browse a prefix.")
	}
	if err := ValidateStoragePath(prefix, true); err != nil {
		return ChildrenResponse{}, badRequest("data_browser_prefix_invalid", err.Error())
	}
	parent := scope.ref
	parentID := ""
	if prefix != "" {
		parent.Kind, parent.Path = "storage_prefix", strings.TrimSuffix(prefix, "/")+"/"
		parentID = encodeRef(parent)
	}
	nodes, truncated, err := s.storageChildren(ctx, scope.ref, parent, parentID)
	if err != nil {
		return ChildrenResponse{}, internalError("data_browser_discovery_failed", err)
	}
	return ChildrenResponse{Status: "ok", ConnectionID: connectionID, ParentID: parentID, Revision: scope.revision, Nodes: nodes, Truncated: truncated}, nil
}

func (s *Service) storageChildren(ctx context.Context, connection, parent objectRef, parentID string) ([]Node, bool, error) {
	if parent.Kind != "connection" && parent.Kind != "storage_prefix" {
		return nil, false, fmt.Errorf("choose a storage prefix")
	}
	if err := ValidateStoragePath(parent.Path, parent.Kind == "connection"); err != nil {
		return nil, false, err
	}
	listing, err := s.deps.ListStorage(ctx, connection.Connection, parent.Path, connection.Environment)
	if err != nil {
		return nil, false, err
	}
	nodes := make([]Node, 0, min(len(listing.Entries), maxChildren))
	for _, entry := range listing.Entries {
		if err := ValidateStoragePath(entry.Path, false); err != nil {
			continue
		}
		if storageParent(entry.Path) != parent.Path {
			continue
		}
		ref := connection
		ref.Kind, ref.Path = "storage_object", entry.Path
		node := Node{ParentID: parentID, NodeType: "object", ObjectKind: "file", Label: path.Base(strings.TrimSuffix(entry.Path, "/")), ReferenceText: entry.Reference, Format: storageFormat(entry.Path)}
		if entry.Directory {
			ref.Kind = "storage_prefix"
			node.NodeType, node.NamespaceKind, node.ObjectKind, node.Format, node.HasChildren = "namespace", "prefix", "prefix", "", true
		}
		node.ID, node.Address = encodeRef(ref), addressForRef(ref)
		nodes = append(nodes, node)
		if len(nodes) == maxChildren {
			break
		}
	}
	return nodes, listing.Truncated || len(listing.Entries) > maxChildren, nil
}

func (s *Service) storageObject(ctx context.Context, scope resolvedScope, ref objectRef, object Object) (ObjectResponse, *apperror.Error) {
	if (ref.Kind != "storage_object" && ref.Kind != "storage_prefix") || ValidateStoragePath(ref.Path, false) != nil {
		return ObjectResponse{}, badRequest("data_browser_object_invalid", "Choose a valid storage object or prefix.")
	}
	parent := scope.ref
	parent.Path = storageParent(ref.Path)
	if parent.Path != "" {
		parent.Kind = "storage_prefix"
	}
	nodes, _, err := s.storageChildren(ctx, scope.ref, parent, "")
	if err != nil {
		return ObjectResponse{}, internalError("data_browser_discovery_failed", err)
	}
	for _, node := range nodes {
		if node.ID != object.ID {
			continue
		}
		object.Name, object.ReferenceText, object.Kind, object.Format = node.Label, node.ReferenceText, node.ObjectKind, node.Format
		object.Namespace = compactStrings(strings.Split(strings.TrimSuffix(parent.Path, "/"), "/"))
		object.Capabilities.LoadSource = true
		for _, config := range scope.connections {
			if config.Name == ref.Connection && config.Type == ref.ConnectionType {
				object.Capabilities.LoadDestination = config.AccessMode != "read_only"
			}
		}
		return ObjectResponse{Status: "ok", Object: object}, nil
	}
	return ObjectResponse{}, &apperror.Error{Status: http.StatusNotFound, Code: "data_browser_object_not_found", Message: "This storage object is no longer listed. Refresh the Data Browser and try again."}
}

func storageParent(value string) string {
	value = strings.TrimSuffix(value, "/")
	if index := strings.LastIndexByte(value, '/'); index >= 0 {
		return value[:index+1]
	}
	return ""
}

func storageFormat(value string) string {
	return supportedLocalExtensions[strings.ToLower(path.Ext(value))]
}
