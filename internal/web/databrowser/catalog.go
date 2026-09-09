package databrowser

import (
	"context"
	"fmt"

	"renart/internal/web/sqlnamespace"
)

func (s *Service) catalogChildren(ctx context.Context, connection, parent objectRef, parentID string) ([]Node, bool, error) {
	switch parent.Kind {
	case "connection", "catalog", "database", "schema":
	default:
		return []Node{}, false, nil
	}
	scope := sqlnamespace.Scope{Catalog: parent.Catalog, Database: parent.Database, Schema: parent.Schema}
	entries, err := s.deps.ListWarehouse(ctx, connection.Connection, scope, connection.Environment)
	if err != nil {
		return nil, false, err
	}
	truncated := len(entries) > maxChildren
	if truncated {
		entries = entries[:maxChildren]
	}
	nodes := make([]Node, 0, len(entries))
	for _, entry := range entries {
		ref := connection
		ref.Kind, ref.Catalog, ref.Database, ref.Schema = entry.Kind, entry.Scope.Catalog, entry.Scope.Database, entry.Scope.Schema
		node := Node{ParentID: parentID, Label: entry.Name, NodeType: "namespace", NamespaceKind: entry.Kind, HasChildren: true, IsDefault: entry.Default}
		switch entry.Kind {
		case "catalog", "database", "schema":
		case "table":
			ref.Name, ref.LeafName = entry.Reference, entry.Name
			node.NodeType, node.ObjectKind, node.NamespaceKind, node.HasChildren = "object", "table", "", false
			node.ReferenceText, node.Address = entry.Reference, addressForRef(ref)
		default:
			return nil, false, fmt.Errorf("invalid warehouse namespace kind")
		}
		node.ID = encodeRef(ref)
		nodes = append(nodes, node)
	}
	return nodes, truncated, nil
}
