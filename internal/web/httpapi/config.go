package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/policy"
	"renart/internal/web/secretstore"
	"renart/internal/web/service"
)

const maxVaultRequestBytes = 4 << 10

type ConfigChangePublisher interface {
	ConfigChanged(ctx context.Context, relPath, eventType string)
}

type ConfigHandlers struct {
	Service   *service.ConfigService
	Policies  *policy.Loader
	Publisher ConfigChangePublisher
}

type CreateWorkspaceEnvironmentRequest struct {
	Name         string `json:"name"`
	SchemaPrefix string `json:"schema_prefix"`
	SetAsDefault bool   `json:"set_as_default"`
}

type UpdateWorkspaceEnvironmentRequest struct {
	Name         string `json:"name"`
	NewName      string `json:"new_name"`
	SchemaPrefix string `json:"schema_prefix"`
	SetAsDefault bool   `json:"set_as_default"`
}

type CloneWorkspaceEnvironmentRequest struct {
	SourceName   string `json:"source_name"`
	TargetName   string `json:"target_name"`
	SchemaPrefix string `json:"schema_prefix"`
	SetAsDefault bool   `json:"set_as_default"`
}

type DeleteWorkspaceEnvironmentRequest struct {
	Name string `json:"name"`
}

type UpsertWorkspaceConnectionRequest struct {
	EnvironmentName string                                             `json:"environment_name"`
	CurrentName     string                                             `json:"current_name,omitempty"`
	Name            string                                             `json:"name"`
	Type            string                                             `json:"type"`
	Values          map[string]any                                     `json:"values"`
	SecretChanges   map[string]service.WorkspaceConnectionSecretChange `json:"secret_changes,omitempty"`
}

type DeleteWorkspaceConnectionRequest struct {
	EnvironmentName string `json:"environment_name"`
	Name            string `json:"name"`
}

type UpdateWorkspaceProjectRequest struct {
	Name      string                              `json:"name"`
	Features  map[string]bool                     `json:"features,omitempty"`
	Retention *service.WorkspaceRetentionSettings `json:"retention,omitempty"`
}

type TestWorkspaceConnectionRequest struct {
	EnvironmentName string                                             `json:"environment_name"`
	CurrentName     string                                             `json:"current_name,omitempty"`
	Name            string                                             `json:"name"`
	Type            string                                             `json:"type,omitempty"`
	Values          map[string]any                                     `json:"values,omitempty"`
	SecretChanges   map[string]service.WorkspaceConnectionSecretChange `json:"secret_changes,omitempty"`
}

type VaultPassphraseRequest struct {
	Passphrase string `json:"passphrase"`
}

func RegisterConfigRoutes(router chi.Router, handlers *ConfigHandlers) {
	router.Get("/api/config", handlers.HandleGetWorkspaceConfig)
	router.Put("/api/config/project", handlers.HandleUpdateWorkspaceProject)
	router.Get("/api/config/environment-policies/{environment}", handlers.HandleGetEnvironmentPolicy)
	router.Put("/api/config/environment-policies/{environment}", handlers.HandleUpdateEnvironmentPolicy)
	router.Post("/api/config/environments", handlers.HandleCreateWorkspaceEnvironment)
	router.Put("/api/config/environments", handlers.HandleUpdateWorkspaceEnvironment)
	router.Post("/api/config/environments/clone", handlers.HandleCloneWorkspaceEnvironment)
	router.Delete("/api/config/environments", handlers.HandleDeleteWorkspaceEnvironment)
	router.Post("/api/config/connections", handlers.HandleCreateWorkspaceConnection)
	router.Put("/api/config/connections", handlers.HandleUpdateWorkspaceConnection)
	router.Delete("/api/config/connections", handlers.HandleDeleteWorkspaceConnection)
	router.Post("/api/config/connections/test", handlers.HandleTestWorkspaceConnection)
	router.Post("/api/config/secrets/vault/initialize", handlers.HandleInitializeLocalVault)
	router.Post("/api/config/secrets/vault/unlock", handlers.HandleUnlockLocalVault)
	router.Post("/api/config/secrets/vault/lock", handlers.HandleLockLocalVault)
	router.Post("/api/config/secrets/vault/change-passphrase", handlers.HandleChangeLocalVaultPassphrase)
}

func (h *ConfigHandlers) HandleGetWorkspaceConfig(w http.ResponseWriter, _ *http.Request) {
	cfg, configPath, err := h.Service.LoadForEditing()
	if err != nil {
		webapi.WriteJSON(w, http.StatusOK, h.Service.BuildParseErrorResponse(err))
		return
	}

	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(configPath, cfg))
}

