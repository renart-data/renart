package sqlintelligence

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"
)

const querySemanticIdentityVersion = "v1"

// QuerySemanticIdentityResult separates byte identity from parsed query
// identity. Presentation comments and formatting affect SourceFingerprint but
// not CanonicalFingerprint. Executable directive comments affect both.
type QuerySemanticIdentityResult struct {
	SourceFingerprint    string
	CanonicalFingerprint string
}

// QuerySemanticIdentity returns stable, versioned fingerprints for one SQL
// statement. It is deliberately strict: callers must surface an incomplete
// semantic analysis instead of treating unparsed SQL as equivalent.
func QuerySemanticIdentity(sql, dialect string) (QuerySemanticIdentityResult, error) {
	result := QuerySemanticIdentityResult{SourceFingerprint: semanticIdentityHash(sql)}
	nativeDialect, err := golyglot.ParseDialect(normalizeAnalyzeDialect(dialect))
	if err != nil {
		return result, err
	}
	commentFree, directives, err := semanticIdentitySource(sql, nativeDialect)
	if err != nil {
		return result, err
	}
	statements, err := golyglot.Transpile(commentFree, nativeDialect, nativeDialect)
	if err != nil {
		return result, err
	}
	if len(statements) != 1 {
		return result, fmt.Errorf("semantic identity expects exactly one statement, found %d", len(statements))
	}
	result.CanonicalFingerprint = semanticIdentityHash(statements[0], strings.Join(directives, "\x00"))
	return result, nil
}

func semanticIdentitySource(sql string, dialect golyglot.Dialect) (string, []string, error) {
	parsed, err := golyglot.Parse(sql, golyglot.ParseOptions{Dialect: dialect, Mode: golyglot.Strict})
	if err != nil {
		return "", nil, err
	}
	var builder strings.Builder
	last := 0
	tokenPosition := 0
	var directives []string
	for _, token := range parsed.Tokens {
		if token.Kind != golyglot.TokenComment {
			if token.Kind != golyglot.TokenEOF {
				tokenPosition++
			}
			continue
		}
		if !token.Span.Valid(len(sql)) || token.Span.Start < last {
			return "", nil, fmt.Errorf("comment has invalid source span %d:%d", token.Span.Start, token.Span.End)
		}
		builder.WriteString(sql[last:token.Span.Start])
		builder.WriteByte(' ')
		last = token.Span.End
		if semanticIdentityDirective(token.Text) {
			directives = append(directives, fmt.Sprintf("%d:%s", tokenPosition, strings.TrimSpace(token.Text)))
		}
	}
	if last == 0 {
		return sql, directives, nil
	}
	builder.WriteString(sql[last:])
	return builder.String(), directives, nil
}

func semanticIdentityDirective(comment string) bool {
	comment = strings.TrimSpace(comment)
	return strings.HasPrefix(comment, "/*+") ||
		strings.HasPrefix(comment, "/*!") ||
		strings.HasPrefix(comment, "--+") ||
		strings.HasPrefix(comment, "//+") ||
		strings.HasPrefix(comment, "#+")
}

func semanticIdentityHash(values ...string) string {
	hasher := sha256.New()
	for _, value := range append([]string{querySemanticIdentityVersion}, values...) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hasher.Write(size[:])
		_, _ = hasher.Write([]byte(value))
	}
	return querySemanticIdentityVersion + ":" + hex.EncodeToString(hasher.Sum(nil))
}
