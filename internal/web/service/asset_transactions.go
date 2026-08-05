package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"

	webmodel "renart/internal/web/model"
	"renart/internal/web/service/assetmeta"
)

// AssetTransaction is a single semantic edit to an asset's Bruin definition
// (§11). UI surfaces emit transactions rather than writing YAML directly, so
// ownership and provenance are enforced in one place. The Type field selects
// which of the optional payloads is used.
type AssetTransaction struct {
	Type string `json:"type"`

	// Dependency transactions.
	DependencyKey string                 `json:"dependency_key,omitempty"`
	Dependency    *TransactionDependency `json:"dependency,omitempty"`

	// Column transactions.
	Column      string                `json:"column,omitempty"`
	Field       string                `json:"field,omitempty"`
	Description string                `json:"description,omitempty"`
	Check       *webmodel.ColumnCheck `json:"check,omitempty"`
	ColumnDef   *webmodel.Column      `json:"column_def,omitempty"`

	// Asset-level custom-check transactions. CustomCheckName identifies the
	// current check for updates/removals so its display name can be changed.
	CustomCheckName string                `json:"custom_check_name,omitempty"`
	CustomCheck     *webmodel.CustomCheck `json:"custom_check,omitempty"`

	// Asset-level SQL hook transactions. A nil HookIndex appends on upsert;
	// otherwise it identifies the existing hook within the selected phase.
	HookPhase string `json:"hook_phase,omitempty"`
	HookIndex *int   `json:"hook_index,omitempty"`
	HookQuery string `json:"hook_query,omitempty"`
}

// TransactionDependency describes a dependency to add manually.
type TransactionDependency struct {
	Asset string `json:"asset,omitempty"`
	URI   string `json:"uri,omitempty"`
	Mode  string `json:"mode,omitempty"`
}

// AssetTransactionResult is the post-transaction asset state the UI needs to
// refresh its cards.
type AssetTransactionResult struct {
	Status         string                    `json:"status"`
	Upstreams      []string                  `json:"upstreams"`
	Columns        []WorkspaceColumn         `json:"columns"`
	CustomChecks   []webmodel.CustomCheck    `json:"custom_checks"`
	PreHooks       []string                  `json:"pre_hooks"`
	PostHooks      []string                  `json:"post_hooks"`
	ReconcileItems []assetmeta.ReconcileItem `json:"reconcile_items,omitempty"`
}

// Supported transaction types.
const (
	TxDependencyManualAdd             = "dependency.manual.add"
	TxDependencyManualRemove          = "dependency.manual.remove"
	TxDependencyInferredIgnore        = "dependency.inferred.ignore"
	TxDependencyInferredRestore       = "dependency.inferred.restore"
	TxColumnManualAdd                 = "column.manual.add"
	TxColumnInferredDrop              = "column.inferred.drop"
	TxColumnInferredRestore           = "column.inferred.restore"
	TxColumnFieldOwn                  = "column.field.own"
	TxColumnFieldDisown               = "column.field.disown"
	TxColumnCheckAdd                  = "column.check.add"
	TxColumnCheckRemove               = "column.check.remove"
	TxColumnDescriptionSet            = "column.description.set"
	TxCustomCheckUpsert               = "custom_check.upsert"
	TxCustomCheckRemove               = "custom_check.remove"
	TxColumnMergeSettingsClear        = "column.merge_settings.clear"
	TxMaterializationPartitionByClear = "materialization.partition_by.clear"
	TxMaterializationClusterByClear   = "materialization.cluster_by.clear"
	TxHookUpsert                      = "hook.upsert"
	TxHookRemove                      = "hook.remove"
)

