package databrowser

import (
	"context"
	"path"
	"strings"
	"time"

	"renart/internal/web/apperror"
)

const maxStorageSearchListings = 32

// SearchStorage interprets wildcards here, never in a provider command or object
// reference. Every provider request and every returned action target is literal.
// Only matched branches are expanded; all work shares one deadline and budget.
func (s *Service) SearchStorage(ctx context.Context, connectionID, pattern, environment string) (ChildrenResponse, *apperror.Error) {
	scope, apiErr := s.resolveScope(ctx, connectionID, environment)
	if apiErr != nil {
		return ChildrenResponse{}, apiErr
	}
	if scope.ref.SourceKind != "storage" {
		return ChildrenResponse{}, badRequest("data_browser_search_unsupported", "Choose a storage connection for wildcard search.")
	}
	if strings.Contains(pattern, "**") || strings.ContainsAny(pattern, "[]") {
		return ChildrenResponse{}, badRequest("data_browser_pattern_invalid", "Use * and ? within a path segment; recursive ** and character classes are not supported.")
	}
	literal := strings.NewReplacer("*", "x", "?", "x").Replace(pattern)
	if err := ValidateStoragePath(literal, false); err != nil {
		return ChildrenResponse{}, badRequest("data_browser_pattern_invalid", err.Error())
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "*"
	}
	parts := strings.Split(pattern, "/")
	if len(parts) > 32 {
		return ChildrenResponse{}, badRequest("data_browser_pattern_invalid", "This path has too many levels.")
	}
	first := 0
	for first < len(parts)-1 && !strings.ContainsAny(parts[first], "*?") {
		first++
	}
	root := strings.Join(parts[:first], "/")
	if root != "" {
		root += "/"
	}
	type branch struct {
		prefix string
		level  int
	}
	pending := []branch{{root, first}}
	result := ChildrenResponse{Status: "ok", ConnectionID: connectionID, Revision: scope.revision, Nodes: []Node{}}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for listed := 0; len(pending) > 0; listed++ {
		if listed == maxStorageSearchListings {
			result.Truncated = true
			break
		}
		if ctx.Err() != nil {
			return ChildrenResponse{}, internalError("data_browser_discovery_failed", ctx.Err())
		}
		current := pending[0]
		pending = pending[1:]
		parent := scope.ref
		parent.Path = current.prefix
		parentID := ""
		if parent.Path != "" {
			parent.Kind = "storage_prefix"
			parentID = encodeRef(parent)
		}
		segment := parts[current.level]
		query := StorageQuery{Prefix: current.prefix}
		if scope.ref.ConnectionType == "s3" {
			query.NamePrefix = segment
			if wildcard := strings.IndexAny(segment, "*?"); wildcard >= 0 {
				query.NamePrefix = segment[:wildcard]
			}
		}
		nodes, truncated, err := s.storageChildrenMatching(ctx, scope.ref, parent, parentID, query)
		if err != nil {
			return ChildrenResponse{}, internalError("data_browser_discovery_failed", err)
		}
		result.Truncated = result.Truncated || truncated
		for _, node := range nodes {
			match, _ := path.Match(segment, node.Label)
			if !match {
				continue
			}
			if current.level < len(parts)-1 {
				if node.NodeType == "namespace" {
					if len(pending)+listed+1 >= maxStorageSearchListings {
						result.Truncated = true
						continue
					}
					pending = append(pending, branch{current.prefix + node.Label + "/", current.level + 1})
				}
				continue
			}
			if len(result.Nodes) == maxChildren {
				result.Truncated = true
				return result, nil
			}
			node.Label = strings.TrimPrefix(current.prefix+node.Label, root)
			result.Nodes = append(result.Nodes, node)
		}
	}
	return result, nil
}
