package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/bruin-data/bruin/pkg/config"
	"renart/internal/web/policy"
	"renart/internal/web/secretstore"
	"renart/internal/web/workspacefs"
)

type PersistedConnectionChange struct {
	Config     *config.Config
	ConfigPath string
	RelPath    string
}

func (s *ConfigService) CreateEnvironmentAndPersist(
	name string,
	schemaPrefix string,
	setAsDefault bool,
) (PersistedConnectionChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, lockErr := s.lockConfiguration()
	if lockErr != nil {
		return PersistedConnectionChange{}, lockErr
	}
	defer unlock()

	cfg, configPath, err := s.LoadForEditing()
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	name = strings.TrimSpace(name)
	if err := cfg.AddEnvironment(name, strings.TrimSpace(schemaPrefix)); err != nil {
		return PersistedConnectionChange{}, err
	}
	if setAsDefault {
		cfg.DefaultEnvironmentName = name
	}
	relPath, err := s.Persist(cfg)
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	return PersistedConnectionChange{Config: cfg, ConfigPath: configPath, RelPath: relPath}, nil
}

func (s *ConfigService) UpdateEnvironmentAndPersist(
	ctx context.Context,
	currentName string,
	nextName string,
	schemaPrefix string,
	setAsDefault bool,
) (PersistedConnectionChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, lockErr := s.lockConfiguration()
	if lockErr != nil {
		return PersistedConnectionChange{}, lockErr
	}
	defer unlock()

	currentName = strings.TrimSpace(currentName)
	nextName = strings.TrimSpace(nextName)
	if nextName == "" {
		nextName = currentName
	}
	cfg, configPath, err := s.LoadForEditing()
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	if err := cfg.UpdateEnvironment(currentName, nextName, strings.TrimSpace(schemaPrefix)); err != nil {
		return PersistedConnectionChange{}, err
	}
	if setAsDefault {
		cfg.DefaultEnvironmentName = nextName
	}

	manifestPath := filepath.Join(s.workspaceRoot, ".renart", "secrets.yml")
	manifest, err := secretstore.LoadManifest(manifestPath)
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	nextManifest := cloneSecretManifest(manifest)
	mutations := make([]providerMutation, 0)
	if currentName != nextName {
		environment, found := nextManifest.Environments[currentName]
		if found {
			storedReferences := uniqueStoredReferences(environment)
			copyMutations, err := s.copyStoredSecretMutations(
				ctx,
				currentName,
				nextName,
				storedReferences,
				true,
			)
			if err != nil {
				return PersistedConnectionChange{}, err
			}
			mutations = append(mutations, copyMutations...)
			delete(nextManifest.Environments, currentName)
			for connectionName, fields := range environment.Connections {
				for fieldName, binding := range fields {
					if err := nextManifest.SetBinding(nextName, connectionName, fieldName, binding); err != nil {
						clearSecretMutationSnapshots(mutations)
						return PersistedConnectionChange{}, err
					}
				}
			}
		}
	}
	return s.persistConfigAndSecretManifest(
		ctx,
		cfg,
		configPath,
		manifestPath,
		nextManifest,
		mutations,
		environmentPolicyChange(currentName, nextName, true),
	)
}

func (s *ConfigService) CloneEnvironmentAndPersist(
	ctx context.Context,
	sourceName string,
	targetName string,
	schemaPrefix string,
	setAsDefault bool,
) (PersistedConnectionChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, lockErr := s.lockConfiguration()
	if lockErr != nil {
		return PersistedConnectionChange{}, lockErr
	}
	defer unlock()

	sourceName = strings.TrimSpace(sourceName)
	targetName = strings.TrimSpace(targetName)
	cfg, configPath, err := s.LoadForEditing()
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	if err := cfg.CloneEnvironment(sourceName, targetName, strings.TrimSpace(schemaPrefix)); err != nil {
		return PersistedConnectionChange{}, err
	}
	if setAsDefault {
		cfg.DefaultEnvironmentName = targetName
	}

	manifestPath := filepath.Join(s.workspaceRoot, ".renart", "secrets.yml")
	manifest, err := secretstore.LoadManifest(manifestPath)
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	nextManifest := cloneSecretManifest(manifest)
	mutations := make([]providerMutation, 0)
	if environment, found := manifest.Environments[sourceName]; found {
		copyMutations, err := s.copyStoredSecretMutations(
			ctx,
			sourceName,
			targetName,
			uniqueStoredReferences(environment),
			false,
		)
		if err != nil {
			return PersistedConnectionChange{}, err
		}
		mutations = append(mutations, copyMutations...)
		for connectionName, fields := range environment.Connections {
			for fieldName, binding := range fields {
				if err := nextManifest.SetBinding(targetName, connectionName, fieldName, binding); err != nil {
					clearSecretMutationSnapshots(mutations)
					return PersistedConnectionChange{}, err
				}
			}
		}
	}
	return s.persistConfigAndSecretManifest(
		ctx,
		cfg,
		configPath,
		manifestPath,
		nextManifest,
		mutations,
		environmentPolicyChange(sourceName, targetName, false),
	)
}

