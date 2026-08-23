package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
)

const (
	assetCreationKindSQL    = "sql"
	assetCreationKindPython = "python"
	assetCreationKindAPI    = "api"
	assetCreationKindLoad   = "load"
	assetCreationKindSeed   = "seed"
	assetCreationKindSensor = "sensor"

	assetCreationRoleTarget      = "target"
	assetCreationRoleSource      = "source"
	assetCreationRoleDestination = "destination"
)

var assetCreationKinds = []string{
	assetCreationKindSQL,
	assetCreationKindPython,
	assetCreationKindAPI,
	assetCreationKindLoad,
	assetCreationKindSeed,
	assetCreationKindSensor,
}

// AssetCreationProfile is the secret-free, environment-specific authoring
// contract consumed by New asset. Concrete Bruin types are candidates derived
// from connection identity; the browser never reconstructs this registry.
// renart:web
type AssetCreationProfile struct {
	Status      string                     `json:"status"`
	Environment string                     `json:"environment"`
	Kinds       []AssetCreationKindProfile `json:"kinds"`
}

type AssetCreationKindProfile struct {
	Kind  string                     `json:"kind"`
	Roles []AssetCreationRoleProfile `json:"roles"`
}

type AssetCreationRoleProfile struct {
	Role                     string                              `json:"role"`
	AllowDefault             bool                                `json:"allow_default"`
	Connections              []AssetCreationConnection           `json:"connections"`
	Default                  AssetCreationDefault                `json:"default"`
	ConnectionTypes          []WorkspaceConfigConnectionType     `json:"connection_types"`
	ConnectionTypeCandidates map[string][]AssetCreationCandidate `json:"connection_type_candidates"`
}

type AssetCreationConnection struct {
	Name                string                            `json:"name"`
	ConnectionType      string                            `json:"connection_type"`
	Category            string                            `json:"category,omitempty"`
	Candidates          []AssetCreationCandidate          `json:"candidates"`
	PortabilityWarnings []AssetCreationPortabilityWarning `json:"portability_warnings,omitempty"`
}

type AssetCreationCandidate struct {
	Variant   string `json:"variant,omitempty"`
	AssetType string `json:"asset_type"`
	Dialect   string `json:"dialect,omitempty"`
	Operator  string `json:"operator"`
}

type AssetCreationDefault struct {
	Status         string                   `json:"status"`
	Reason         string                   `json:"reason,omitempty"`
	Connection     string                   `json:"connection,omitempty"`
	ConnectionType string                   `json:"connection_type,omitempty"`
	Candidates     []AssetCreationCandidate `json:"candidates,omitempty"`
}

type AssetCreationPortabilityWarning struct {
	Environment string `json:"environment"`
	Code        string `json:"code"`
	Message     string `json:"message"`
}

type AssetCreationResolution struct {
	Kind                string
	AssetType           string
	Dialect             string
	EffectiveConnection string
}

func (s *AssetService) AssetCreationProfile(ctx context.Context, pipelineID, requestedEnvironment string) (AssetCreationProfile, *APIError) {
	_, pipelinePath, err := s.resolver().DecodePipelineID(pipelineID)
	if err != nil {
		return AssetCreationProfile{}, newAPIError(400, "invalid_pipeline_id", "invalid pipeline id")
	}
	return s.assetCreationProfileForPath(ctx, pipelinePath, requestedEnvironment)
}

