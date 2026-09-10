package sqlintelligence

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// SourceAnchorFingerprint guards editor annotations against stale source text.
// FNV-1a is a content-match hint, NOT a security/integrity or deployment digest.
// Normalize CRLF because Monaco normalizes model line endings. The browser uses
// the same UTF-8 algorithm, including on insecure local-network HTTP origins.
func SourceAnchorFingerprint(source string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ReplaceAll(source, "\r\n", "\n")))
	return fmt.Sprintf("fnv1a64:%016x", h.Sum64())
}