func (s *ConfigService) DeleteEnvironmentAndPersist(
	ctx context.Context,
	environmentName string,
) (PersistedConnectionChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, lockErr := s.lockConfiguration()
	if lockErr != nil {
		return PersistedConnectionChange{}, lockErr
	}
	defer unlock()

	environmentName = strings.TrimSpace(environmentName)
	cfg, configPath, err := s.LoadForEditing()
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	if err := cfg.DeleteEnvironment(environmentName); err != nil {
		return PersistedConnectionChange{}, err
	}
	manifestPath := filepath.Join(s.workspaceRoot, ".renart", "secrets.yml")
	manifest, err := secretstore.LoadManifest(manifestPath)
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	nextManifest := cloneSecretManifest(manifest)
	mutations := make([]providerMutation, 0)
	if environment, found := nextManifest.Environments[environmentName]; found {
		for _, reference := range uniqueStoredReferences(environment) {
			mutations = append(mutations, providerMutation{
				environment: environmentName,
				reference:   reference,
				delete:      true,
			})
		}
		delete(nextManifest.Environments, environmentName)
	}
	return s.persistConfigAndSecretManifest(
		ctx,
		cfg,
		configPath,
		manifestPath,
		nextManifest,
		mutations,
		environmentPolicyChange(environmentName, "", true),
	)
}

func (s *ConfigService) CreateConnectionAndPersist(
	ctx context.Context,
	params UpsertWorkspaceConnectionParams,
) (PersistedConnectionChange, error) {
	return s.changeConnectionAndPersist(ctx, params, false)
}

func (s *ConfigService) UpdateConnectionAndPersist(
	ctx context.Context,
	params UpsertWorkspaceConnectionParams,
) (PersistedConnectionChange, error) {
	return s.changeConnectionAndPersist(ctx, params, true)
}

func (s *ConfigService) DeleteConnectionAndPersist(
	ctx context.Context,
	environmentName string,
	connectionName string,
) (PersistedConnectionChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, lockErr := s.lockConfiguration()
	if lockErr != nil {
		return PersistedConnectionChange{}, lockErr
	}
	defer unlock()

	environmentName = strings.TrimSpace(environmentName)
	connectionName = strings.TrimSpace(connectionName)
	cfg, configPath, err := s.LoadForEditing()
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	manifestPath := filepath.Join(s.workspaceRoot, ".renart", "secrets.yml")
	manifest, err := secretstore.LoadManifest(manifestPath)
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	nextManifest := cloneSecretManifest(manifest)
	mutations := make([]providerMutation, 0)
	if environment, found := nextManifest.Environments[environmentName]; found {
		for fieldName, binding := range environment.Connections[connectionName] {
			nextManifest.RemoveBinding(environmentName, connectionName, fieldName)
			if isEditorStoredSecretProvider(binding.Reference.Provider) {
				mutations = append(mutations, providerMutation{
					environment: environmentName,
					reference:   binding.Reference,
					delete:      true,
				})
			}
		}
	}
	mutations = filterReferencedSecretDeletes(mutations, nextManifest)
	if err := cfg.DeleteConnection(environmentName, connectionName); err != nil {
		return PersistedConnectionChange{}, err
	}

	return s.persistConfigAndSecretManifest(
		ctx,
		cfg,
		configPath,
		manifestPath,
		nextManifest,
		mutations,
		connectionPolicyChange(environmentName, connectionName, "", nil),
	)
}