func (s *AssetService) assetCreationProfileForPath(ctx context.Context, pipelinePath, requestedEnvironment string) (AssetCreationProfile, *APIError) {
	environment := strings.TrimSpace(requestedEnvironment)
	if environment == "" && s.deps.SelectedEnvironment != nil {
		environment = strings.TrimSpace(s.deps.SelectedEnvironment())
	}
	cfg, err := loadSelectedConfigReadOnlyFS(s.fs(), s.deps.ConfigPath, environment)
	if err != nil {
		return AssetCreationProfile{}, newAPIError(400, "invalid_environment", err.Error())
	}
	if environment = strings.TrimSpace(cfg.SelectedEnvironmentName); environment == "" {
		environment = strings.TrimSpace(cfg.DefaultEnvironmentName)
	}

	parsedPipeline, err := NewRenartPipelineBuilder(s.fs()).CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	if err != nil {
		return AssetCreationProfile{}, newAPIError(400, "pipeline_parse_failed", err.Error())
	}

	connectionTypes := BuildWorkspaceConfigConnectionTypes()
	connections := selectedConnectionSummaries(cfg)
	profile := AssetCreationProfile{
		Status:      "ok",
		Environment: environment,
		Kinds:       make([]AssetCreationKindProfile, 0, len(assetCreationKinds)),
	}
	for _, kind := range assetCreationKinds {
		kindProfile := AssetCreationKindProfile{Kind: kind}
		for _, role := range assetCreationRoles(kind) {
			compatibleConnectionTypes := filterAssetCreationConnectionTypes(connectionTypes, kind, role)
			roleProfile := AssetCreationRoleProfile{
				Role:                     role,
				AllowDefault:             roleAllowsPipelineDefault(kind, role),
				Connections:              buildAssetCreationConnections(cfg, environment, kind, role, connections),
				ConnectionTypes:          compatibleConnectionTypes,
				ConnectionTypeCandidates: assetCreationConnectionTypeCandidates(compatibleConnectionTypes, kind, role),
			}
			roleProfile.Default = resolveAssetCreationDefault(parsedPipeline, connections, roleProfile)
			kindProfile.Roles = append(kindProfile.Roles, roleProfile)
		}
		profile.Kinds = append(profile.Kinds, kindProfile)
	}
	return profile, nil
}

func assetCreationConnectionTypeCandidates(connectionTypes []WorkspaceConfigConnectionType, kind, role string) map[string][]AssetCreationCandidate {
	result := make(map[string][]AssetCreationCandidate, len(connectionTypes))
	for _, connectionType := range connectionTypes {
		name := normalizeConnectionType(connectionType.TypeName)
		if name == "" {
			continue
		}
		result[name] = assetCreationCandidates(kind, role, name)
	}
	return result
}

func assetCreationRoles(kind string) []string {
	if kind == assetCreationKindLoad {
		return []string{assetCreationRoleSource, assetCreationRoleDestination}
	}
	return []string{assetCreationRoleTarget}
}

func roleAllowsPipelineDefault(kind, role string) bool {
	return role != assetCreationRoleSource && (kind != assetCreationKindLoad || role == assetCreationRoleDestination)
}

func selectedConnectionSummaries(cfg *config.Config) map[string]string {
	if cfg == nil || cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return map[string]string{}
	}
	return cfg.SelectedEnvironment.Connections.ConnectionsSummaryList()
}

func buildAssetCreationConnections(cfg *config.Config, selectedEnvironment, kind, role string, summaries map[string]string) []AssetCreationConnection {
	names := make([]string, 0, len(summaries))
	for name := range summaries {
		names = append(names, name)
	}
	sort.Strings(names)

	connections := make([]AssetCreationConnection, 0, len(names)+1)
	for _, name := range names {
		connectionType := normalizeConnectionType(summaries[name])
		candidates := assetCreationCandidates(kind, role, connectionType)
		if len(candidates) == 0 {
			continue
		}
		connections = append(connections, AssetCreationConnection{
			Name:                name,
			ConnectionType:      connectionType,
			Category:            loadConnectionCategory(connectionType),
			Candidates:          candidates,
			PortabilityWarnings: assetCreationPortabilityWarnings(cfg, selectedEnvironment, name, connectionType),
		})
	}
	if kind == assetCreationKindLoad {
		connections = append(connections, AssetCreationConnection{
			Name:           localLoadConnectionName,
			ConnectionType: localLoadConnectionName,
			Category:       LoadCategoryFile,
			Candidates:     assetCreationCandidates(kind, role, localLoadConnectionName),
		})
	}
	return connections
}

const localLoadConnectionName = "local"

