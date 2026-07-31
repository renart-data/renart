package service

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"

	"renart/internal/web/model"
)

const (
	renartOwnedSeedFileMetaKey = "renart_seed_file"
	maxSeedPreviewBytes        = 256 << 10
)

// seedWorkspacePathParameter is accepted only by semantic seed creation. The
// UI's workspace picker speaks workspace-relative paths; canonical Bruin seed
// definitions store paths relative to the .asset.yml file instead.
const seedWorkspacePathParameter = "workspace_path"

type semanticAssetDefinition struct {
	Name       string                  `yaml:"name"`
	Type       string                  `yaml:"type"`
	Connection string                  `yaml:"connection,omitempty"`
	Meta       map[string]string       `yaml:"meta,omitempty"`
	Parameters semanticAssetParameters `yaml:"parameters"`
}

type semanticAssetParameters struct {
	Path          string `yaml:"path,omitempty"`
	FileType      string `yaml:"file_type,omitempty"`
	EnforceSchema *bool  `yaml:"enforce_schema,omitempty"`
	Query         string `yaml:"query,omitempty"`
	Table         string `yaml:"table,omitempty"`
	BucketName    string `yaml:"bucket_name,omitempty"`
	BucketKey     string `yaml:"bucket_key,omitempty"`
	PokeInterval  *int   `yaml:"poke_interval,omitempty"`
	Timeout       string `yaml:"timeout,omitempty"`
}

type semanticAssetFiles struct {
	definition  []byte
	sidecarPath string
	sidecar     []byte
}

func authoringCapabilityForType(assetType string) (model.AssetAuthoringCapability, bool) {
	for _, capability := range assetAuthoringCapabilities() {
		if capability.Type == strings.TrimSpace(assetType) {
			return capability, true
		}
	}
	return model.AssetAuthoringCapability{}, false
}

func (s *AssetService) buildSemanticAssetFiles(assetName, assetType, absAssetPath string, req CreateAssetParams) (semanticAssetFiles, *APIError) {
	capability, ok := authoringCapabilityForType(assetType)
	if !ok {
		return semanticAssetFiles{}, newAPIError(400, "unsupported_asset_type", fmt.Sprintf("Renart cannot create asset type %q", assetType))
	}
	if connectionErr := s.validateAuthoringConnection(req.Connection, capability); connectionErr != nil {
		return semanticAssetFiles{}, connectionErr
	}

	switch capability.Kind {
	case "seed":
		return s.buildSemanticSeedFiles(assetName, assetType, absAssetPath, req, capability)
	case "sensor":
		return buildSemanticSensorFiles(assetName, assetType, req, capability)
	default:
		return semanticAssetFiles{}, newAPIError(400, "unsupported_asset_kind", fmt.Sprintf("Renart cannot create asset kind %q", capability.Kind))
	}
}

func (s *AssetService) validateSemanticAssetUpdate(asset *pipeline.Asset) *APIError {
	if asset == nil {
		return nil
	}
	assetType := strings.TrimSpace(string(asset.Type))
	capability, ok := authoringCapabilityForType(assetType)
	if !ok {
		return newAPIError(400, "unsupported_asset_type", fmt.Sprintf("Renart cannot author asset type %q", assetType))
	}
	parameters := make(map[string]string, len(asset.Parameters))
	for key := range asset.Parameters {
		value, ok := asset.Parameters.GetString(key)
		if !ok {
			return newAPIError(400, "invalid_asset_parameter", fmt.Sprintf("asset parameter %q must be a scalar value", key))
		}
		parameters[key] = value
	}
	req := CreateAssetParams{Connection: asset.Connection, Parameters: parameters}
	switch capability.Kind {
	case "seed":
		_, apiErr := s.buildSemanticSeedFiles(asset.Name, assetType, asset.DefinitionFile.Path, req, capability)
		return apiErr
	case "sensor":
		_, apiErr := buildSemanticSensorFiles(asset.Name, assetType, req, capability)
		return apiErr
	default:
		return newAPIError(400, "unsupported_asset_kind", fmt.Sprintf("Renart cannot author asset kind %q", capability.Kind))
	}
}