// ApplyAssetTransaction applies a single semantic transaction to an asset,
// updating its upstreams/columns and the renart provenance, and persists the
// result. Dependency and column inference is not re-run here; the change is
// applied directly and a subsequent SQL reconcile normalizes it.
func (s *AssetService) ApplyAssetTransaction(ctx context.Context, assetID string, tx AssetTransaction) (AssetTransactionResult, *APIError) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return AssetTransactionResult{}, badRequestError("invalid_asset_id", "invalid asset id")
	}
	absAssetPath, err := s.resolver().JoinPath(relAssetPath)
	if err != nil {
		return AssetTransactionResult{}, badRequestError("invalid_asset_path", err.Error())
	}

	unlock := s.lockAssetFile(absAssetPath)
	defer unlock()

	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return AssetTransactionResult{}, badRequestError("asset_resolve_failed", err.Error())
	}

	meta := assetmeta.ParseAsset(asset)
	if apiErr := applyTransactionToAsset(asset, &meta, tx); apiErr != nil {
		return AssetTransactionResult{}, apiErr
	}
	meta.Version = assetmeta.SchemaVersion
	meta.Generator = assetmeta.GeneratorVersion
	meta.ApplyToAsset(asset)
	if apiErr := loaderMaterializationAPIError(asset); apiErr != nil {
		return AssetTransactionResult{}, apiErr
	}

	if apiErr := s.persistAssetPreservingInferredName(asset, parsedPipeline); apiErr != nil {
		return AssetTransactionResult{}, apiErr
	}

	s.deps.SuppressWatcher(relAssetPath)
	if s.deps.PushWorkspaceUpdateImmediate != nil {
		s.deps.PushWorkspaceUpdateImmediate(ctx, "asset.updated", relAssetPath)
	}

	return AssetTransactionResult{
		Status:       "ok",
		Upstreams:    upstreamNames(asset.Upstreams),
		Columns:      PipelineColumnsToModelColumns(asset.Columns),
		CustomChecks: PipelineCustomChecksToModelCustomChecks(asset.CustomChecks),
		PreHooks:     pipelineHookQueries(asset.Hooks.Pre),
		PostHooks:    pipelineHookQueries(asset.Hooks.Post),
	}, nil
}

