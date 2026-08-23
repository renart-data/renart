// Package workspacefs owns filesystem primitives shared by Git-authored
// application domains. It keeps encoded route IDs, traversal-safe path
// resolution, and durable single-file replacement below the service facade.
package workspacefs

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EncodePathID creates a URL-safe identifier from a workspace-relative path.
func EncodePathID(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(filepath.ToSlash(value)))
}

// DecodePathID decodes a URL-safe workspace path identifier.
func DecodePathID(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// Join resolves a workspace-relative slash path without allowing it to escape
// root.
func Join(root, relPath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if clean == "." || clean == "" {
		return root, nil
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid path: %s", relPath)
	}
	return filepath.Join(root, clean), nil
}

// WriteFileAtomic writes content to a sibling temporary file, syncs it, and
// renames it into place. Callers retain ownership of multi-file transactions;
// this function guarantees only one-file replacement.
func WriteFileAtomic(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".renart-write-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if _, err := temp.Write(content); err != nil {
		cleanup()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}