func (s *AssetService) validateAuthoringConnection(connectionName string, capability model.AssetAuthoringCapability) *APIError {
	connectionName = strings.TrimSpace(connectionName)
	if connectionName == "" || s.deps.ConnectionTypeFor == nil {
		return nil
	}
	connectionType := strings.TrimSpace(s.deps.ConnectionTypeFor(connectionName))
	if connectionType == "" {
		return nil
	}
	for _, allowed := range capability.ConnectionTypes {
		if connectionType == allowed {
			return nil
		}
	}
	return newAPIError(400, "incompatible_connection", fmt.Sprintf("connection %q has type %q, which cannot back %s", connectionName, connectionType, capability.Type))
}

func (s *AssetService) buildSemanticSeedFiles(assetName, assetType, absAssetPath string, req CreateAssetParams, capability model.AssetAuthoringCapability) (semanticAssetFiles, *APIError) {
	parameters := req.Parameters
	if parameters == nil {
		parameters = map[string]string{}
	}
	enforceSchema := true
	if raw := strings.TrimSpace(parameters["enforce_schema"]); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return semanticAssetFiles{}, newAPIError(400, "invalid_enforce_schema", "enforce_schema must be true or false")
		}
		enforceSchema = parsed
	}
	fileType := strings.ToLower(strings.TrimSpace(parameters["file_type"]))
	if fileType != "" && !containsString(capability.FileTypes, fileType) {
		return semanticAssetFiles{}, newAPIError(400, "invalid_seed_file_type", fmt.Sprintf("unsupported seed file type %q", fileType))
	}

	uploadBytes := req.SeedFileBytes
	if uploadBytes == nil && strings.TrimSpace(req.SeedFileName) != "" {
		uploadBytes = []byte(req.SeedFileContent)
	}
	seedPath := strings.TrimSpace(parameters["path"])
	workspaceSeedPath := strings.TrimSpace(parameters[seedWorkspacePathParameter])
	if seedPath != "" && workspaceSeedPath != "" {
		return semanticAssetFiles{}, newAPIError(400, "conflicting_seed_paths", "provide either path or workspace_path, not both")
	}
	if workspaceSeedPath != "" {
		var pathErr *APIError
		seedPath, pathErr = s.canonicalSeedWorkspacePath(workspaceSeedPath, absAssetPath)
		if pathErr != nil {
			return semanticAssetFiles{}, pathErr
		}
	}
	ownedFile := ""
	files := semanticAssetFiles{}
	if uploadBytes != nil {
		seedFileName, nameErr := cleanSeedUploadName(req.SeedFileName)
		if nameErr != nil {
			return semanticAssetFiles{}, nameErr
		}
		if seedFileName == filepath.Base(absAssetPath) {
			return semanticAssetFiles{}, newAPIError(400, "invalid_seed_file_name", "seed file cannot replace its asset definition")
		}
		inferredType := seedFileTypeFromPath(seedFileName)
		if fileType == "" {
			fileType = inferredType
		}
		if fileType == "" || !containsString(capability.FileTypes, fileType) {
			return semanticAssetFiles{}, newAPIError(400, "invalid_seed_file_type", "seed uploads need a supported file extension or an explicit file_type")
		}
		if inferredType != "" && inferredType != fileType {
			return semanticAssetFiles{}, newAPIError(400, "seed_file_type_mismatch", fmt.Sprintf("uploaded .%s file does not match file_type %q", inferredType, fileType))
		}
		seedPath = "./" + seedFileName
		ownedFile = seedFileName
		files.sidecarPath = filepath.Join(filepath.Dir(absAssetPath), seedFileName)
		files.sidecar = append([]byte(nil), uploadBytes...)
	} else {
		if seedPath == "" {
			return semanticAssetFiles{}, newAPIError(400, "missing_seed_path", "seed path is required when no file is uploaded")
		}
		if pathErr := s.validateExistingSeedPath(seedPath, absAssetPath); pathErr != nil {
			return semanticAssetFiles{}, pathErr
		}
		if fileType == "" {
			fileType = seedFileTypeFromPath(seedPath)
		}
		if fileType == "" || !containsString(capability.FileTypes, fileType) {
			return semanticAssetFiles{}, newAPIError(400, "invalid_seed_file_type", "seed paths need a supported file extension or an explicit file_type")
		}
	}

	definition := semanticAssetDefinition{
		Name:       assetName,
		Type:       assetType,
		Connection: strings.TrimSpace(req.Connection),
		Parameters: semanticAssetParameters{
			Path:          seedPath,
			FileType:      fileType,
			EnforceSchema: &enforceSchema,
		},
	}
	if ownedFile != "" {
		definition.Meta = map[string]string{renartOwnedSeedFileMetaKey: ownedFile}
	}
	encoded, err := yaml.Marshal(definition)
	if err != nil {
		return semanticAssetFiles{}, newAPIError(500, "asset_render_failed", err.Error())
	}
	files.definition = encoded
	return files, nil
}

