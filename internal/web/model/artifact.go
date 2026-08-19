package model

// ArtifactIndex is a read-only projection of versioned workspace artifacts.
// It complements, rather than replaces, the Bruin pipeline and notebook run
// models. Execution planning continues to use each host's native graph.
type ArtifactIndex struct {
	Revision     string                `json:"revision"`
	Artifacts    []ArtifactDescriptor  `json:"artifacts"`
	Containment  []ArtifactContainment `json:"containment,omitempty"`
	Dependencies []ArtifactDependency  `json:"dependencies,omitempty"`
}

// ArtifactDescriptor is one Git/versioning and ownership unit. Capabilities
// are derived from the artifact kind and definition; callers cannot edit them.
type ArtifactDescriptor struct {
	ID           string              `json:"id"`
	Kind         string              `json:"kind"`
	WorkspaceID  string              `json:"workspace_id,omitempty"`
	Path         string              `json:"path"`
	Title        string              `json:"title"`
	Capabilities []string            `json:"capabilities"`
	Columns      []Column            `json:"columns,omitempty"`
	Components   []ArtifactComponent `json:"components,omitempty"`
}

// ArtifactComponent is a stable addressable child of an artifact. Components
// may participate in lineage and diagnostics without becoming top-level
// pipeline assets or default canvas nodes.
type ArtifactComponent struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Name         string   `json:"name,omitempty"`
	Path         string   `json:"path,omitempty"`
	AssetType    string   `json:"asset_type,omitempty"`
	Capabilities []string `json:"capabilities"`
	Columns      []Column `json:"columns,omitempty"`
}

// ArtifactRef addresses either an artifact container or one of its stable
// components. ComponentID is empty for a container-level reference.
type ArtifactRef struct {
	Kind        string `json:"kind"`
	ArtifactID  string `json:"artifact_id"`
	ComponentID string `json:"component_id,omitempty"`
}

// ArtifactContainment records ordered child membership independently of a
// host's execution graph.
type ArtifactContainment struct {
	Parent   ArtifactRef `json:"parent"`
	Child    ArtifactRef `json:"child"`
	Position int         `json:"position"`
}

// ArtifactColumnUsage explains which producer columns a consumer references.
// Role is a stable definition path such as encoding.x.field.
type ArtifactColumnUsage struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// ArtifactDependency is a resolved producer-to-consumer lineage edge. Empty
// Columns means relation-level dependency only.
type ArtifactDependency struct {
	Producer ArtifactRef           `json:"producer"`
	Consumer ArtifactRef           `json:"consumer"`
	Columns  []ArtifactColumnUsage `json:"columns,omitempty"`
}