func (s *ConfigService) changeConnectionAndPersist(
	ctx context.Context,
	params UpsertWorkspaceConnectionParams,
	update bool,
) (PersistedConnectionChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, lockErr := s.lockConfiguration()
	if lockErr != nil {
		return PersistedConnectionChange{}, lockErr
	}
	defer unlock()

	if params.PolicyRevision != "" {
		snapshot, err := policy.NewLoader(s.environmentPolicyPath()).Snapshot()
		if err != nil {
			return PersistedConnectionChange{}, err
		}
		if snapshot.Revision != params.PolicyRevision {
			return PersistedConnectionChange{}, newAPIError(409, "connection_policy_stale", "Connection access settings changed. Reload the connection before saving.")
		}
	}

	cfg, configPath, err := s.LoadForEditing()
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	manifestPath := filepath.Join(s.workspaceRoot, ".renart", "secrets.yml")
	manifest, err := secretstore.LoadManifest(manifestPath)
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	nextManifest := cloneSecretManifest(manifest)
	transformed, mutations, err := s.prepareManagedConnectionSecrets(
		cfg,
		nextManifest,
		params,
		update,
	)
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	if update {
		err = s.UpdateConnection(cfg, transformed)
	} else {
		err = s.AddConnection(cfg, transformed)
	}
	if err != nil {
		return PersistedConnectionChange{}, err
	}

	return s.persistConfigAndSecretManifest(
		ctx,
		cfg,
		configPath,
		manifestPath,
		nextManifest,
		mutations,
		connectionPolicyChange(params.EnvironmentName, params.CurrentName, params.Name, params.AccessMode),
	)
}

type providerMutation struct {
	environment string
	reference   secretstore.Ref
	value       []byte
	delete      bool
	previous    []byte
	existed     bool
}