func buildSemanticSensorFiles(assetName, assetType string, req CreateAssetParams, capability model.AssetAuthoringCapability) (semanticAssetFiles, *APIError) {
	parameters := req.Parameters
	if parameters == nil {
		parameters = map[string]string{}
	}
	for _, required := range capability.RequiredParameters {
		if strings.TrimSpace(parameters[required]) == "" {
			return semanticAssetFiles{}, newAPIError(400, "missing_sensor_parameter", fmt.Sprintf("sensor parameter %q is required", required))
		}
	}
	pokeInterval := 30
	if raw := strings.TrimSpace(parameters["poke_interval"]); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return semanticAssetFiles{}, newAPIError(400, "invalid_poke_interval", "poke_interval must be a positive number of seconds")
		}
		pokeInterval = parsed
	}
	timeout := strings.TrimSpace(parameters["timeout"])
	if timeout == "" {
		timeout = "24h"
	}
	parsedTimeout, err := time.ParseDuration(timeout)
	if err != nil || parsedTimeout <= 0 {
		return semanticAssetFiles{}, newAPIError(400, "invalid_sensor_timeout", "timeout must be a positive duration such as 30m or 24h")
	}

	definition := semanticAssetDefinition{
		Name:       assetName,
		Type:       assetType,
		Connection: strings.TrimSpace(req.Connection),
		Parameters: semanticAssetParameters{
			Query:        strings.TrimSpace(parameters["query"]),
			Table:        strings.TrimSpace(parameters["table"]),
			BucketName:   strings.TrimSpace(parameters["bucket_name"]),
			BucketKey:    strings.TrimSpace(parameters["bucket_key"]),
			PokeInterval: &pokeInterval,
			Timeout:      timeout,
		},
	}
	encoded, err := yaml.Marshal(definition)
	if err != nil {
		return semanticAssetFiles{}, newAPIError(500, "asset_render_failed", err.Error())
	}
	return semanticAssetFiles{definition: encoded}, nil
}

func cleanSeedUploadName(name string) (string, *APIError) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	base := filepath.Base(filepath.FromSlash(trimmed))
	if trimmed == "" || base == "." || base == string(filepath.Separator) || base == "" {
		return "", newAPIError(400, "invalid_seed_file_name", "seed file name is invalid")
	}
	return base, nil
}