func (h *ConfigHandlers) HandleInitializeLocalVault(w http.ResponseWriter, r *http.Request) {
	passphrase, ok := decodeVaultPassphrase(w, r)
	if !ok {
		return
	}
	defer clearStringBytes(passphrase)
	if err := h.Service.InitializeLocalVault(r.Context(), passphrase); err != nil {
		writeVaultError(w, err)
		return
	}
	h.writeWorkspaceConfig(w)
}

func (h *ConfigHandlers) HandleUnlockLocalVault(w http.ResponseWriter, r *http.Request) {
	passphrase, ok := decodeVaultPassphrase(w, r)
	if !ok {
		return
	}
	defer clearStringBytes(passphrase)
	if err := h.Service.UnlockLocalVault(r.Context(), passphrase); err != nil {
		writeVaultError(w, err)
		return
	}
	h.writeWorkspaceConfig(w)
}

func (h *ConfigHandlers) HandleLockLocalVault(w http.ResponseWriter, _ *http.Request) {
	h.Service.LockLocalVault()
	h.writeWorkspaceConfig(w)
}

func (h *ConfigHandlers) HandleChangeLocalVaultPassphrase(w http.ResponseWriter, r *http.Request) {
	passphrase, ok := decodeVaultPassphrase(w, r)
	if !ok {
		return
	}
	defer clearStringBytes(passphrase)
	if err := h.Service.ChangeLocalVaultPassphrase(r.Context(), passphrase); err != nil {
		writeVaultError(w, err)
		return
	}
	h.writeWorkspaceConfig(w)
}

func (h *ConfigHandlers) writeWorkspaceConfig(w http.ResponseWriter) {
	cfg, configPath, err := h.Service.LoadForEditing()
	if err != nil {
		webapi.WriteJSON(w, http.StatusOK, h.Service.BuildParseErrorResponse(err))
		return
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(configPath, cfg))
}

func decodeVaultPassphrase(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	request, err := decodeJSONObject[VaultPassphraseRequest](w, r, maxVaultRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", "Enter a valid vault passphrase.")
		return nil, false
	}
	passphrase := []byte(request.Passphrase)
	request.Passphrase = ""
	if len(passphrase) == 0 {
		webapi.WriteBadRequest(w, "missing_passphrase", "Vault passphrase is required.")
		return nil, false
	}
	return passphrase, true
}

func writeVaultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, secretstore.ErrVaultInvalidPassphrase):
		webapi.WriteError(w, http.StatusUnauthorized, "invalid_vault_passphrase", "The vault passphrase is incorrect.")
	case errors.Is(err, secretstore.ErrVaultAlreadyInitialized):
		webapi.WriteConflict(w, "vault_already_initialized", "The encrypted vault is already initialized.")
	case errors.Is(err, secretstore.ErrVaultNotInitialized):
		webapi.WriteConflict(w, "vault_not_initialized", "Set up the encrypted vault before unlocking it.")
	case errors.Is(err, secretstore.ErrPermissionRequired):
		webapi.WriteError(w, http.StatusLocked, "vault_locked", "Unlock the encrypted vault first.")
	case errors.Is(err, secretstore.ErrUnavailable):
		webapi.WriteError(w, http.StatusServiceUnavailable, "vault_unavailable", "The encrypted vault is unavailable.")
	default:
		webapi.WriteBadRequest(w, "vault_update_failed", err.Error())
	}
}

func clearStringBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func (h *ConfigHandlers) HandleUpdateWorkspaceProject(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[UpdateWorkspaceProjectRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" && req.Features == nil && req.Retention == nil {
		webapi.WriteBadRequest(w, "missing_project_settings", "project name, features, or retention settings are required")
		return
	}

	if name != "" {
		if _, err := h.Service.RenameProject(name); err != nil {
			webapi.WriteInternalError(w, "project_rename_failed", err.Error())
			return
		}
	}
	if req.Features != nil {
		if _, err := h.Service.SetProjectFeatures(req.Features); err != nil {
			webapi.WriteInternalError(w, "project_update_failed", err.Error())
			return
		}
	}
	if req.Retention != nil {
		if _, err := h.Service.SetProjectRetention(*req.Retention); err != nil {
			webapi.WriteBadRequest(w, "invalid_retention_settings", err.Error())
			return
		}
	}
	if h.Publisher != nil {
		h.Publisher.ConfigChanged(r.Context(), ".renart/project.yml", "config.updated")
	}

	cfg, configPath, err := h.Service.LoadForEditing()
	if err != nil {
		webapi.WriteJSON(w, http.StatusOK, h.Service.BuildParseErrorResponse(err))
		return
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(configPath, cfg))
}

