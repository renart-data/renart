// Package typecheck owns the value contracts produced by pipeline and
// presentation type-checking. The checker implementation can migrate behind
// these contracts without making execution planning depend on the broad
// service facade.
package typecheck

import (
	webmodel "renart/internal/web/model"
	"renart/internal/web/navigationtarget"
)

// Finding is a single diagnostic about an asset (a type/column error from the
// SQL parser, a template-rendering failure, or an asset-source warning).
// renart:web-name TypeCheckFinding
type Finding struct {
	Code                        string                   `json:"code"`
	Source                      string                   `json:"source"`
	Severity                    string                   `json:"severity"`
	Message                     string                   `json:"message"`
	Line                        int                      `json:"line,omitempty"`
	Column                      int                      `json:"column,omitempty"`
	EndLine                     int                      `json:"end_line,omitempty"`
	EndColumn                   int                      `json:"end_column,omitempty"`
	Scope                       string                   `json:"scope,omitempty"`
	Confidence                  string                   `json:"confidence,omitempty"`
	SourceFingerprint           string                   `json:"source_fingerprint,omitempty"`
	Resolutions                 []Resolution             `json:"resolutions,omitempty"`
	Target                      *navigationtarget.Target `json:"target,omitempty"`
	NavigationUnavailableReason string                   `json:"navigation_unavailable_reason,omitempty"`
}

// Resolution is a safe semantic edit Renart can offer for a finding.
// renart:web-name TypeCheckResolution
type Resolution struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Transaction *ResolutionTransaction `json:"transaction,omitempty"`
	Action      *ResolutionAction      `json:"action,omitempty"`
}

// renart:web-name TypeCheckResolutionTransaction
type ResolutionTransaction struct {
	Type       string                          `json:"type"`
	Column     string                          `json:"column,omitempty"`
	Dependency *webmodel.TransactionDependency `json:"dependency,omitempty"`
}

// renart:web-name TypeCheckResolutionAction
type ResolutionAction struct {
	Type       string `json:"type"`
	RelationID string `json:"relation_id,omitempty"`
	PipelineID string `json:"pipeline_id,omitempty"`
	AssetID    string `json:"asset_id,omitempty"`
}

// Asset is the per-asset result of a pipeline type check.
// renart:web-name TypeCheckAsset
type Asset struct {
	ID       string    `json:"id,omitempty"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Dialect  string    `json:"dialect,omitempty"`
	Status   string    `json:"status"`
	Findings []Finding `json:"findings"`
}

// Presentation is one Git-native dashboard or report that consumes an asset
// in the checked pipeline.
// renart:web-name TypeCheckPresentation
type Presentation struct {
	ID          string                `json:"id"`
	WorkspaceID string                `json:"workspace_id"`
	Kind        string                `json:"kind"`
	Title       string                `json:"title"`
	Path        string                `json:"path"`
	Status      string                `json:"status"`
	Findings    []PresentationFinding `json:"findings"`
}

// renart:web-name TypeCheckPresentationFinding
type PresentationFinding struct {
	Target       *navigationtarget.Target `json:"target,omitempty"`
	Code         string                   `json:"code"`
	Severity     string                   `json:"severity"`
	Message      string                   `json:"message"`
	Path         string                   `json:"path,omitempty"`
	Field        string                   `json:"field,omitempty"`
	PhysicalType string                   `json:"physical_type,omitempty"`
}

// Summary aggregates finding counts across the pipeline.
// renart:web-name TypeCheckSummary
type Summary struct {
	Assets        int `json:"assets"`
	Presentations int `json:"presentations,omitempty"`
	Errors        int `json:"errors"`
	Warnings      int `json:"warnings"`
}

// ExternalRelation is positive, ephemeral catalog evidence used by the canvas
// and import review. It is never persisted as workspace state.
// renart:web-name TypeCheckExternalRelation
type ExternalRelation struct {
	ID                     string               `json:"id"`
	Connection             string               `json:"connection"`
	Environment            string               `json:"environment,omitempty"`
	QualifiedName          string               `json:"qualified_name"`
	SchemaName             string               `json:"schema_name,omitempty"`
	Name                   string               `json:"name"`
	Columns                []webmodel.SQLColumn `json:"columns"`
	ColumnsKnown           bool                 `json:"columns_known"`
	ObservedAt             string               `json:"observed_at,omitempty"`
	Stale                  bool                 `json:"stale,omitempty"`
	ReferencedByAssetIDs   []string             `json:"referenced_by_asset_ids"`
	ReferencedByAssetNames []string             `json:"referenced_by_asset_names"`
}

// CrossPipelineReference links a SQL relation use to a producer in another
// pipeline before the consumer declares Bruin's explicit URI dependency.
// renart:web-name TypeCheckCrossPipelineReference
type CrossPipelineReference struct {
	ID                   string `json:"id"`
	Status               string `json:"status"`
	Relation             string `json:"relation"`
	ConsumerAssetID      string `json:"consumer_asset_id"`
	ConsumerAssetName    string `json:"consumer_asset_name"`
	ProducerAssetID      string `json:"producer_asset_id"`
	ProducerAssetName    string `json:"producer_asset_name"`
	ProducerPipelineID   string `json:"producer_pipeline_id"`
	ProducerPipelineName string `json:"producer_pipeline_name"`
	ProducerURI          string `json:"producer_uri,omitempty"`
}

// Report is the full result of type-checking a pipeline.
// renart:web
// renart:web-name TypeCheckReport
type Report struct {
	Status                  string                   `json:"status"`
	PipelineID              string                   `json:"pipeline_id,omitempty"`
	PipelineName            string                   `json:"pipeline_name"`
	StartDate               string                   `json:"start_date,omitempty"`
	EndDate                 string                   `json:"end_date,omitempty"`
	Assets                  []Asset                  `json:"assets"`
	Presentations           []Presentation           `json:"presentations,omitempty"`
	ExternalRelations       []ExternalRelation       `json:"external_relations,omitempty"`
	CrossPipelineReferences []CrossPipelineReference `json:"cross_pipeline_references,omitempty"`
	Summary                 Summary                  `json:"summary"`
}
