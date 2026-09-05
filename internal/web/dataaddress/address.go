// Package dataaddress defines durable, project-scoped data identities. Addresses
// contain no credentials, SQL, revision tokens, or authority to execute queries.
package dataaddress

// renart:web-name DataObjectAddress
type Address struct {
	SourceKind     string `json:"source_kind"`
	Connection     string `json:"connection,omitempty"`
	ConnectionType string `json:"connection_type,omitempty"`
	Database       string `json:"database,omitempty"`
	Schema         string `json:"schema,omitempty"`
	Name           string `json:"name,omitempty"`
	Path           string `json:"path,omitempty"`
}