func (s *ConfigService) prepareManagedConnectionSecrets(
	cfg *config.Config,
	manifest secretstore.Manifest,
	params UpsertWorkspaceConnectionParams,
	update bool,
) (UpsertWorkspaceConnectionParams, []providerMutation, error) {
	environmentName := strings.TrimSpace(params.EnvironmentName)
	connectionName := strings.TrimSpace(params.Name)
	currentName := strings.TrimSpace(params.CurrentName)
	if currentName == "" {
		currentName = connectionName
	}

	typeName := normalizeConnectionType(params.Type)
	if update && typeName == "" {
		if environment, found := cfg.Environments[environmentName]; found && environment.Connections != nil {
			typeName = normalizeConnectionType(environment.Connections.ConnectionsSummaryList()[currentName])
		}
	}
	fields := workspaceConnectionFieldDefsForType(typeName)
	fieldByName := make(map[string]WorkspaceConfigFieldDef, len(fields))
	for _, field := range fields {
		fieldByName[field.Name] = field
	}

	transformed := params
	transformed.SecretChanges = cloneWorkspaceSecretChanges(params.SecretChanges)
	mutations := make([]providerMutation, 0)
	mutationByReference := make(map[string]int)

	for _, field := range fields {
		if !field.IsSensitive && !field.IsSensitiveFile {
			continue
		}
		oldBinding, hasOldBinding := manifest.Binding(environmentName, currentName, field.Name)
		change, hasChange := transformed.SecretChanges[field.Name]
		action := strings.ToLower(strings.TrimSpace(change.Action))
		if !hasChange || action == "" {
			action = "keep"
			change.Action = action
		}

		switch action {
		case "keep":
			if update && currentName != connectionName && hasOldBinding {
				manifest.RemoveBinding(environmentName, currentName, field.Name)
				if err := manifest.SetBinding(environmentName, connectionName, field.Name, oldBinding); err != nil {
					return params, nil, err
				}
			}
		case "replace":
			reference, symbol, hasRequestedBinding, err := managedSecretBinding(
				change.Binding,
				oldBinding,
				connectionName,
				field.Name,
			)
			if err != nil {
				return params, nil, err
			}

			if hasRequestedBinding && reference.Provider == "env" {
				if change.Value != "" {
					return params, nil, fmt.Errorf(
						"environment binding for connection secret field %q cannot include a secret value",
						field.Name,
					)
				}
				if err := manifest.SetBinding(environmentName, connectionName, field.Name, secretstore.Binding{
					Symbol: symbol, Reference: reference,
				}); err != nil {
					return params, nil, err
				}
				if currentName != connectionName {
					manifest.RemoveBinding(environmentName, currentName, field.Name)
				}
				change.Value = "${" + symbol + "}"
				transformed.SecretChanges[field.Name] = change
				if hasOldBinding && isEditorStoredSecretProvider(oldBinding.Reference.Provider) &&
					oldBinding.Reference.String() != reference.String() {
					if err := appendProviderMutation(
						&mutations,
						mutationByReference,
						providerMutation{
							environment: environmentName,
							reference:   oldBinding.Reference,
							delete:      true,
						},
					); err != nil {
						return params, nil, err
					}
				}
				continue
			}
			if hasRequestedBinding && !isEditorStoredSecretProvider(reference.Provider) {
				return params, nil, fmt.Errorf(
					"connection secret field %q supports only local:, local-vault:, and env: bindings in the editor",
					field.Name,
				)
			}
			if change.Value == "" {
				return params, nil, fmt.Errorf(
					"replacement value for connection secret field %q is required",
					field.Name,
				)
			}
			if field.IsSensitiveFile {
				// A file secret is a path, not inline credential content. Keep
				// the existing write-only path behavior until temporary-file
				// leases are implemented.
				manifest.RemoveBinding(environmentName, currentName, field.Name)
				if currentName != connectionName {
					manifest.RemoveBinding(environmentName, connectionName, field.Name)
				}
				if hasOldBinding && isEditorStoredSecretProvider(oldBinding.Reference.Provider) {
					if err := appendProviderMutation(
						&mutations,
						mutationByReference,
						providerMutation{
							environment: environmentName,
							reference:   oldBinding.Reference,
							delete:      true,
						},
					); err != nil {
						return params, nil, err
					}
				}
				break
			}
			if err := manifest.SetBinding(environmentName, connectionName, field.Name, secretstore.Binding{
				Symbol: symbol, Reference: reference,
			}); err != nil {
				return params, nil, err
			}
			if currentName != connectionName {
				manifest.RemoveBinding(environmentName, currentName, field.Name)
			}
			change.Value = "${" + symbol + "}"
			transformed.SecretChanges[field.Name] = change
			if err := appendProviderMutation(
				&mutations,
				mutationByReference,
				providerMutation{
					environment: environmentName,
					reference:   reference,
					value:       []byte(params.SecretChanges[field.Name].Value),
				},
			); err != nil {
				return params, nil, err
			}
			if hasOldBinding && isEditorStoredSecretProvider(oldBinding.Reference.Provider) &&
				oldBinding.Reference.String() != reference.String() {
				if err := appendProviderMutation(
					&mutations,
					mutationByReference,
					providerMutation{
						environment: environmentName,
						reference:   oldBinding.Reference,
						delete:      true,
					},
				); err != nil {
					return params, nil, err
				}
			}
		case "clear":
			manifest.RemoveBinding(environmentName, currentName, field.Name)
			if connectionName != currentName {
				manifest.RemoveBinding(environmentName, connectionName, field.Name)
			}
			if hasOldBinding && isEditorStoredSecretProvider(oldBinding.Reference.Provider) {
				if err := appendProviderMutation(
					&mutations,
					mutationByReference,
					providerMutation{
						environment: environmentName,
						reference:   oldBinding.Reference,
						delete:      true,
					},
				); err != nil {
					return params, nil, err
				}
			}
		default:
			return params, nil, fmt.Errorf(
				"connection secret field %q action must be keep, replace, or clear",
				field.Name,
			)
		}
	}

	for name := range transformed.SecretChanges {
		field, found := fieldByName[name]
		if !found || (!field.IsSensitive && !field.IsSensitiveFile) {
			return params, nil, fmt.Errorf("unknown connection secret field %q for type %q", name, typeName)
		}
	}
	return transformed, filterReferencedSecretDeletes(mutations, manifest), nil
}