func (s *AssetService) validateExistingSeedPath(seedPath, absAssetPath string) *APIError {
	parsedURL, err := url.Parse(seedPath)
	if err != nil {
		return newAPIError(400, "invalid_seed_path", err.Error())
	}
	if parsedURL.IsAbs() {
		if (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") && parsedURL.Host != "" {
			return nil
		}
		return newAPIError(400, "invalid_seed_path", "seed URLs must use http or https")
	}
	if filepath.IsAbs(filepath.FromSlash(seedPath)) {
		return newAPIError(400, "invalid_seed_path", "local seed paths must be relative to the asset definition")
	}
	joined := filepath.Clean(filepath.Join(filepath.Dir(absAssetPath), filepath.FromSlash(seedPath)))
	relative, err := filepath.Rel(s.deps.WorkspaceRoot, joined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return newAPIError(400, "invalid_seed_path", "seed path must stay inside the workspace")
	}
	exists, err := afero.Exists(s.fs(), joined)
	if err != nil {
		return newAPIError(500, "seed_file_stat_failed", err.Error())
	}
	if !exists {
		return newAPIError(400, "seed_file_not_found", fmt.Sprintf("seed file %q does not exist", seedPath))
	}
	return nil
}

func (s *AssetService) canonicalSeedWorkspacePath(workspacePath, absAssetPath string) (string, *APIError) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(workspacePath, "\\", "/"))
	trimmed = strings.TrimPrefix(trimmed, "./")
	if trimmed == "" {
		return "", newAPIError(400, "invalid_seed_workspace_path", "workspace seed path is required")
	}
	absSeedPath, err := SafeJoin(s.deps.WorkspaceRoot, trimmed)
	if err != nil {
		return "", newAPIError(400, "invalid_seed_workspace_path", "workspace seed path must stay inside the workspace")
	}
	info, err := s.fs().Stat(absSeedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", newAPIError(400, "seed_file_not_found", fmt.Sprintf("workspace seed file %q does not exist", workspacePath))
		}
		return "", newAPIError(500, "seed_file_stat_failed", err.Error())
	}
	if info.IsDir() {
		return "", newAPIError(400, "invalid_seed_workspace_path", "workspace seed path must select a file")
	}
	relative, err := filepath.Rel(filepath.Dir(absAssetPath), absSeedPath)
	if err != nil {
		return "", newAPIError(400, "invalid_seed_workspace_path", err.Error())
	}
	canonical := filepath.ToSlash(relative)
	if !strings.HasPrefix(canonical, ".") {
		canonical = "./" + canonical
	}
	return canonical, nil
}

