package service

import (
	"io"
	"os"
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"
	"renart/internal/sqlintelligence"
	"renart/internal/sqllsp"
	webexecution "renart/internal/web/execution"
)

func semanticFileSourceAnchors(uri sqllsp.URI, dialect, canonical string, columns int) *webexecution.SemanticSourceAnchors {
	path, ok := sqllsp.URIToPath(uri)
	if !ok || !strings.HasSuffix(strings.ToLower(path), ".sql") {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const maxBytes = 2 << 20
	source, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(source) > maxBytes {
		return nil
	}
	return semanticSourceAnchors(string(source), dialect, canonical, columns)
}

func semanticSourceAnchors(source, dialect, canonical string, columns int) *webexecution.SemanticSourceAnchors {
	// Never guess locations in rendered templates or a different query. Parsing
	// the original file keeps metadata comments, whitespace and Unicode offsets.
	identity, err := sqlintelligence.QuerySemanticIdentity(source, dialect)
	if err != nil || canonical == "" || identity.CanonicalFingerprint != canonical {
		return nil
	}
	native, err := golyglot.ParseDialect(dialect)
	if err != nil {
		return nil
	}
	parsed, err := golyglot.Parse(source, golyglot.ParseOptions{Dialect: native, Mode: golyglot.Strict})
	if err != nil || len(parsed.Statements) != 1 {
		return nil
	}
	query, ok := parsed.Statements[0].Node.(*golyglot.SelectStmt)
	if !ok {
		return nil
	}
	toRange := func(span golyglot.Span) webexecution.SemanticSourceRange {
		r := parsed.Source.Range(span, golyglot.PositionUTF16)
		return webexecution.SemanticSourceRange{Line: r.Start.Line + 1, Column: r.Start.Character + 1, EndLine: r.End.Line + 1, EndColumn: r.End.Character + 1}
	}
	span := query.SourceSpan()
	if !span.Valid(len(source)) || span.Empty() {
		return nil
	}
	anchors := &webexecution.SemanticSourceAnchors{
		Fingerprint: sqlintelligence.SourceAnchorFingerprint(source), Query: toRange(span),
		Projections: []webexecution.SemanticSourceRange{},
	}
	if query.SetOperator != "" || len(query.Projections) != columns {
		return anchors
	}
	for _, projection := range query.Projections {
		if member, ok := projection.Expr.(*golyglot.FieldExpr); ok && member.Field.Text == "*" && !member.Field.Quoted {
			return anchors
		}
		if _, star := projection.Expr.(*golyglot.StarExpr); star {
			return anchors
		}
		if identifier, ok := projection.Expr.(*golyglot.IdentifierExpr); ok {
			for _, part := range identifier.Parts {
				if part.Text == "*" && !part.Quoted {
					return anchors
				}
			}
		}
		if !projection.Span.Valid(len(source)) || projection.Span.Empty() {
			return anchors
		}
	}
	for _, projection := range query.Projections {
		anchors.Projections = append(anchors.Projections, toRange(projection.Span))
	}
	return anchors
}