func assetCreationCandidates(kind, role, connectionType string) []AssetCreationCandidate {
	connectionType = normalizeConnectionType(connectionType)
	switch kind {
	case assetCreationKindSQL:
		assetType, dialect, ok := supportedSQLAssetTypeForConnectionType(connectionType)
		if !ok {
			return nil
		}
		return []AssetCreationCandidate{{AssetType: string(assetType), Dialect: dialect, Operator: "SQL"}}
	case assetCreationKindPython:
		if loadConnectionCategory(connectionType) != LoadCategoryDatabase {
			return nil
		}
		return []AssetCreationCandidate{{AssetType: string(pipeline.AssetTypePython), Operator: "Python to relation via Sling"}}
	case assetCreationKindAPI:
		if loadConnectionCategory(connectionType) != LoadCategoryDatabase {
			return nil
		}
		return []AssetCreationCandidate{{AssetType: apiAssetType, Operator: "HTTP API to relation via Sling"}}
	case assetCreationKindLoad:
		if connectionType != localLoadConnectionName && loadConnectionCategory(connectionType) == "" {
			return nil
		}
		return []AssetCreationCandidate{{AssetType: loadAssetType, Operator: "Sling replication"}}
	case assetCreationKindSeed, assetCreationKindSensor:
		return semanticAssetCreationCandidates(kind, connectionType)
	default:
		return nil
	}
}

func supportedSQLAssetTypeForConnectionType(connectionType string) (pipeline.AssetType, string, bool) {
	assetType, ok := queryAssetTypeForConnectionType(connectionType)
	if !ok || !isDirectRunAssetTypeSupported(assetType) {
		return "", "", false
	}
	dialect, err := AssetTypeToDialect(assetType)
	if err != nil {
		return "", "", false
	}
	return assetType, dialect, true
}