func seedFileTypeFromPath(value string) string {
	pathValue := value
	if parsed, err := url.Parse(value); err == nil && parsed.Path != "" {
		pathValue = parsed.Path
	}
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(pathValue)), ".")
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// SeedFilePreview reads only modest, local text seeds for the guided editor.
// The asset definition remains the authority for the path; callers cannot
// submit a filesystem path of their own.
func (s *AssetService) SeedFilePreview(ctx context.Context, assetID string) (SeedFilePreviewResponse, *APIError) {
	response := SeedFilePreviewResponse{Status: "ok", AssetID: assetID}
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return SeedFilePreviewResponse{}, badRequestError("invalid_asset_id", "invalid asset id")
	}
	absAssetPath, err := s.resolver().JoinPath(relAssetPath)
	if err != nil {
		return SeedFilePreviewResponse{}, badRequestError("invalid_asset_path", err.Error())
	}

	unlock := s.lockAssetFile(absAssetPath)
	defer unlock()

	_, _, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return SeedFilePreviewResponse{}, badRequestError("asset_resolve_failed", err.Error())
	}
	if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".seed") {
		return SeedFilePreviewResponse{}, badRequestError("unsupported_asset_type", "seed file preview is supported for seed assets only")
	}

	seedPath, _ := asset.Parameters.GetString("path")
	seedPath = strings.TrimSpace(seedPath)
	fileType, _ := asset.Parameters.GetString("file_type")
	fileType = strings.ToLower(strings.TrimSpace(fileType))
	if fileType == "" {
		fileType = seedFileTypeFromPath(seedPath)
	}
	response.FileType = fileType
	if seedPath == "" {
		response.UnavailableReason = "missing_path"
		return response, nil
	}
	if strings.Contains(seedPath, "{{") || strings.Contains(seedPath, "{%") {
		response.UnavailableReason = "runtime_path"
		return response, nil
	}

	parsedURL, parseErr := url.Parse(seedPath)
	if parseErr != nil {
		return SeedFilePreviewResponse{}, badRequestError("invalid_seed_path", parseErr.Error())
	}
	if parsedURL.IsAbs() {
		if (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") && parsedURL.Host != "" {
			response.UnavailableReason = "remote"
			return response, nil
		}
		return SeedFilePreviewResponse{}, badRequestError("invalid_seed_path", "seed URLs must use http or https")
	}
	if filepath.IsAbs(filepath.FromSlash(seedPath)) {
		return SeedFilePreviewResponse{}, badRequestError("invalid_seed_path", "local seed paths must be relative to the asset definition")
	}
	if !isTextSeedFileType(fileType) {
		response.UnavailableReason = "binary_format"
		return response, nil
	}

	seedFilePath := filepath.Clean(filepath.Join(filepath.Dir(absAssetPath), filepath.FromSlash(seedPath)))
	workspaceRoot, absErr := filepath.Abs(s.deps.WorkspaceRoot)
	if absErr != nil {
		return SeedFilePreviewResponse{}, internalError("seed_file_stat_failed", absErr.Error())
	}
	seedFilePath, absErr = filepath.Abs(seedFilePath)
	if absErr != nil || !renderPathIsWithin(workspaceRoot, seedFilePath) {
		return SeedFilePreviewResponse{}, badRequestError("invalid_seed_path", "seed path must stay inside the workspace")
	}
	if _, isOSFS := s.fs().(*afero.OsFs); isOSFS {
		resolvedRoot, resolveErr := filepath.EvalSymlinks(workspaceRoot)
		if resolveErr != nil {
			return SeedFilePreviewResponse{}, internalError("seed_file_stat_failed", resolveErr.Error())
		}
		resolvedSeed, resolveErr := filepath.EvalSymlinks(seedFilePath)
		if resolveErr != nil {
			if os.IsNotExist(resolveErr) {
				return SeedFilePreviewResponse{}, newAPIError(404, "seed_file_not_found", fmt.Sprintf("seed file %q does not exist", seedPath))
			}
			return SeedFilePreviewResponse{}, internalError("seed_file_stat_failed", resolveErr.Error())
		}
		if !renderPathIsWithin(resolvedRoot, resolvedSeed) {
			return SeedFilePreviewResponse{}, badRequestError("invalid_seed_path", "seed path must stay inside the workspace")
		}
	}

	info, statErr := s.fs().Stat(seedFilePath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return SeedFilePreviewResponse{}, newAPIError(404, "seed_file_not_found", fmt.Sprintf("seed file %q does not exist", seedPath))
		}
		return SeedFilePreviewResponse{}, internalError("seed_file_stat_failed", statErr.Error())
	}
	if info.IsDir() {
		return SeedFilePreviewResponse{}, badRequestError("invalid_seed_path", "seed path must select a file")
	}
	response.SizeBytes = info.Size()
	if info.Size() > maxSeedPreviewBytes {
		response.UnavailableReason = "too_large"
		return response, nil
	}

	file, openErr := s.fs().Open(seedFilePath)
	if openErr != nil {
		return SeedFilePreviewResponse{}, internalError("seed_file_read_failed", openErr.Error())
	}
	defer file.Close()
	contents, readErr := io.ReadAll(io.LimitReader(file, maxSeedPreviewBytes+1))
	if readErr != nil {
		return SeedFilePreviewResponse{}, internalError("seed_file_read_failed", readErr.Error())
	}
	response.SizeBytes = int64(len(contents))
	if len(contents) > maxSeedPreviewBytes {
		response.UnavailableReason = "too_large"
		return response, nil
	}
	if !utf8.Valid(contents) {
		response.UnavailableReason = "non_utf8"
		return response, nil
	}
	response.Displayable = true
	response.Content = string(contents)
	return response, nil
}

