// Package databrowser owns read-only discovery of configured data sources and
// project-local tabular files. Its DTOs deliberately contain no connection
// values or credentials.
package databrowser

import (
	"renart/internal/web/dataaddress"
	"renart/internal/web/model"
)

// renart:web-name DataBrowserCapabilities
type Capabilities struct {
	ListNamespaces  bool `json:"list_namespaces"`
	ListObjects     bool `json:"list_objects"`
	DescribeColumns bool `json:"describe_columns"`
	PreviewRows     bool `json:"preview_rows"`
	Query           bool `json:"query"`
}

// renart:web-name DataBrowserConnection
type Connection struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Type            string       `json:"type"`
	Environment     string       `json:"environment"`
	Revision        string       `json:"revision"`
	SourceKind      string       `json:"source_kind"`
	DiscoveryStatus string       `json:"discovery_status"`
	Capabilities    Capabilities `json:"capabilities"`
}

// renart:web
// renart:web-name DataBrowserConnectionsResponse
type ConnectionsResponse struct {
	Status      string       `json:"status"`
	Environment string       `json:"environment"`
	Revision    string       `json:"revision"`
	Connections []Connection `json:"connections"`
}

// renart:web-name DataBrowserNode
type Node struct {
	Address       *dataaddress.Address `json:"address,omitempty"`
	ID            string               `json:"id"`
	ParentID      string               `json:"parent_id,omitempty"`
	NodeType      string               `json:"node_type"`
	Label         string               `json:"label"`
	NamespaceKind string               `json:"namespace_kind,omitempty"`
	ObjectKind    string               `json:"object_kind,omitempty"`
	HasChildren   bool                 `json:"has_children"`
	ReferenceText string               `json:"reference_text,omitempty"`
	Format        string               `json:"format,omitempty"`
	SizeBytes     int64                `json:"size_bytes,omitempty"`
	ModifiedAt    string               `json:"modified_at,omitempty"`
}

// renart:web
// renart:web-name DataBrowserChildrenResponse
type ChildrenResponse struct {
	Status       string `json:"status"`
	ConnectionID string `json:"connection_id"`
	ParentID     string `json:"parent_id,omitempty"`
	Revision     string `json:"revision"`
	Nodes        []Node `json:"nodes"`
	Truncated    bool   `json:"truncated,omitempty"`
}

// renart:web-name DataBrowserObject
type Object struct {
	Address        *dataaddress.Address `json:"address,omitempty"`
	ID             string               `json:"id"`
	ConnectionID   string               `json:"connection_id"`
	ConnectionName string               `json:"connection_name"`
	ConnectionType string               `json:"connection_type"`
	Environment    string               `json:"environment"`
	Revision       string               `json:"revision"`
	Namespace      []string             `json:"namespace"`
	Name           string               `json:"name"`
	Kind           string               `json:"kind"`
	ReferenceText  string               `json:"reference_text"`
	Format         string               `json:"format,omitempty"`
	SizeBytes      int64                `json:"size_bytes,omitempty"`
	ModifiedAt     string               `json:"modified_at,omitempty"`
	Columns        []model.SQLColumn    `json:"columns"`
	Capabilities   Capabilities         `json:"capabilities"`
	Warning        string               `json:"warning,omitempty"`
}

// renart:web
// renart:web-name DataBrowserObjectResponse
type ObjectResponse struct {
	Status string `json:"status"`
	Object Object `json:"object"`
}

// renart:web
// renart:web-name DataBrowserResolveRequest
type ResolveRequest struct {
	Environment string              `json:"environment"`
	Address     dataaddress.Address `json:"address"`
}

// renart:web
// renart:web-name DataBrowserPreviewRequest
type PreviewRequest struct {
	ObjectID    string `json:"object_id"`
	Environment string `json:"environment,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// renart:web
// renart:web-name DataBrowserPreviewResponse
type PreviewResponse struct {
	Status    string           `json:"status"`
	ObjectID  string           `json:"object_id"`
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated,omitempty"`
	ElapsedMS int64            `json:"elapsed_ms"`
}
