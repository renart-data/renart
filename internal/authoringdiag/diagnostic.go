// Package authoringdiag defines diagnostics shared by pipeline type-checking
// and editor/LSP transports. Byte offsets are the neutral range format;
// adapters convert them to their transport-specific line and column model.
package authoringdiag

import (
	"fmt"
	"sort"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
	SeverityHint    Severity = "hint"
)

type Scope string

const (
	ScopeDocument Scope = "document"
	ScopeAsset    Scope = "asset"
	ScopePipeline Scope = "pipeline"
)

type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

const (
	SourcePolyglot = "polyglot"
	SourceRenart   = "renart"
)

const (
	CodeSQLSyntax                       = "sql-syntax"
	CodeSQLValidationFailed             = "sql-validation-failed"
	CodeUnresolvedRelation              = "unresolved-relation"
	CodeUnresolvedAlias                 = "unresolved-alias"
	CodeUnresolvedColumn                = "unresolved-column"
	CodeSQLTypeMismatch                 = "sql-type-mismatch"
	CodeDeclaredOutputSchemaDrift       = "declared-output-schema-drift"
	CodeDeclaredColumnTypeDrift         = "declared-column-type-drift"
	CodeDeclaredColumnNullabilityDrift  = "declared-column-nullability-drift"
	CodeUnmaterializedColumn            = "unmaterialized-column"
	CodeCrossConnectionReference        = "cross-connection-reference"
	CodeExternalRelation                = "external-relation"
	CodeCrossPipelineDependencyMissing  = "undeclared-cross-pipeline-dependency"
	CodeCrossPipelineRelationAmbiguous  = "ambiguous-cross-pipeline-relation"
	CodeDependencyValidationFailed      = "dependency-validation-failed"
	CodeMissingDependency               = "missing-dependency"
	CodeCrossPipelineDuplicateURI       = "cross-pipeline-duplicate-uri"
	CodeCrossPipelineAmbiguousURI       = "cross-pipeline-ambiguous-uri"
	CodeCrossPipelineUnresolvedURI      = "cross-pipeline-unresolved-uri"
	CodeCrossPipelineInvalidProducer    = "cross-pipeline-invalid-producer"
	CodeCrossPipelineCycle              = "cross-pipeline-cycle"
	CodeCrossPipelineExecutionPending   = "cross-pipeline-execution-unsupported"
	CodeInvalidMaterialization          = "invalid-materialization"
	CodeInactiveMaterialization         = "inactive-materialization-metadata"
	CodeMissingDeclaredColumns          = "missing-declared-columns"
	CodePythonUndeclaredQueryDependency = "python-undeclared-query-dependency"
	CodeTemplateRenderFailed            = "template-render-failed"
	CodeAssetDefinitionParseFailed      = "asset-definition-parse-failed"
	// CodeDuckDBFilesystemAccessDisabled is an editor-only policy diagnostic.
	// Pipeline type-checking does not emit it because the setting belongs to the
	// running Renart process rather than the version-controlled asset.
	CodeDuckDBFilesystemAccessDisabled = "duckdb-filesystem-access-disabled"
)

// Subject identifies the semantic fact involved, not a UI route or message span.
// Column preserves the declaration's spelling so adapters never guess identity
// by parsing translated prose or case-folding SQL identifiers.
type Subject struct {
	Column string `json:"column,omitempty"`
	Field  string `json:"field,omitempty"`
}

type Diagnostic struct {
	Code       string
	Source     string
	Severity   Severity
	Message    string
	URI        string
	StartByte  *int
	EndByte    *int
	Scope      Scope
	Confidence Confidence
	Subject    *Subject
}

func (d Diagnostic) Key() string {
	start, end := -1, -1
	if d.StartByte != nil {
		start = *d.StartByte
	}
	if d.EndByte != nil {
		end = *d.EndByte
	}
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", d.Code, d.URI, d.Message, start, end)
}

type EditorDelivery string

const (
	DeliveryDocument     EditorDelivery = "document"
	DeliveryAssetHeader  EditorDelivery = "asset/header"
	DeliveryPipelineOnly EditorDelivery = "pipeline-only"
)

// typeCheckDelivery is an explicit parity registry. Any stable code emitted by
// pipeline type-checking must be classified here; tests enforce that contract.
var typeCheckDelivery = map[string]EditorDelivery{
	CodeSQLSyntax:                       DeliveryDocument,
	CodeSQLValidationFailed:             DeliveryDocument,
	CodeUnresolvedRelation:              DeliveryDocument,
	CodeUnresolvedAlias:                 DeliveryDocument,
	CodeUnresolvedColumn:                DeliveryDocument,
	CodeSQLTypeMismatch:                 DeliveryDocument,
	CodeDeclaredOutputSchemaDrift:       DeliveryAssetHeader,
	CodeDeclaredColumnTypeDrift:         DeliveryAssetHeader,
	CodeDeclaredColumnNullabilityDrift:  DeliveryAssetHeader,
	CodeUnmaterializedColumn:            DeliveryDocument,
	CodeCrossConnectionReference:        DeliveryDocument,
	CodeExternalRelation:                DeliveryDocument,
	CodeCrossPipelineDependencyMissing:  DeliveryDocument,
	CodeCrossPipelineRelationAmbiguous:  DeliveryDocument,
	CodeDependencyValidationFailed:      DeliveryAssetHeader,
	CodeMissingDependency:               DeliveryAssetHeader,
	CodeCrossPipelineDuplicateURI:       DeliveryAssetHeader,
	CodeCrossPipelineAmbiguousURI:       DeliveryAssetHeader,
	CodeCrossPipelineUnresolvedURI:      DeliveryAssetHeader,
	CodeCrossPipelineInvalidProducer:    DeliveryAssetHeader,
	CodeCrossPipelineCycle:              DeliveryAssetHeader,
	CodeInvalidMaterialization:          DeliveryAssetHeader,
	CodeInactiveMaterialization:         DeliveryAssetHeader,
	CodeMissingDeclaredColumns:          DeliveryAssetHeader,
	CodePythonUndeclaredQueryDependency: DeliveryDocument,
	CodeTemplateRenderFailed:            DeliveryAssetHeader,
	CodeAssetDefinitionParseFailed:      DeliveryAssetHeader,
}

func TypeCheckDelivery(code string) (EditorDelivery, bool) {
	delivery, ok := typeCheckDelivery[code]
	return delivery, ok
}

func RegisteredTypeCheckCodes() []string {
	codes := make([]string, 0, len(typeCheckDelivery))
	for code := range typeCheckDelivery {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func ByteRange(start, end int) (*int, *int) {
	return &start, &end
}