func applyTransactionToAsset(asset *pipeline.Asset, meta *assetmeta.RenartMeta, tx AssetTransaction) *APIError {
	switch tx.Type {
	case TxDependencyManualAdd:
		if tx.Dependency == nil {
			return badRequestError("invalid_transaction", "dependency is required")
		}
		up := dependencyFromTransaction(tx.Dependency)
		if strings.TrimSpace(up.Value) == "" {
			return badRequestError("invalid_transaction", "dependency asset or uri is required")
		}
		key := assetmeta.DependencyKey(up)
		meta.DepAdd = appendDependencyKey(meta.DepAdd, key)
		meta.DepDrop = removeDependencyKey(meta.DepDrop, key)
		asset.Upstreams = addUpstream(asset.Upstreams, up)

	case TxDependencyManualRemove:
		key := strings.TrimSpace(tx.DependencyKey)
		if key == "" {
			return badRequestError("invalid_transaction", "dependency_key is required")
		}
		meta.DepAdd = removeDependencyKey(meta.DepAdd, key)
		asset.Upstreams = removeUpstreamByKey(asset.Upstreams, key)

	case TxDependencyInferredIgnore:
		key := strings.TrimSpace(tx.DependencyKey)
		if key == "" {
			return badRequestError("invalid_transaction", "dependency_key is required")
		}
		meta.DepDrop = appendDependencyKey(meta.DepDrop, key)
		meta.DepAdd = removeDependencyKey(meta.DepAdd, key)
		asset.Upstreams = removeUpstreamByKey(asset.Upstreams, key)

	case TxDependencyInferredRestore:
		key := strings.TrimSpace(tx.DependencyKey)
		if key == "" {
			return badRequestError("invalid_transaction", "dependency_key is required")
		}
		meta.DepDrop = removeDependencyKey(meta.DepDrop, key)
		asset.Upstreams = addUpstream(asset.Upstreams, assetmeta.ParseDependencyKey(key))

	case TxColumnManualAdd:
		if tx.ColumnDef == nil || strings.TrimSpace(tx.ColumnDef.Name) == "" {
			return badRequestError("invalid_transaction", "column_def with a name is required")
		}
		cols := ModelColumnsToPipelineColumns([]webmodel.Column{*tx.ColumnDef})
		asset.Columns = upsertColumn(asset.Columns, cols[0])
		meta.ColAdd = appendName(meta.ColAdd, tx.ColumnDef.Name)
		meta.ColDrop = removeName(meta.ColDrop, tx.ColumnDef.Name)
		meta.ColSource = removeColumnSource(meta.ColSource, tx.ColumnDef.Name)

	case TxColumnInferredDrop:
		if strings.TrimSpace(tx.Column) == "" {
			return badRequestError("invalid_transaction", "column is required")
		}
		meta.ColDrop = appendName(meta.ColDrop, tx.Column)
		meta.ColAdd = removeName(meta.ColAdd, tx.Column)
		meta.ColSource = removeColumnSource(meta.ColSource, tx.Column)
		asset.Columns = removeColumnByName(asset.Columns, tx.Column)

	case TxColumnInferredRestore:
		if strings.TrimSpace(tx.Column) == "" {
			return badRequestError("invalid_transaction", "column is required")
		}
		meta.ColDrop = removeName(meta.ColDrop, tx.Column)

	case TxColumnFieldOwn:
		if strings.TrimSpace(tx.Column) == "" || strings.TrimSpace(tx.Field) == "" {
			return badRequestError("invalid_transaction", "column and field are required")
		}
		meta.ColOwn = ownField(meta.ColOwn, tx.Column, tx.Field)
		if strings.EqualFold(strings.TrimSpace(tx.Field), "type") {
			meta.ColSource = removeColumnSource(meta.ColSource, tx.Column)
		}

	case TxColumnFieldDisown:
		if strings.TrimSpace(tx.Column) == "" || strings.TrimSpace(tx.Field) == "" {
			return badRequestError("invalid_transaction", "column and field are required")
		}
		meta.ColOwn = disownField(meta.ColOwn, tx.Column, tx.Field)

	case TxColumnDescriptionSet:
		if strings.TrimSpace(tx.Column) == "" {
			return badRequestError("invalid_transaction", "column is required")
		}
		if !setColumnDescription(asset.Columns, tx.Column, tx.Description) {
			return badRequestError("unknown_column", fmt.Sprintf("column %q not found", tx.Column))
		}

	case TxColumnCheckAdd:
		if strings.TrimSpace(tx.Column) == "" || tx.Check == nil {
			return badRequestError("invalid_transaction", "column and check are required")
		}
		if !addColumnCheck(asset.Columns, tx.Column, *tx.Check) {
			return badRequestError("unknown_column", fmt.Sprintf("column %q not found", tx.Column))
		}

	case TxColumnCheckRemove:
		if strings.TrimSpace(tx.Column) == "" || tx.Check == nil || strings.TrimSpace(tx.Check.Name) == "" {
			return badRequestError("invalid_transaction", "column and check name are required")
		}
		if !removeColumnCheck(asset.Columns, tx.Column, tx.Check.Name) {
			return badRequestError("unknown_column", fmt.Sprintf("column %q not found", tx.Column))
		}

	case TxCustomCheckUpsert:
		if tx.CustomCheck == nil {
			return badRequestError("invalid_transaction", "custom_check is required")
		}
		if apiErr := upsertCustomCheck(asset, tx.CustomCheckName, *tx.CustomCheck); apiErr != nil {
			return apiErr
		}

	case TxCustomCheckRemove:
		name := strings.TrimSpace(tx.CustomCheckName)
		if name == "" {
			return badRequestError("invalid_transaction", "custom_check_name is required")
		}
		if !removeCustomCheck(asset, name) {
			return badRequestError("unknown_custom_check", fmt.Sprintf("custom check %q not found", name))
		}

	case TxColumnMergeSettingsClear:
		name := strings.TrimSpace(tx.Column)
		if name == "" {
			return badRequestError("invalid_transaction", "column is required")
		}
		found := false
		for index := range asset.Columns {
			if !strings.EqualFold(strings.TrimSpace(asset.Columns[index].Name), name) {
				continue
			}
			asset.Columns[index].UpdateOnMerge = false
			asset.Columns[index].MergeSQL = ""
			found = true
			break
		}
		if !found {
			return badRequestError("unknown_column", fmt.Sprintf("column %q not found", name))
		}

	case TxMaterializationPartitionByClear:
		asset.Materialization.PartitionBy = ""

	case TxMaterializationClusterByClear:
		asset.Materialization.ClusterBy = nil

	case TxHookUpsert:
		hooks, apiErr := hooksForTransactionPhase(asset, tx.HookPhase)
		if apiErr != nil {
			return apiErr
		}
		query := strings.TrimSpace(tx.HookQuery)
		if query == "" {
			return badRequestError("invalid_transaction", "hook_query is required")
		}
		if tx.HookIndex == nil {
			*hooks = append(*hooks, pipeline.Hook{Query: query})
			break
		}
		if *tx.HookIndex < 0 || *tx.HookIndex >= len(*hooks) {
			return badRequestError("unknown_hook", "hook_index is out of range")
		}
		(*hooks)[*tx.HookIndex].Query = query

	case TxHookRemove:
		hooks, apiErr := hooksForTransactionPhase(asset, tx.HookPhase)
		if apiErr != nil {
			return apiErr
		}
		if tx.HookIndex == nil || *tx.HookIndex < 0 || *tx.HookIndex >= len(*hooks) {
			return badRequestError("unknown_hook", "hook_index is required and must be in range")
		}
		index := *tx.HookIndex
		*hooks = append((*hooks)[:index], (*hooks)[index+1:]...)

	default:
		return badRequestError("unknown_transaction", fmt.Sprintf("unknown transaction type %q", tx.Type))
	}
	return nil
}