func managedSecretBinding(
	requested *WorkspaceConnectionSecretBinding,
	existing secretstore.Binding,
	connectionName string,
	fieldName string,
) (secretstore.Ref, string, bool, error) {
	symbol := existing.Symbol
	if symbol == "" {
		symbol = managedSecretSymbol(connectionName, fieldName)
	}
	if requested == nil || strings.TrimSpace(requested.Ref) == "" {
		if requested != nil && strings.TrimSpace(requested.Provider) != "" {
			provider := strings.TrimSpace(requested.Provider)
			if !isEditorStoredSecretProvider(provider) {
				return secretstore.Ref{}, "", false, fmt.Errorf(
					"connection secret storage provider must be local or local-vault",
				)
			}
			return secretstore.Ref{
				Provider: provider,
				Key:      managedSecretAlias(connectionName, fieldName),
			}, symbol, true, nil
		}
		if isEditorStoredSecretProvider(existing.Reference.Provider) {
			return existing.Reference, symbol, false, nil
		}
		return secretstore.Ref{
			Provider: "local",
			Key:      managedSecretAlias(connectionName, fieldName),
		}, symbol, false, nil
	}
	reference, err := secretstore.ParseRef(requested.Ref)
	if err != nil {
		return secretstore.Ref{}, "", false, err
	}
	if provider := strings.TrimSpace(requested.Provider); provider != "" &&
		provider != reference.Provider {
		return secretstore.Ref{}, "", false, errors.New(
			"connection secret binding provider does not match its reference",
		)
	}
	return reference, symbol, true, nil
}

func isEditorStoredSecretProvider(provider string) bool {
	return provider == "local" || provider == "local-vault"
}

func managedSecretSymbol(connectionName, fieldName string) string {
	return "RENART_" + upperIdentifier(connectionName) + "_" + upperIdentifier(fieldName)
}

func upperIdentifier(value string) string {
	var builder strings.Builder
	previousUnderscore := false
	for _, character := range strings.TrimSpace(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			builder.WriteRune(unicode.ToUpper(character))
			previousUnderscore = false
			continue
		}
		if builder.Len() > 0 && !previousUnderscore {
			builder.WriteByte('_')
			previousUnderscore = true
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "SECRET"
	}
	if result[0] >= '0' && result[0] <= '9' {
		return "VALUE_" + result
	}
	return result
}

func managedSecretAlias(connectionName, fieldName string) string {
	segment := strings.ToLower(strings.TrimSpace(connectionName))
	var builder strings.Builder
	changed := false
	for _, character := range segment {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '.' || character == '_' || character == '-' {
			builder.WriteRune(character)
			continue
		}
		builder.WriteByte('-')
		changed = true
	}
	segment = strings.Trim(builder.String(), "-.")
	if segment == "" {
		segment = "connection"
		changed = true
	}
	if changed {
		hash := sha256.Sum256([]byte(connectionName))
		segment = fmt.Sprintf("%s-%x", segment, hash[:3])
	}
	return segment + "/" + strings.ToLower(upperIdentifier(fieldName))
}

func cloneWorkspaceSecretChanges(
	input map[string]WorkspaceConnectionSecretChange,
) map[string]WorkspaceConnectionSecretChange {
	if len(input) == 0 {
		return map[string]WorkspaceConnectionSecretChange{}
	}
	result := make(map[string]WorkspaceConnectionSecretChange, len(input))
	for name, change := range input {
		if change.Binding != nil {
			binding := *change.Binding
			change.Binding = &binding
		}
		result[name] = change
	}
	return result
}

func cloneSecretManifest(input secretstore.Manifest) secretstore.Manifest {
	result := secretstore.NewManifest()
	for environmentName, environment := range input.Environments {
		for connectionName, fields := range environment.Connections {
			for fieldName, binding := range fields {
				_ = result.SetBinding(environmentName, connectionName, fieldName, binding)
			}
		}
	}
	return result
}

func appendProviderMutation(
	mutations *[]providerMutation,
	indexByReference map[string]int,
	mutation providerMutation,
) error {
	key := mutation.environment + "\x00" + mutation.reference.String()
	if existingIndex, found := indexByReference[key]; found {
		existing := (*mutations)[existingIndex]
		if existing.delete != mutation.delete || string(existing.value) != string(mutation.value) {
			return fmt.Errorf("secret reference %s is assigned conflicting changes", mutation.reference.String())
		}
		return nil
	}
	indexByReference[key] = len(*mutations)
	*mutations = append(*mutations, mutation)
	return nil
}

func filterReferencedSecretDeletes(
	mutations []providerMutation,
	manifest secretstore.Manifest,
) []providerMutation {
	result := mutations[:0]
	for _, mutation := range mutations {
		if mutation.delete && manifestUsesSecretReference(manifest, mutation.environment, mutation.reference) {
			continue
		}
		result = append(result, mutation)
	}
	return result
}

func manifestUsesSecretReference(
	manifest secretstore.Manifest,
	environmentName string,
	reference secretstore.Ref,
) bool {
	environment, found := manifest.Environments[environmentName]
	if !found {
		return false
	}
	for _, fields := range environment.Connections {
		for _, binding := range fields {
			if binding.Reference.String() == reference.String() {
				return true
			}
		}
	}
	return false
}