func isTextSeedFileType(fileType string) bool {
	switch strings.ToLower(strings.TrimSpace(fileType)) {
	case "csv", "json", "jsonl", "ndjson":
		return true
	default:
		return false
	}
}

// ReplaceSeedFile copies a new user upload beside an existing seed definition,
// updates the canonical path/type and ownership marker, and preserves every
// other managed or unknown field in the YAML document.
func (s *AssetService) ReplaceSeedFile(
	ctx context.Context,
	assetID string,
	fileName string,
	fileBytes []byte,
) (AssetMutationResponse, *APIError) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return AssetMutationResponse{}, badRequestError("invalid_asset_id", "invalid asset id")
	}
	absAssetPath, err := s.resolver().JoinPath(relAssetPath)
	if err != nil {
		return AssetMutationResponse{}, badRequestError("invalid_asset_path", err.Error())
	}

	unlock := s.lockAssetFile(absAssetPath)
	defer unlock()

	_, _, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return AssetMutationResponse{}, badRequestError("asset_resolve_failed", err.Error())
	}
	if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".seed") {
		return AssetMutationResponse{}, badRequestError("unsupported_asset_type", "seed file upload is supported for seed assets only")
	}
	capability, ok := authoringCapabilityForType(string(asset.Type))
	if !ok || capability.Kind != "seed" {
		return AssetMutationResponse{}, badRequestError("unsupported_asset_type", fmt.Sprintf("Renart cannot upload files for asset type %q", asset.Type))
	}

	cleanedName, nameErr := cleanSeedUploadName(fileName)
	if nameErr != nil {
		return AssetMutationResponse{}, nameErr
	}
	if cleanedName == filepath.Base(absAssetPath) {
		return AssetMutationResponse{}, badRequestError("invalid_seed_file_name", "seed file cannot replace its asset definition")
	}
	fileType := seedFileTypeFromPath(cleanedName)
	if fileType == "" || !containsString(capability.FileTypes, fileType) {
		return AssetMutationResponse{}, badRequestError("invalid_seed_file_type", fmt.Sprintf("unsupported seed file type %q", fileType))
	}

	fs := s.fs()
	existingDefinition, readErr := afero.ReadFile(fs, absAssetPath)
	if readErr != nil {
		return AssetMutationResponse{}, internalError("asset_read_failed", readErr.Error())
	}
	oldSidecarPath := ownedSeedSidecar(asset, absAssetPath)
	newSidecarPath := filepath.Join(filepath.Dir(absAssetPath), cleanedName)
	previousSidecar, previousSidecarErr := afero.ReadFile(fs, newSidecarPath)
	previousSidecarExists := previousSidecarErr == nil
	if previousSidecarErr != nil && !os.IsNotExist(previousSidecarErr) {
		return AssetMutationResponse{}, internalError("seed_file_read_failed", previousSidecarErr.Error())
	}
	if previousSidecarExists && filepath.Clean(oldSidecarPath) != filepath.Clean(newSidecarPath) {
		return AssetMutationResponse{}, newAPIError(
			409,
			"seed_file_path_exists",
			fmt.Sprintf("seed file %q already exists and is not owned by this asset", cleanedName),
		)
	}

	if asset.Parameters == nil {
		asset.Parameters = pipeline.ParameterMap{}
	}
	asset.Parameters["path"] = "./" + cleanedName
	asset.Parameters["file_type"] = fileType
	if asset.Meta == nil {
		asset.Meta = pipeline.EmptyStringMap{}
	}
	asset.Meta[renartOwnedSeedFileMetaKey] = cleanedName
	mergedDefinition, mergeErr := mergeYAMLAssetDefinition(existingDefinition, asset)
	if mergeErr != nil {
		return AssetMutationResponse{}, internalError("asset_render_failed", mergeErr.Error())
	}

	writeErr := writeNewSemanticAssetFiles(fs, absAssetPath, semanticAssetFiles{
		definition:  mergedDefinition,
		sidecarPath: newSidecarPath,
		sidecar:     append([]byte(nil), fileBytes...),
	})
	if writeErr != nil {
		if previousSidecarExists {
			_ = afero.WriteFile(fs, newSidecarPath, previousSidecar, 0o644)
		} else {
			_ = fs.Remove(newSidecarPath)
		}
		return AssetMutationResponse{}, internalError("seed_file_write_failed", writeErr.Error())
	}
	if oldSidecarPath != "" && oldSidecarPath != newSidecarPath {
		_ = fs.Remove(oldSidecarPath)
	}

	pathsToSuppress := []string{filepath.ToSlash(relAssetPath)}
	for _, sidecarPath := range []string{oldSidecarPath, newSidecarPath} {
		if sidecarPath == "" {
			continue
		}
		if relative, relErr := filepath.Rel(s.deps.WorkspaceRoot, sidecarPath); relErr == nil {
			pathsToSuppress = appendUniqueStrings(pathsToSuppress, filepath.ToSlash(relative))
		}
	}
	for _, path := range pathsToSuppress {
		if s.deps.SuppressWatcher != nil {
			s.deps.SuppressWatcher(path)
		}
	}
	if s.deps.PushWorkspaceUpdateImmediateWithChangedIDs != nil {
		s.deps.PushWorkspaceUpdateImmediateWithChangedIDs(ctx, "asset.updated", filepath.ToSlash(relAssetPath), []string{assetID})
	} else if s.deps.PushWorkspaceUpdateImmediate != nil {
		s.deps.PushWorkspaceUpdateImmediate(ctx, "asset.updated", filepath.ToSlash(relAssetPath))
	}
	return AssetMutationResponse{
		Status:    "ok",
		AssetID:   assetID,
		AssetPath: filepath.ToSlash(relAssetPath),
	}, nil
}