func hooksForTransactionPhase(asset *pipeline.Asset, phase string) (*[]pipeline.Hook, *APIError) {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "pre":
		return &asset.Hooks.Pre, nil
	case "post":
		return &asset.Hooks.Post, nil
	default:
		return nil, badRequestError("invalid_transaction", "hook_phase must be pre or post")
	}
}

func upsertCustomCheck(asset *pipeline.Asset, existingName string, check webmodel.CustomCheck) *APIError {
	name := strings.TrimSpace(check.Name)
	query := strings.TrimSpace(check.Query)
	if name == "" || query == "" {
		return badRequestError("invalid_custom_check", "custom check name and query are required")
	}
	existingName = strings.TrimSpace(existingName)
	if existingName == "" {
		existingName = name
	}

	existingIndex := -1
	for index := range asset.CustomChecks {
		if strings.EqualFold(asset.CustomChecks[index].Name, existingName) {
			existingIndex = index
			break
		}
	}
	for index := range asset.CustomChecks {
		if index != existingIndex && strings.EqualFold(asset.CustomChecks[index].Name, name) {
			return badRequestError(
				"duplicate_custom_check",
				fmt.Sprintf("custom check %q already exists", name),
			)
		}
	}

	converted := ModelCustomCheckToPipelineCustomCheck(check)
	if existingIndex >= 0 {
		// Notifications are not part of the guided editor yet. Preserve them (and
		// the parser-assigned runtime ID) when editing the fields Renart exposes.
		converted.ID = asset.CustomChecks[existingIndex].ID
		converted.Notifications = asset.CustomChecks[existingIndex].Notifications
		asset.CustomChecks[existingIndex] = converted
		return nil
	}
	asset.CustomChecks = append(asset.CustomChecks, converted)
	return nil
}

func removeCustomCheck(asset *pipeline.Asset, name string) bool {
	for index := range asset.CustomChecks {
		if !strings.EqualFold(asset.CustomChecks[index].Name, name) {
			continue
		}
		asset.CustomChecks = append(
			asset.CustomChecks[:index],
			asset.CustomChecks[index+1:]...,
		)
		return true
	}
	return false
}

// --- dependency helpers ---

func dependencyFromTransaction(dep *TransactionDependency) pipeline.Upstream {
	mode := pipeline.MarshalUpstreamMode(dep.Mode)
	if strings.TrimSpace(dep.URI) != "" {
		return pipeline.Upstream{Type: "uri", Value: strings.TrimSpace(dep.URI), Mode: mode}
	}
	return pipeline.Upstream{Type: "asset", Value: strings.TrimSpace(dep.Asset), Mode: mode}
}

func appendDependencyKey(keys []string, key string) []string {
	match := assetmeta.DependencyMatchKey(key)
	for _, existing := range keys {
		if assetmeta.DependencyMatchKey(existing) == match {
			return keys
		}
	}
	return append(append([]string(nil), keys...), key)
}