func (h *ConfigHandlers) HandleGetEnvironmentPolicy(w http.ResponseWriter, r *http.Request) {
	environment := strings.TrimSpace(chi.URLParam(r, "environment"))
	if environment == "" {
		webapi.WriteBadRequest(w, "missing_environment", "environment is required")
		return
	}
	if h.Policies == nil {
		webapi.WriteInternalError(w, "policy_loader_missing", "environment policy loader is not configured")
		return
	}

	webapi.WriteJSON(w, http.StatusOK, service.WorkspaceEnvironmentPolicyResponse{
		Status:      "ok",
		Environment: environment,
		Policy:      h.Policies.For(environment),
	})
}

func (h *ConfigHandlers) HandleUpdateEnvironmentPolicy(w http.ResponseWriter, r *http.Request) {
	environment := strings.TrimSpace(chi.URLParam(r, "environment"))
	if environment == "" {
		webapi.WriteBadRequest(w, "missing_environment", "environment is required")
		return
	}
	if h.Policies == nil {
		webapi.WriteInternalError(w, "policy_loader_missing", "environment policy loader is not configured")
		return
	}

	req, err := decodeJSONObject[policy.EnvironmentPolicy](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if _, err := h.Policies.Set(environment, req); err != nil {
		webapi.WriteInternalError(w, "environment_policy_persist_failed", err.Error())
		return
	}
	if h.Publisher != nil {
		h.Publisher.ConfigChanged(r.Context(), ".renart/environments.yml", "config.updated")
	}

	webapi.WriteJSON(w, http.StatusOK, service.WorkspaceEnvironmentPolicyResponse{
		Status:      "ok",
		Environment: environment,
		Policy:      h.Policies.For(environment),
	})
}

func (h *ConfigHandlers) HandleCreateWorkspaceEnvironment(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[CreateWorkspaceEnvironmentRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	change, err := h.Service.CreateEnvironmentAndPersist(
		req.Name,
		req.SchemaPrefix,
		req.SetAsDefault,
	)
	if err != nil {
		webapi.WriteBadRequest(w, "environment_create_failed", err.Error())
		return
	}
	if h.Publisher != nil {
		h.Publisher.ConfigChanged(r.Context(), change.RelPath, "config.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(change.ConfigPath, change.Config))
}

func (h *ConfigHandlers) HandleUpdateWorkspaceEnvironment(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[UpdateWorkspaceEnvironmentRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	currentName := strings.TrimSpace(req.Name)
	nextName := strings.TrimSpace(req.NewName)
	if nextName == "" {
		nextName = currentName
	}
	change, err := h.Service.UpdateEnvironmentAndPersist(
		r.Context(),
		currentName,
		nextName,
		req.SchemaPrefix,
		req.SetAsDefault,
	)
	if err != nil {
		webapi.WriteBadRequest(w, "environment_update_failed", err.Error())
		return
	}

	// Renaming must carry the renart policy along, otherwise the guardrails
	// silently stay behind under the old name.
	if h.Policies != nil && nextName != currentName {
		if envPolicy := h.Policies.For(currentName); !envPolicy.Zero() {
			if _, err := h.Policies.Set(nextName, envPolicy); err == nil {
				_, _ = h.Policies.Set(currentName, policy.EnvironmentPolicy{})
			}
		}
	}
	if h.Publisher != nil {
		h.Publisher.ConfigChanged(r.Context(), change.RelPath, "config.updated")
		h.Publisher.ConfigChanged(r.Context(), ".renart/secrets.yml", "config.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(change.ConfigPath, change.Config))
}

func (h *ConfigHandlers) HandleCloneWorkspaceEnvironment(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[CloneWorkspaceEnvironmentRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	change, err := h.Service.CloneEnvironmentAndPersist(
		r.Context(),
		req.SourceName,
		req.TargetName,
		req.SchemaPrefix,
		req.SetAsDefault,
	)
	if err != nil {
		webapi.WriteBadRequest(w, "environment_clone_failed", err.Error())
		return
	}

	// Clones inherit the source's guardrails; erring on the protected side
	// beats silently dropping a protection flag.
	if h.Policies != nil {
		if envPolicy := h.Policies.For(strings.TrimSpace(req.SourceName)); !envPolicy.Zero() {
			_, _ = h.Policies.Set(strings.TrimSpace(req.TargetName), envPolicy)
		}
	}
	if h.Publisher != nil {
		h.Publisher.ConfigChanged(r.Context(), change.RelPath, "config.updated")
		h.Publisher.ConfigChanged(r.Context(), ".renart/secrets.yml", "config.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(change.ConfigPath, change.Config))
}

func (h *ConfigHandlers) HandleDeleteWorkspaceEnvironment(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[DeleteWorkspaceEnvironmentRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	change, err := h.Service.DeleteEnvironmentAndPersist(r.Context(), req.Name)
	if err != nil {
		webapi.WriteBadRequest(w, "environment_delete_failed", err.Error())
		return
	}
	if h.Policies != nil && !h.Policies.For(strings.TrimSpace(req.Name)).Zero() {
		_, _ = h.Policies.Set(strings.TrimSpace(req.Name), policy.EnvironmentPolicy{})
	}

	if h.Publisher != nil {
		h.Publisher.ConfigChanged(r.Context(), change.RelPath, "config.updated")
		h.Publisher.ConfigChanged(r.Context(), ".renart/secrets.yml", "config.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(change.ConfigPath, change.Config))
}

func (h *ConfigHandlers) HandleCreateWorkspaceConnection(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[UpsertWorkspaceConnectionRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	change, err := h.Service.CreateConnectionAndPersist(r.Context(), service.UpsertWorkspaceConnectionParams{
		EnvironmentName: req.EnvironmentName,
		CurrentName:     req.CurrentName,
		Name:            req.Name,
		Type:            req.Type,
		Values:          req.Values,
		SecretChanges:   req.SecretChanges,
	})
	if err != nil {
		webapi.WriteBadRequest(w, "connection_create_failed", err.Error())
		return
	}
	if h.Publisher != nil {
		h.Publisher.ConfigChanged(r.Context(), change.RelPath, "config.updated")
		h.Publisher.ConfigChanged(r.Context(), ".renart/secrets.yml", "config.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(change.ConfigPath, change.Config))
}

func (h *ConfigHandlers) HandleUpdateWorkspaceConnection(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[UpsertWorkspaceConnectionRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	change, err := h.Service.UpdateConnectionAndPersist(r.Context(), service.UpsertWorkspaceConnectionParams{
		EnvironmentName: req.EnvironmentName,
		CurrentName:     req.CurrentName,
		Name:            req.Name,
		Type:            req.Type,
		Values:          req.Values,
		SecretChanges:   req.SecretChanges,
	})
	if err != nil {
		webapi.WriteBadRequest(w, "connection_update_failed", err.Error())
		return
	}
	if h.Publisher != nil {
		h.Publisher.ConfigChanged(r.Context(), change.RelPath, "config.updated")
		h.Publisher.ConfigChanged(r.Context(), ".renart/secrets.yml", "config.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(change.ConfigPath, change.Config))
}

func (h *ConfigHandlers) HandleDeleteWorkspaceConnection(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[DeleteWorkspaceConnectionRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	change, err := h.Service.DeleteConnectionAndPersist(
		r.Context(),
		req.EnvironmentName,
		req.Name,
	)
	if err != nil {
		webapi.WriteBadRequest(w, "connection_delete_failed", err.Error())
		return
	}
	if h.Publisher != nil {
		h.Publisher.ConfigChanged(r.Context(), change.RelPath, "config.updated")
		h.Publisher.ConfigChanged(r.Context(), ".renart/secrets.yml", "config.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(change.ConfigPath, change.Config))
}

func (h *ConfigHandlers) HandleTestWorkspaceConnection(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[TestWorkspaceConnectionRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	cfg, _, err := h.Service.LoadForEditing()
	if err != nil {
		webapi.WriteInternalError(w, "config_load_failed", err.Error())
		return
	}

	message, err := h.Service.TestConnection(r.Context(), cfg, service.TestWorkspaceConnectionParams{
		EnvironmentName: req.EnvironmentName,
		CurrentName:     req.CurrentName,
		Name:            req.Name,
		Type:            req.Type,
		Values:          req.Values,
		SecretChanges:   req.SecretChanges,
	})
	if err != nil {
		trimmed := strings.TrimSpace(err.Error())
		switch {
		case trimmed == "no environment selected":
			webapi.WriteBadRequest(w, "missing_environment", trimmed)
		case trimmed == "connection name is required":
			webapi.WriteBadRequest(w, "missing_connection_name", trimmed)
		case strings.Contains(trimmed, "not found"):
			webapi.WriteBadRequest(w, "missing_connection", trimmed)
		case strings.Contains(trimmed, "failed to test connection"):
			webapi.WriteBadRequest(w, "connection_test_failed", trimmed)
		default:
			webapi.WriteInternalError(w, "connection_manager_failed", trimmed)
		}
		return
	}

	webapi.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": message,
	})
}

func (h *ConfigHandlers) persistAndRespond(ctx context.Context, w http.ResponseWriter, cfg *config.Config, configPath string) {
	relPath, err := h.Service.Persist(cfg)
	if err != nil {
		webapi.WriteInternalError(w, "config_persist_failed", err.Error())
		return
	}
	if h.Publisher != nil {
		h.Publisher.ConfigChanged(ctx, relPath, "config.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, h.Service.BuildResponse(configPath, cfg))
}