func semanticAssetCreationCandidates(kind, connectionType string) []AssetCreationCandidate {
	result := make([]AssetCreationCandidate, 0, 2)
	for _, capability := range assetAuthoringCapabilities() {
		if capability.Kind != kind || !containsNormalizedString(capability.ConnectionTypes, connectionType) {
			continue
		}
		assetType := pipeline.AssetType(capability.Type)
		dialect := ""
		if kind == assetCreationKindSensor && capability.Variant == "query" {
			var err error
			dialect, err = AssetTypeToDialect(assetType)
			if err != nil {
				continue
			}
		}
		result = append(result, AssetCreationCandidate{
			Variant:   capability.Variant,
			AssetType: capability.Type,
			Dialect:   dialect,
			Operator:  semanticAssetOperator(kind, capability.Variant),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Variant == result[j].Variant {
			return result[i].AssetType < result[j].AssetType
		}
		return result[i].Variant < result[j].Variant
	})
	return result
}

func semanticAssetOperator(kind, variant string) string {
	if kind == assetCreationKindSeed {
		return "Seed file via Sling"
	}
	switch variant {
	case "query":
		return "Query sensor"
	case "table":
		return "Table sensor"
	case "key":
		return "Object key sensor"
	default:
		return "Sensor"
	}
}

func containsNormalizedString(values []string, candidate string) bool {
	candidate = normalizeConnectionType(candidate)
	for _, value := range values {
		if normalizeConnectionType(value) == candidate {
			return true
		}
	}
	return false
}

func filterAssetCreationConnectionTypes(all []WorkspaceConfigConnectionType, kind, role string) []WorkspaceConfigConnectionType {
	result := make([]WorkspaceConfigConnectionType, 0, len(all))
	for _, connectionType := range all {
		if len(assetCreationCandidates(kind, role, connectionType.TypeName)) > 0 {
			result = append(result, connectionType)
		}
	}
	return result
}

func assetCreationPortabilityWarnings(cfg *config.Config, selectedEnvironment, connectionName, selectedType string) []AssetCreationPortabilityWarning {
	if cfg == nil || len(cfg.Environments) < 2 {
		return nil
	}
	environments := cfg.GetEnvironmentNames()
	sort.Strings(environments)
	warnings := make([]AssetCreationPortabilityWarning, 0)
	for _, environmentName := range environments {
		if environmentName == selectedEnvironment {
			continue
		}
		environment := cfg.Environments[environmentName]
		if environment.Connections == nil {
			warnings = append(warnings, missingConnectionPortabilityWarning(environmentName, connectionName))
			continue
		}
		otherType := normalizeConnectionType(environment.Connections.ConnectionsSummaryList()[connectionName])
		switch {
		case otherType == "":
			warnings = append(warnings, missingConnectionPortabilityWarning(environmentName, connectionName))
		case otherType != normalizeConnectionType(selectedType):
			warnings = append(warnings, AssetCreationPortabilityWarning{
				Environment: environmentName,
				Code:        "type_mismatch",
				Message:     fmt.Sprintf("%s is %s in %s, not %s", connectionName, otherType, environmentName, selectedType),
			})
		}
	}
	return warnings
}

func missingConnectionPortabilityWarning(environmentName, connectionName string) AssetCreationPortabilityWarning {
	return AssetCreationPortabilityWarning{
		Environment: environmentName,
		Code:        "missing",
		Message:     fmt.Sprintf("%s is not configured in %s", connectionName, environmentName),
	}
}

func resolveAssetCreationDefault(pl *pipeline.Pipeline, summaries map[string]string, role AssetCreationRoleProfile) AssetCreationDefault {
	if !role.AllowDefault {
		return AssetCreationDefault{Status: "not_applicable"}
	}
	name, status, reason := pipelineCreationDefaultName(pl, role.Connections)
	if status != "resolved" {
		return AssetCreationDefault{Status: status, Reason: reason}
	}
	for _, connection := range role.Connections {
		if connection.Name == name {
			return AssetCreationDefault{
				Status:         "resolved",
				Connection:     connection.Name,
				ConnectionType: connection.ConnectionType,
				Candidates:     append([]AssetCreationCandidate(nil), connection.Candidates...),
			}
		}
	}
	connectionType := normalizeConnectionType(summaries[name])
	if connectionType == "" {
		return AssetCreationDefault{
			Status:     "missing",
			Connection: name,
			Reason:     fmt.Sprintf("Pipeline default %s is not configured in this environment", name),
		}
	}
	return AssetCreationDefault{
		Status:         "incompatible",
		Connection:     name,
		ConnectionType: connectionType,
		Reason:         fmt.Sprintf("Pipeline default %s (%s) cannot serve this asset role", name, connectionType),
	}
}

func pipelineCreationDefaultName(pl *pipeline.Pipeline, compatible []AssetCreationConnection) (string, string, string) {
	if pl == nil {
		return "", "missing", "The pipeline could not be loaded"
	}
	if pipelineHasSQLMajorityCandidate(pl) {
		if name, err := defaultPipelineTargetConnection(pl); err == nil && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name), "resolved", ""
		}
	}

	compatibleNames := make(map[string]struct{}, len(compatible))
	for _, connection := range compatible {
		compatibleNames[connection.Name] = struct{}{}
	}
	defaultNames := make(map[string]struct{}, len(pl.DefaultConnections))
	for _, name := range pl.DefaultConnections {
		if name = strings.TrimSpace(name); name != "" {
			defaultNames[name] = struct{}{}
		}
	}
	matching := make([]string, 0, len(defaultNames))
	for name := range defaultNames {
		if _, ok := compatibleNames[name]; ok {
			matching = append(matching, name)
		}
	}
	sort.Strings(matching)
	if len(matching) == 1 {
		return matching[0], "resolved", ""
	}
	if len(matching) > 1 {
		return "", "ambiguous", fmt.Sprintf("Multiple compatible pipeline defaults are configured: %s", strings.Join(matching, ", "))
	}
	if len(defaultNames) == 1 {
		for name := range defaultNames {
			return name, "resolved", ""
		}
	}
	if len(defaultNames) > 1 {
		names := make([]string, 0, len(defaultNames))
		for name := range defaultNames {
			names = append(names, name)
		}
		sort.Strings(names)
		return "", "ambiguous", fmt.Sprintf("No single compatible pipeline default can be chosen from: %s", strings.Join(names, ", "))
	}

	// Bruin's conventional zero-configuration default remains valid when the
	// connection actually exists in this environment.
	return "duckdb-default", "resolved", ""
}

