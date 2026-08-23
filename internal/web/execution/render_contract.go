package execution

// RenderStatus is the aggregate result of statically rendering one asset.
// renart:web-name AssetRenderStatus
type RenderStatus string

const (
	RenderStatusOK          RenderStatus = "ok"
	RenderStatusPartial     RenderStatus = "partial"
	RenderStatusUnsupported RenderStatus = "unsupported"
	RenderStatusError       RenderStatus = "error"
)

// renart:web-name AssetRenderStageStatus
type RenderStageStatus string

const (
	RenderStageStatusOK          RenderStageStatus = "ok"
	RenderStageStatusUnsupported RenderStageStatus = "unsupported"
	RenderStageStatusError       RenderStageStatus = "error"
)

// renart:web-name AssetRenderFidelity
type RenderFidelity string

const (
	RenderFidelityExact       RenderFidelity = "exact"
	RenderFidelitySemantic    RenderFidelity = "semantic"
	RenderFidelityRuntimeOnly RenderFidelity = "runtime_only"
	RenderFidelityUnsupported RenderFidelity = "unsupported"
)

// RenderRequest is the public, secret-free context for rendering one saved
// asset without executing it.
// renart:web
// renart:web-name AssetRenderRequest
type RenderRequest struct {
	Environment   string `json:"environment,omitempty"`
	StartDate     string `json:"start_date,omitempty"`
	EndDate       string `json:"end_date,omitempty"`
	ExecutionTime string `json:"execution_time,omitempty"`
	FullRefresh   bool   `json:"full_refresh"`
}

// renart:web-name AssetRenderSource
type RenderSource struct {
	Kind              string `json:"kind"`
	VersionID         string `json:"version_id,omitempty"`
	DeploymentOrdinal int64  `json:"deployment_ordinal,omitempty"`
	PipelinePath      string `json:"pipeline_path"`
	MerkleRoot        string `json:"merkle_root"`
}

// renart:web-name AssetRenderContext
type RenderContext struct {
	Environment           string               `json:"environment,omitempty"`
	SchemaPrefix          string               `json:"schema_prefix,omitempty"`
	StartDate             string               `json:"start_date"`
	EndDate               string               `json:"end_date"`
	ExecutionTime         string               `json:"execution_time"`
	RunID                 string               `json:"run_id"`
	RequestedFullRefresh  bool                 `json:"requested_full_refresh"`
	FullRefresh           bool                 `json:"full_refresh"`
	VariablesDigest       string               `json:"variables_digest"`
	CoverageVariablesHash string               `json:"coverage_variables_hash"`
	VariableProvenance    []VariableProvenance `json:"variable_provenance"`
	ConfigurationDigest   string               `json:"configuration_digest"`
	ConfigurationFidelity string               `json:"configuration_fidelity"`
	ConfigurationMessage  string               `json:"configuration_message,omitempty"`
}

// renart:web-name AssetRenderVariableProvenance
type VariableProvenance struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// renart:web-name AssetRenderProvenance
type RenderProvenance struct {
	Source   RenderSource  `json:"source"`
	Pipeline string        `json:"pipeline"`
	Context  RenderContext `json:"context"`
}

// RenderTarget is the mutable physical output selected by the same saved
// asset and environment configuration used by execution. Identity is empty
// unless Fidelity is exact.
// renart:web-name AssetRenderTarget
type RenderTarget struct {
	Kind          string         `json:"kind"`
	Object        string         `json:"object,omitempty"`
	Identity      string         `json:"identity,omitempty"`
	Fidelity      RenderFidelity `json:"fidelity"`
	Message       string         `json:"message,omitempty"`
	WriteResource WriteResource  `json:"write_resource"`
}

// WriteResource is the exclusive mutation resource selected by the same
// resolver as execution. Identity is an opaque secret-free digest.
// renart:web-name AssetRenderWriteResource
type WriteResource struct {
	Kind     string         `json:"kind"`
	Identity string         `json:"identity,omitempty"`
	Fidelity RenderFidelity `json:"fidelity"`
	Message  string         `json:"message,omitempty"`
}

// renart:web-name AssetRenderAsset
type RenderAsset struct {
	ID             string       `json:"id,omitempty"`
	Name           string       `json:"name"`
	Type           string       `json:"type"`
	Dialect        string       `json:"dialect,omitempty"`
	ConnectionName string       `json:"connection_name,omitempty"`
	Fingerprint    string       `json:"fingerprint,omitempty"`
	Target         RenderTarget `json:"target"`
}

// renart:web-name AssetRenderStage
type RenderStage struct {
	Kind          string            `json:"kind"`
	Label         string            `json:"label,omitempty"`
	Language      string            `json:"language"`
	Content       string            `json:"content,omitempty"`
	Status        RenderStageStatus `json:"status"`
	Fidelity      RenderFidelity    `json:"fidelity"`
	Conditional   bool              `json:"conditional,omitempty"`
	CheckKind     string            `json:"check_kind,omitempty"`
	CheckName     string            `json:"check_name,omitempty"`
	CheckColumn   string            `json:"check_column,omitempty"`
	CheckBlocking *bool             `json:"check_blocking,omitempty"`
	Redacted      bool              `json:"redacted,omitempty"`
	Message       string            `json:"message,omitempty"`
}

// renart:web-name AssetRenderRedaction
type RenderRedaction struct {
	Kind        string `json:"kind"`
	Replacement string `json:"replacement"`
}

// renart:web-name AssetRenderIssue
type RenderIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// RenderResult is the complete secret-free render result for one asset.
// renart:web
// renart:web-name AssetRenderResult
type RenderResult struct {
	Status     RenderStatus      `json:"status"`
	Provenance RenderProvenance  `json:"provenance"`
	Asset      RenderAsset       `json:"asset"`
	Stages     []RenderStage     `json:"stages"`
	Issues     []RenderIssue     `json:"issues"`
	Redactions []RenderRedaction `json:"redactions"`
}