func uniqueStoredReferences(environment secretstore.EnvironmentBindings) []secretstore.Ref {
	seen := make(map[string]struct{})
	references := make([]secretstore.Ref, 0)
	for _, fields := range environment.Connections {
		for _, binding := range fields {
			if !isEditorStoredSecretProvider(binding.Reference.Provider) {
				continue
			}
			key := binding.Reference.String()
			if _, found := seen[key]; found {
				continue
			}
			seen[key] = struct{}{}
			references = append(references, binding.Reference)
		}
	}
	return references
}

func (s *ConfigService) copyStoredSecretMutations(
	ctx context.Context,
	sourceEnvironment string,
	targetEnvironment string,
	references []secretstore.Ref,
	deleteSource bool,
) ([]providerMutation, error) {
	projectID := s.ProjectIdentity().ID
	mutations := make([]providerMutation, 0, len(references)*2)
	for _, reference := range references {
		lease, err := s.secretResolver.Resolve(ctx, secretstore.ResolveRequest{
			ProjectID:   projectID,
			Environment: sourceEnvironment,
			Reference:   reference,
			Purpose:     secretstore.PurposeSecretAdministration,
		})
		if errors.Is(err, secretstore.ErrNotFound) {
			continue
		}
		if err != nil {
			clearSecretMutationSnapshots(mutations)
			return nil, fmt.Errorf("read stored secret for environment %s: %w", sourceEnvironment, err)
		}
		value := append([]byte(nil), lease.Bytes()...)
		_ = lease.Close(ctx)
		mutations = append(mutations, providerMutation{
			environment: targetEnvironment,
			reference:   reference,
			value:       value,
		})
		if deleteSource {
			mutations = append(mutations, providerMutation{
				environment: sourceEnvironment,
				reference:   reference,
				delete:      true,
			})
		}
	}
	return mutations, nil
}

func (s *ConfigService) persistConfigAndSecretManifest(
	ctx context.Context,
	cfg *config.Config,
	configPath string,
	manifestPath string,
	nextManifest secretstore.Manifest,
	mutations []providerMutation,
	policyChanges ...configurationPolicyChange,
) (PersistedConnectionChange, error) {
	policyPath := s.environmentPolicyPath()
	policyBackup, policyExisted, policyMode, err := readOptionalFile(policyPath)
	if err != nil {
		return PersistedConnectionChange{}, err
	}
	nextPolicy, err := policy.Load(policyPath)
	if err != nil {
		return PersistedConnectionChange{}, policy.InvalidError(err.Error())
	}
	if nextPolicy.Environments == nil {
		nextPolicy.Environments = map[string]policy.EnvironmentPolicy{}
	}
	for _, change := range policyChanges {
		if err := change(&nextPolicy); err != nil {
			return PersistedConnectionChange{}, err
		}
	}
	if err := nextPolicy.Validate(); err != nil {
		return PersistedConnectionChange{}, err
	}
	configBackup, configExisted, configMode, err := readOptionalFile(configPath)
	if err != nil {
		clearSecretMutationSnapshots(mutations)
		return PersistedConnectionChange{}, err
	}
	defer clearSecretBytes(configBackup)
	manifestBackup, manifestExisted, manifestMode, err := readOptionalFile(manifestPath)
	if err != nil {
		clearSecretMutationSnapshots(mutations)
		return PersistedConnectionChange{}, err
	}
	defer clearSecretBytes(manifestBackup)
	if err := workspacefs.WriteFileAtomic(policy.PendingPath(policyPath), []byte("configuration transaction in progress\n"), 0o600); err != nil {
		return PersistedConnectionChange{}, err
	}
	applied, err := s.applySecretMutations(ctx, mutations)
	if err != nil {
		clearSecretMutationSnapshots(mutations)
		_ = os.Remove(policy.PendingPath(policyPath))
		return PersistedConnectionChange{}, err
	}
	rollback := func() {
		manifestErr := restoreOptionalFile(manifestPath, manifestBackup, manifestExisted, manifestMode)
		configErr := restoreOptionalFile(configPath, configBackup, configExisted, configMode)
		policyErr := restoreOptionalFile(policyPath, policyBackup, policyExisted, policyMode)
		if manifestErr == nil && configErr == nil && policyErr == nil {
			_ = os.Remove(policy.PendingPath(policyPath))
		}
		s.rollbackSecretMutations(ctx, applied)
	}
	if err := secretstore.SaveManifest(manifestPath, nextManifest); err != nil {
		rollback()
		return PersistedConnectionChange{}, err
	}
	relPath, err := s.Persist(cfg)
	if err != nil {
		rollback()
		return PersistedConnectionChange{}, err
	}
	if policyExisted || len(nextPolicy.Environments) > 0 {
		if err := policy.Save(policyPath, nextPolicy); err != nil {
			rollback()
			return PersistedConnectionChange{}, err
		}
	}
	if err := os.Remove(policy.PendingPath(policyPath)); err != nil {
		rollback()
		return PersistedConnectionChange{}, err
	}
	clearSecretMutationSnapshots(applied)
	return PersistedConnectionChange{Config: cfg, ConfigPath: configPath, RelPath: relPath}, nil
}