func (s *AssetService) resolveAssetCreation(ctx context.Context, pipelinePath string, req CreateAssetParams) (AssetCreationResolution, *APIError) {
	kind := normalizeAssetCreationKind(req.Kind)
	if !containsString(assetCreationKinds, kind) {
		return AssetCreationResolution{}, newAPIError(400, "unsupported_asset_kind", fmt.Sprintf("Renart cannot create asset kind %q", req.Kind))
	}
	requestedConnection := strings.TrimSpace(req.Connection)
	if requestedConnection == "" && !req.UsePipelineDefault {
		return AssetCreationResolution{}, newAPIError(400, "missing_connection_choice", "Choose a compatible connection or explicitly use the pipeline default")
	}
	if requestedConnection != "" && req.UsePipelineDefault {
		return AssetCreationResolution{}, newAPIError(400, "conflicting_connection_choice", "Choose either an explicit connection or the pipeline default")
	}
	profile, apiErr := s.assetCreationProfileForPath(ctx, pipelinePath, req.Environment)
	if apiErr != nil {
		return AssetCreationResolution{}, apiErr
	}
	kindProfile, ok := findAssetCreationKindProfile(profile, kind)
	if !ok {
		return AssetCreationResolution{}, newAPIError(400, "unsupported_asset_kind", fmt.Sprintf("Renart cannot create asset kind %q", kind))
	}
	targetRoleName := assetCreationRoleTarget
	if kind == assetCreationKindLoad {
		targetRoleName = assetCreationRoleDestination
	}
	targetRole, _ := findAssetCreationRoleProfile(kindProfile, targetRoleName)
	targetConnection, apiErr := selectAssetCreationConnection(targetRole, req.Connection)
	if apiErr != nil {
		return AssetCreationResolution{}, apiErr
	}
	candidate, apiErr := selectAssetCreationCandidate(targetConnection.Candidates, req.Variant)
	if apiErr != nil {
		return AssetCreationResolution{}, apiErr
	}

	if kind == assetCreationKindLoad {
		sourceConnection := strings.TrimSpace(req.Parameters[loadParamSourceConnection])
		if sourceConnection != "" {
			sourceRole, _ := findAssetCreationRoleProfile(kindProfile, assetCreationRoleSource)
			if _, sourceErr := selectExplicitAssetCreationConnection(sourceRole, sourceConnection); sourceErr != nil {
				return AssetCreationResolution{}, newAPIError(400, "incompatible_load_source_connection", sourceErr.Message)
			}
		}
	}

	return AssetCreationResolution{
		Kind:                kind,
		AssetType:           candidate.AssetType,
		Dialect:             candidate.Dialect,
		EffectiveConnection: targetConnection.Name,
	}, nil
}

func (s *AssetService) resolveAssetConnectionSelection(ctx context.Context, pipelinePath string, asset *pipeline.Asset, req AssetConnectionSelectionRequest) (AssetCreationResolution, *APIError) {
	if asset == nil {
		return AssetCreationResolution{}, newAPIError(400, "missing_asset", "asset context is required")
	}
	currentType := strings.TrimSpace(string(asset.Type))
	if expected := strings.TrimSpace(req.ExpectedAssetType); expected != "" && expected != currentType {
		return AssetCreationResolution{}, newAPIError(409, "asset_type_changed", fmt.Sprintf("asset type changed from %q to %q; reload before changing its connection", expected, currentType))
	}
	requestedConnection := strings.TrimSpace(req.Connection)
	if requestedConnection == "" && !req.UsePipelineDefault {
		return AssetCreationResolution{}, newAPIError(400, "missing_connection_choice", "Choose a compatible connection or explicitly use the pipeline default")
	}
	if requestedConnection != "" && req.UsePipelineDefault {
		return AssetCreationResolution{}, newAPIError(400, "conflicting_connection_choice", "Choose either an explicit connection or the pipeline default")
	}

	kind := legacyAssetCreationKind(currentType)
	if kind == "" {
		return AssetCreationResolution{}, newAPIError(400, "unsupported_asset_type", fmt.Sprintf("connection editing is not supported for asset type %q", currentType))
	}
	profile, apiErr := s.assetCreationProfileForPath(ctx, pipelinePath, req.Environment)
	if apiErr != nil {
		return AssetCreationResolution{}, apiErr
	}
	kindProfile, ok := findAssetCreationKindProfile(profile, kind)
	if !ok {
		return AssetCreationResolution{}, newAPIError(400, "unsupported_asset_type", fmt.Sprintf("connection editing is not supported for asset type %q", currentType))
	}
	roleName := assetCreationRoleTarget
	if kind == assetCreationKindLoad {
		roleName = assetCreationRoleDestination
	}
	role, ok := findAssetCreationRoleProfile(kindProfile, roleName)
	if !ok {
		return AssetCreationResolution{}, newAPIError(400, "unsupported_asset_type", fmt.Sprintf("connection editing is not supported for asset type %q", currentType))
	}
	connection, apiErr := selectAssetCreationConnection(role, requestedConnection)
	if apiErr != nil {
		return AssetCreationResolution{}, apiErr
	}
	candidate, apiErr := selectAssetConnectionCandidateForExistingType(currentType, connection.Candidates)
	if apiErr != nil {
		return AssetCreationResolution{}, apiErr
	}
	if candidate.AssetType != currentType && !req.ConfirmTypeMigration {
		return AssetCreationResolution{}, newAPIError(
			409,
			"asset_type_migration_required",
			fmt.Sprintf("changing to connection %q requires migrating asset type from %q to %q", connection.Name, currentType, candidate.AssetType),
		)
	}
	return AssetCreationResolution{
		Kind:                kind,
		AssetType:           candidate.AssetType,
		Dialect:             candidate.Dialect,
		EffectiveConnection: connection.Name,
	}, nil
}

