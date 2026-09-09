package service

import (
	"github.com/bruin-data/bruin/pkg/config"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestWorkspaceConfigurationSource(t *testing.T) {
	root := t.TempDir()
	for _, relative := range []string{".bruin.yml", "../.bruin.yml", "../sibling/.bruin.yml", "config/.bruin.yml"} {
		t.Run(relative, func(t *testing.T) {
			configPath := filepath.Join(root, relative)
			svc := NewConfigService(root, configPath)
			response := svc.BuildResponse(configPath, &config.Config{Environments: map[string]config.Environment{}})
			require.Equal(t, filepath.ToSlash(relative), response.ConfigurationPath)
			require.Equal(t, relative == "../.bruin.yml" || relative == "../sibling/.bruin.yml", response.ConfigurationInherited)
			require.Equal(t, ".bruin.yml", response.Path) // Existing API field stays compatible.
		})
	}
}