func removeDependencyKey(keys []string, key string) []string {
	match := assetmeta.DependencyMatchKey(key)
	out := make([]string, 0, len(keys))
	for _, existing := range keys {
		if assetmeta.DependencyMatchKey(existing) == match {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func addUpstream(upstreams []pipeline.Upstream, up pipeline.Upstream) []pipeline.Upstream {
	match := assetmeta.DependencyMatchKey(assetmeta.DependencyKey(up))
	for i, existing := range upstreams {
		if assetmeta.DependencyMatchKey(assetmeta.DependencyKey(existing)) == match {
			upstreams[i] = up // refresh mode
			return upstreams
		}
	}
	if up.Type == "" {
		up.Type = "asset"
	}
	return append(upstreams, up)
}

func removeUpstreamByKey(upstreams []pipeline.Upstream, key string) []pipeline.Upstream {
	match := assetmeta.DependencyMatchKey(key)
	out := make([]pipeline.Upstream, 0, len(upstreams))
	for _, existing := range upstreams {
		if assetmeta.DependencyMatchKey(assetmeta.DependencyKey(existing)) == match {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func upstreamNames(upstreams []pipeline.Upstream) []string {
	out := make([]string, 0, len(upstreams))
	for _, up := range upstreams {
		out = append(out, up.Value)
	}
	return out
}

// --- column helpers ---

func upsertColumn(columns []pipeline.Column, col pipeline.Column) []pipeline.Column {
	for i, existing := range columns {
		if strings.EqualFold(existing.Name, col.Name) {
			columns[i] = col
			return columns
		}
	}
	return append(columns, col)
}

func removeColumnByName(columns []pipeline.Column, name string) []pipeline.Column {
	out := make([]pipeline.Column, 0, len(columns))
	for _, existing := range columns {
		if strings.EqualFold(existing.Name, name) {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func setColumnDescription(columns []pipeline.Column, name, description string) bool {
	for i := range columns {
		if strings.EqualFold(columns[i].Name, name) {
			columns[i].Description = description
			return true
		}
	}
	return false
}

func addColumnCheck(columns []pipeline.Column, name string, check webmodel.ColumnCheck) bool {
	converted := ModelColumnsToPipelineColumns([]webmodel.Column{{Name: name, Checks: []webmodel.ColumnCheck{check}}})
	if len(converted) == 0 || len(converted[0].Checks) == 0 {
		return false
	}
	for i := range columns {
		if strings.EqualFold(columns[i].Name, name) {
			columns[i].Checks = append(columns[i].Checks, converted[0].Checks[0])
			return true
		}
	}
	return false
}

// removeColumnCheck drops the first check matching checkName (case-insensitive)
// from the named column. Returns false when the column does not exist.
func removeColumnCheck(columns []pipeline.Column, name, checkName string) bool {
	for i := range columns {
		if !strings.EqualFold(columns[i].Name, name) {
			continue
		}
		for j := range columns[i].Checks {
			if strings.EqualFold(columns[i].Checks[j].Name, checkName) {
				columns[i].Checks = append(columns[i].Checks[:j], columns[i].Checks[j+1:]...)
				break
			}
		}
		return true
	}
	return false
}

func appendName(names []string, name string) []string {
	for _, existing := range names {
		if strings.EqualFold(existing, name) {
			return names
		}
	}
	return append(append([]string(nil), names...), name)
}

func removeName(names []string, name string) []string {
	out := make([]string, 0, len(names))
	for _, existing := range names {
		if strings.EqualFold(existing, name) {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func ownField(own map[string][]string, column, field string) map[string][]string {
	if own == nil {
		own = make(map[string][]string)
	}
	key := strings.ToLower(strings.TrimSpace(column))
	for _, existing := range own[key] {
		if strings.EqualFold(existing, field) {
			return own
		}
	}
	own[key] = append(own[key], strings.TrimSpace(field))
	return own
}

func disownField(own map[string][]string, column, field string) map[string][]string {
	if own == nil {
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(column))
	fields := make([]string, 0, len(own[key]))
	for _, existing := range own[key] {
		if strings.EqualFold(existing, field) {
			continue
		}
		fields = append(fields, existing)
	}
	if len(fields) == 0 {
		delete(own, key)
	} else {
		own[key] = fields
	}
	return own
}

func setColumnSource(sources map[string]string, column, source string) map[string]string {
	key := columnNameKey(column)
	source = strings.TrimSpace(source)
	if key == "" || source == "" {
		return removeColumnSource(sources, column)
	}
	if sources == nil {
		sources = make(map[string]string)
	}
	sources[key] = source
	return sources
}

func removeColumnSource(sources map[string]string, column string) map[string]string {
	if sources == nil {
		return nil
	}
	delete(sources, columnNameKey(column))
	if len(sources) == 0 {
		return nil
	}
	return sources
}