func (s *ConfigService) applySecretMutations(
	ctx context.Context,
	mutations []providerMutation,
) ([]providerMutation, error) {
	applied := make([]providerMutation, 0, len(mutations))
	projectID := s.ProjectIdentity().ID
	for _, mutation := range mutations {
		request := secretstore.ResolveRequest{
			ProjectID:   projectID,
			Environment: mutation.environment,
			Reference:   mutation.reference,
			Purpose:     secretstore.PurposeSecretAdministration,
		}
		lease, err := s.secretResolver.Resolve(ctx, request)
		switch {
		case err == nil:
			mutation.previous = append([]byte(nil), lease.Bytes()...)
			mutation.existed = true
			_ = lease.Close(ctx)
		case errors.Is(err, secretstore.ErrNotFound):
			mutation.existed = false
		default:
			s.rollbackSecretMutations(ctx, applied)
			return nil, fmt.Errorf("read existing %s secret: %w", mutation.reference.Provider, err)
		}

		if mutation.delete {
			err = s.secretResolver.Delete(ctx, secretstore.DeleteRequest{
				ProjectID:   projectID,
				Environment: mutation.environment,
				Reference:   mutation.reference,
				Purpose:     secretstore.PurposeSecretAdministration,
			})
		} else {
			_, err = s.secretResolver.Put(ctx, secretstore.PutRequest{
				ProjectID:   projectID,
				Environment: mutation.environment,
				Reference:   mutation.reference,
				Value:       mutation.value,
				Purpose:     secretstore.PurposeSecretAdministration,
			})
		}
		if err != nil {
			s.rollbackSecretMutations(ctx, applied)
			clearSecretMutationSnapshots([]providerMutation{mutation})
			return nil, fmt.Errorf("update %s secret: %w", mutation.reference.Provider, err)
		}
		applied = append(applied, mutation)
	}
	return applied, nil
}

func (s *ConfigService) rollbackSecretMutations(ctx context.Context, mutations []providerMutation) {
	projectID := s.ProjectIdentity().ID
	for index := len(mutations) - 1; index >= 0; index-- {
		mutation := mutations[index]
		if mutation.existed {
			_, _ = s.secretResolver.Put(ctx, secretstore.PutRequest{
				ProjectID:   projectID,
				Environment: mutation.environment,
				Reference:   mutation.reference,
				Value:       mutation.previous,
				Purpose:     secretstore.PurposeSecretAdministration,
			})
		} else {
			_ = s.secretResolver.Delete(ctx, secretstore.DeleteRequest{
				ProjectID:   projectID,
				Environment: mutation.environment,
				Reference:   mutation.reference,
				Purpose:     secretstore.PurposeSecretAdministration,
			})
		}
	}
	clearSecretMutationSnapshots(mutations)
}

func clearSecretMutationSnapshots(mutations []providerMutation) {
	for index := range mutations {
		clearSecretBytes(mutations[index].previous)
		clearSecretBytes(mutations[index].value)
	}
}

func clearSecretBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func readOptionalFile(path string) ([]byte, bool, os.FileMode, error) {
	handle, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, 0, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return nil, false, 0, err
	}
	content, err := io.ReadAll(handle)
	if err != nil {
		clearSecretBytes(content)
		return nil, false, 0, err
	}
	return content, true, info.Mode().Perm(), nil
}

func restoreOptionalFile(path string, content []byte, existed bool, mode os.FileMode) error {
	if !existed {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".restore-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
