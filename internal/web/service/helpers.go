package service

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// SQLBruinHeaderPattern matches SQL Bruin header comments.
	SQLBruinHeaderPattern = regexp.MustCompile(`(?s)\A(\s*/\*\s*@bruin.*?@bruin\s*\*/)(\s*)`)
	// PythonBruinHeaderPattern matches Python Bruin header comments.
	PythonBruinHeaderPattern = regexp.MustCompile(`(?s)\A(\s*(?:"""\s*@bruin.*?@bruin\s*"""|'''\s*@bruin.*?@bruin\s*'''|#\s*@bruin\s*\n.*?\n\s*#\s*@bruin\s*))(\s*)`)
)

// EncodeID creates a URL-safe base64 ID from a path.
func EncodeID(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(filepath.ToSlash(value)))
}

// DecodeID decodes a URL-safe base64 ID back to a path.
func DecodeID(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// SafeJoin safely joins paths, preventing directory traversal attacks.
func SafeJoin(root, relPath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if clean == "." || clean == "" {
		return root, nil
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid path: %s", relPath)
	}
	return filepath.Join(root, clean), nil
}

// Slug converts a string to a URL-safe slug.
func Slug(input string) string {
	trimmed := strings.TrimSpace(strings.ToLower(input))
	if trimmed == "" {
		return "asset"
	}
	b := strings.Builder{}
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if r == '_' || r == '-' || r == ' ' {
			b.WriteRune('-')
		}
	}

	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "asset"
	}
	return result
}

// ExtensionForAssetType returns the appropriate file extension for an asset type.
func ExtensionForAssetType(assetType string) string {
	assetType = strings.ToLower(assetType)
	if strings.Contains(assetType, "python") || strings.HasSuffix(assetType, ".py") {
		return ".py"
	}
	if strings.Contains(assetType, "r") || strings.HasSuffix(assetType, ".r") {
		return ".r"
	}
	if strings.Contains(assetType, "yaml") || strings.Contains(assetType, "yml") {
		return ".yml"
	}
	return ".sql"
}

// InferAssetTypeFromPath infers asset type from file extension.
func InferAssetTypeFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".py":
		return "python"
	case ".r":
		return "r"
	case ".yml", ".yaml":
		return "yaml"
	default:
		return "duckdb.sql"
	}
}

// SplitBruinHeader extracts the Bruin header from file content.
func SplitBruinHeader(content string) (header, separator, body string, found bool) {
	if match := SQLBruinHeaderPattern.FindStringSubmatchIndex(content); match != nil {
		header = content[match[2]:match[3]]
		separator = content[match[4]:match[5]]
		body = content[match[1]:]
		return header, separator, body, true
	}

	if match := PythonBruinHeaderPattern.FindStringSubmatchIndex(content); match != nil {
		header = content[match[2]:match[3]]
		separator = content[match[4]:match[5]]
		body = content[match[1]:]
		return header, separator, body, true
	}

	return "", "", content, false
}

// ExtractExecutableContent extracts the executable portion of asset content.
func ExtractExecutableContent(content string) string {
	_, _, body, found := SplitBruinHeader(content)
	if !found {
		return content
	}
	return body
}

// MergeExecutableContent merges executable content with the existing header.
func MergeExecutableContent(currentFileContent, executableContent string) string {
	header, separator, _, found := SplitBruinHeader(currentFileContent)
	if !found {
		return executableContent
	}

	sep := separator
	if sep == "" {
		sep = "\n\n"
	}

	return header + sep + strings.TrimLeft(executableContent, "\r\n")
}

// DefaultAssetContent generates default content for a new asset.
func DefaultAssetContent(assetName, assetType, assetPath string) string {
	if strings.HasSuffix(strings.ToLower(assetPath), ".py") {
		return `""" @bruin

image: python:3.11
connection: duckdb-default

materialization:
  type: table

@bruin """

import pandas as pd


def materialize():
    items = 100000
    df = pd.DataFrame({
        'col1': range(items),
        'col2': [f'value_new_{i}' for i in range(items)],
        'col3': [i * 6.0 for i in range(items)]
    })

    return df
`
	}

	return fmt.Sprintf("/* @bruin\n\ntype: %s\n\n@bruin */\n", assetType)
}

// NormalizeIdentifier normalizes a database identifier for comparison.
func NormalizeIdentifier(value string) string {
	replacer := strings.NewReplacer("`", "", `"`, "", "[", "", "]", "")
	clean := replacer.Replace(strings.TrimSpace(value))
	return strings.ToLower(clean)
}

// QuoteQualifiedIdentifier quotes a qualified identifier for use in SQL.
func QuoteQualifiedIdentifier(value string) string {
	parts := strings.Split(value, ".")
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(strings.Trim(part, "`\"[]"))
		quoted = append(quoted, `"`+strings.ReplaceAll(trimmed, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, ".")
}

// EscapeSQLLiteral escapes a string for use as a SQL literal.
func EscapeSQLLiteral(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

// SlugUnderscore creates a filesystem-friendly underscore slug.
func SlugUnderscore(input string) string {
	return strings.ReplaceAll(Slug(strings.ReplaceAll(input, ".", " ")), "-", "_")
}

// MaterializationAssetKey creates a unique key for materialization lookup.
func MaterializationAssetKey(assetName, connectionName string) string {
	return NormalizeIdentifier(assetName) + "|" + NormalizeIdentifier(connectionName)
}