func writeNewSemanticAssetFiles(fs afero.Fs, definitionPath string, files semanticAssetFiles) error {
	paths := []struct {
		path string
		data []byte
	}{{path: definitionPath, data: files.definition}}
	if files.sidecarPath != "" {
		paths = append(paths, struct {
			path string
			data []byte
		}{path: files.sidecarPath, data: files.sidecar})
	}

	temporary := make([]string, 0, len(paths))
	cleanupTemps := func() {
		for _, path := range temporary {
			_ = fs.Remove(path)
		}
	}
	defer cleanupTemps()
	for _, file := range paths {
		tmp, err := afero.TempFile(fs, filepath.Dir(file.path), ".renart-create-*")
		if err != nil {
			return err
		}
		temporary = append(temporary, tmp.Name())
		if _, err := tmp.Write(file.data); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := fs.Chmod(tmp.Name(), 0o644); err != nil {
			return err
		}
	}

	committedSidecar := ""
	if files.sidecarPath != "" {
		if err := fs.Rename(temporary[1], files.sidecarPath); err != nil {
			return err
		}
		committedSidecar = files.sidecarPath
		temporary[1] = ""
	}
	if err := fs.Rename(temporary[0], definitionPath); err != nil {
		if committedSidecar != "" {
			_ = fs.Remove(committedSidecar)
		}
		return err
	}
	temporary[0] = ""
	return nil
}

func ownedSeedSidecar(asset *pipeline.Asset, definitionPath string) string {
	if asset == nil || !strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".seed") {
		return ""
	}
	name := strings.TrimSpace(asset.Meta[renartOwnedSeedFileMetaKey])
	cleaned, apiErr := cleanSeedUploadName(name)
	if apiErr != nil || cleaned != name {
		return ""
	}
	return filepath.Join(filepath.Dir(definitionPath), cleaned)
}