func selectAssetConnectionCandidateForExistingType(currentType string, candidates []AssetCreationCandidate) (AssetCreationCandidate, *APIError) {
	currentType = strings.TrimSpace(currentType)
	variant := existingAssetCreationVariant(currentType)
	if variant != "" {
		for _, candidate := range candidates {
			if candidate.Variant == variant {
				return candidate, nil
			}
		}
		return AssetCreationCandidate{}, newAPIError(400, "incompatible_connection", fmt.Sprintf("the selected connection does not support the current %s asset variant", variant))
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	for _, candidate := range candidates {
		if candidate.AssetType == currentType {
			return candidate, nil
		}
	}
	return AssetCreationCandidate{}, newAPIError(400, "incompatible_connection", "the selected connection does not support this asset")
}

func existingAssetCreationVariant(assetType string) string {
	if capability, ok := authoringCapabilityForType(assetType); ok {
		return capability.Variant
	}
	lowered := strings.ToLower(strings.TrimSpace(assetType))
	switch {
	case strings.HasSuffix(lowered, ".seed"):
		return "file"
	case strings.HasSuffix(lowered, ".sensor.query"):
		return "query"
	case strings.HasSuffix(lowered, ".sensor.table"):
		return "table"
	case strings.Contains(lowered, ".sensor.key"):
		return "key"
	default:
		return ""
	}
}

func (s *AssetService) validateDirectAssetConnectionUpdate(ctx context.Context, pipelinePath string, asset *pipeline.Asset, nextConnection string) *APIError {
	if asset == nil || strings.TrimSpace(s.deps.ConfigPath) == "" || strings.TrimSpace(nextConnection) == strings.TrimSpace(asset.Connection) {
		return nil
	}
	_, apiErr := s.resolveAssetConnectionSelection(ctx, pipelinePath, asset, AssetConnectionSelectionRequest{
		Connection:           strings.TrimSpace(nextConnection),
		UsePipelineDefault:   strings.TrimSpace(nextConnection) == "",
		ExpectedAssetType:    strings.TrimSpace(string(asset.Type)),
		ConfirmTypeMigration: false,
	})
	if apiErr != nil && apiErr.Code == "asset_type_migration_required" {
		return newAPIError(409, "asset_type_connection_mismatch", apiErr.Message+"; use the reviewed connection migration")
	}
	return apiErr
}

func normalizeAssetCreationKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "http", "http_api":
		return assetCreationKindAPI
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func findAssetCreationKindProfile(profile AssetCreationProfile, kind string) (AssetCreationKindProfile, bool) {
	for _, candidate := range profile.Kinds {
		if candidate.Kind == kind {
			return candidate, true
		}
	}
	return AssetCreationKindProfile{}, false
}

func findAssetCreationRoleProfile(profile AssetCreationKindProfile, role string) (AssetCreationRoleProfile, bool) {
	for _, candidate := range profile.Roles {
		if candidate.Role == role {
			return candidate, true
		}
	}
	return AssetCreationRoleProfile{}, false
}

func selectAssetCreationConnection(role AssetCreationRoleProfile, requested string) (AssetCreationConnection, *APIError) {
	if requested = strings.TrimSpace(requested); requested != "" {
		return selectExplicitAssetCreationConnection(role, requested)
	}
	if role.Default.Status != "resolved" {
		message := role.Default.Reason
		if message == "" {
			message = "Choose a compatible connection"
		}
		return AssetCreationConnection{}, newAPIError(400, "unresolved_pipeline_default", message)
	}
	for _, connection := range role.Connections {
		if connection.Name == role.Default.Connection {
			return connection, nil
		}
	}
	return AssetCreationConnection{}, newAPIError(400, "unresolved_pipeline_default", "The pipeline default is no longer available")
}

func selectExplicitAssetCreationConnection(role AssetCreationRoleProfile, requested string) (AssetCreationConnection, *APIError) {
	for _, connection := range role.Connections {
		if connection.Name == strings.TrimSpace(requested) {
			return connection, nil
		}
	}
	return AssetCreationConnection{}, newAPIError(400, "incompatible_connection", fmt.Sprintf("connection %q is not compatible with this asset role", requested))
}

func selectAssetCreationCandidate(candidates []AssetCreationCandidate, variant string) (AssetCreationCandidate, *APIError) {
	variant = strings.ToLower(strings.TrimSpace(variant))
	if variant != "" {
		for _, candidate := range candidates {
			if candidate.Variant == variant {
				return candidate, nil
			}
		}
		return AssetCreationCandidate{}, newAPIError(400, "unsupported_asset_variant", fmt.Sprintf("variant %q is not available for this connection", variant))
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return AssetCreationCandidate{}, newAPIError(400, "missing_asset_variant", "Choose a sensor condition type")
}

func (s *AssetService) validateLegacyAssetCreation(ctx context.Context, pipelinePath string, req CreateAssetParams) *APIError {
	if strings.TrimSpace(s.deps.ConfigPath) == "" || strings.TrimSpace(req.Connection) == "" {
		return nil
	}
	kind := legacyAssetCreationKind(req.Type)
	if kind == "" {
		return nil
	}
	profile, apiErr := s.assetCreationProfileForPath(ctx, pipelinePath, req.Environment)
	if apiErr != nil {
		return apiErr
	}
	kindProfile, _ := findAssetCreationKindProfile(profile, kind)
	roleName := assetCreationRoleTarget
	if kind == assetCreationKindLoad {
		roleName = assetCreationRoleDestination
	}
	role, _ := findAssetCreationRoleProfile(kindProfile, roleName)
	connection, connectionErr := selectExplicitAssetCreationConnection(role, req.Connection)
	if connectionErr != nil {
		return connectionErr
	}
	for _, candidate := range connection.Candidates {
		if candidate.AssetType == strings.TrimSpace(req.Type) {
			return nil
		}
	}
	return newAPIError(400, "asset_type_connection_mismatch", fmt.Sprintf("asset type %q does not match connection %q (%s)", req.Type, req.Connection, connection.ConnectionType))
}

func legacyAssetCreationKind(assetType string) string {
	assetType = strings.TrimSpace(assetType)
	switch {
	case isQueryAssetType(pipeline.AssetType(assetType)):
		return assetCreationKindSQL
	case assetType == string(pipeline.AssetTypePython):
		return assetCreationKindPython
	case isAPIAssetType(assetType):
		return assetCreationKindAPI
	case isLoadAssetType(assetType):
		return assetCreationKindLoad
	case strings.HasSuffix(strings.ToLower(assetType), ".seed"):
		return assetCreationKindSeed
	case isSensorAssetType(pipeline.AssetType(assetType)):
		return assetCreationKindSensor
	default:
		return ""
	}
}
