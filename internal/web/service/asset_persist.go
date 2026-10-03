package service

import (
	"slices"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
)

// persistExecutableAsset writes a SQL or Python asset through Bruin's Persist
// without the connection secret that Bruin's builder adds while parsing
// (Builder.InjectConnectionAsSecret). Assets resolved through a mutating
// builder carry {key: <connection>, inject_as: <connection>} in memory. It is
// a runtime default, not authored metadata: writing it back would add a
// `secrets:` block to every asset with an explicit connection. A mapping the
// file itself declares is kept.
func persistExecutableAsset(fs afero.Fs, asset *pipeline.Asset, pl ...*pipeline.Pipeline) error {
	persisted := *asset
	persisted.Secrets = secretsWithoutInjectedConnection(fs, asset)
	return persisted.Persist(fs, pl...)
}

func secretsWithoutInjectedConnection(fs afero.Fs, asset *pipeline.Asset) []pipeline.SecretMapping {
	connection := strings.TrimSpace(asset.Connection)
	if connection == "" {
		return asset.Secrets
	}
	injected := slices.IndexFunc(asset.Secrets, func(secret pipeline.SecretMapping) bool {
		return secret.SecretKey == connection && secret.InjectedKey == connection
	})
	if injected < 0 {
		return asset.Secrets
	}
	// The comment task creator parses the file without builder mutators, so its
	// secrets are exactly the authored ones. When the file cannot be read, keep
	// the mapping rather than risk dropping one the user wrote.
	authored, err := pipeline.CreateTaskFromFileComments(fs)(asset.ExecutableFile.Path)
	if err != nil || authored == nil {
		return asset.Secrets
	}
	if slices.ContainsFunc(authored.Secrets, func(secret pipeline.SecretMapping) bool {
		return secret.SecretKey == connection
	}) {
		return asset.Secrets
	}
	return slices.Delete(slices.Clone(asset.Secrets), injected, injected+1)
}
