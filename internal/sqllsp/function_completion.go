package sqllsp

import (
	"fmt"
	"strings"
	"sync"

	"github.com/renart-data/golyglot/pkg/golyglot"
)

// Engine observations are immutable here. Cache the public API's defensive copy
// and formatted completion text once per dialect, not once per keystroke or
// once per workspace connection. Empty-prefix suggestions include hundreds of
// functions; their signatures/documentation are identical for every request.
var completionFunctionCatalogs sync.Map

type functionCompletionCandidate struct {
	name string // normalized once for prefix filtering
	kind golyglot.FunctionKind
	item CompletionItem
}

type functionCompletionCatalog struct {
	catalog    golyglot.BuiltinFunctionCatalog
	candidates []functionCompletionCandidate
}

func functionCatalog(dialectName string) golyglot.BuiltinFunctionCatalog {
	return cachedFunctionCompletions(dialectName).catalog
}

func cachedFunctionCompletions(dialectName string) functionCompletionCatalog {
	dialect, err := golyglot.ParseDialect(dialectName)
	if err != nil {
		return functionCompletionCatalog{}
	}
	if cached, ok := completionFunctionCatalogs.Load(dialect); ok {
		return cached.(func() functionCompletionCatalog)()
	}
	load := sync.OnceValue(func() functionCompletionCatalog {
		catalog, _ := golyglot.BuiltinFunctionCatalogForDialect(dialect)
		result := functionCompletionCatalog{catalog: catalog}
		for _, fn := range catalog.Functions {
			if !callableIdentifier(fn.Name) {
				continue
			}
			detail := string(fn.Kind) + " function"
			if len(fn.Signatures) > 0 {
				detail = builtinSignatureLabel(fn.Name, fn.Signatures[0])
				if len(fn.Signatures) > 1 {
					detail += fmt.Sprintf(" (+%d overloads)", len(fn.Signatures)-1)
				}
			}
			name := strings.ToLower(fn.Name)
			result.candidates = append(result.candidates, functionCompletionCandidate{
				name: name, kind: fn.Kind,
				item: CompletionItem{Label: fn.Name, Kind: completionKindFunction, InsertText: fn.Name,
					Detail: detail, Documentation: builtinFunctionDocumentation(catalog, fn), SortText: "2-" + name},
			})
		}
		return result
	})
	actual, _ := completionFunctionCatalogs.LoadOrStore(dialect, load)
	return actual.(func() functionCompletionCatalog)()
}

func (e *Engine) functionCompletions(doc TextDocumentItem, offset int, context golyglot.SyntacticContext, table bool) []CompletionItem {
	if offsetInQuotedIdentifier(doc.Text, offset) {
		return nil
	}
	prefix := strings.ToLower(context.Prefix)
	// Functions are additive, including at an empty argument/projection. Ranking
	// keeps columns first; loading schema information must not hide functions.
	catalog := cachedFunctionCompletions(e.dialectForDocument(doc))
	var items []CompletionItem
	for _, candidate := range catalog.candidates {
		if !strings.HasPrefix(candidate.name, prefix) {
			continue
		}
		if table != (candidate.kind == golyglot.FunctionTable) {
			continue
		}
		if !table {
			if candidate.kind == golyglot.FunctionAggregate && !aggregateCompletionContext(context.Kind) {
				continue
			}
			if candidate.kind == golyglot.FunctionWindow && !windowCompletionContext(context.Kind) {
				continue
			}
			if context.Kind == golyglot.ContextUpdate && contextExpects(context, golyglot.ExpectedIdentifier) {
				continue
			}
		}
		items = append(items, candidate.item)
	}
	return items
}

func callableIdentifier(name string) bool {
	if name == "" || !isWordStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isIdentByte(name[i]) {
			return false
		}
	}
	return true
}

func aggregateCompletionContext(kind golyglot.SyntacticContextKind) bool {
	return kind == golyglot.ContextSelectList || kind == golyglot.ContextHaving || kind == golyglot.ContextOrderBy || kind == golyglot.ContextQualify
}

func windowCompletionContext(kind golyglot.SyntacticContextKind) bool {
	return kind == golyglot.ContextSelectList || kind == golyglot.ContextOrderBy || kind == golyglot.ContextQualify
}

func builtinParameterLabel(parameter golyglot.BuiltinParameter) string {
	label := parameter.Name
	if parameter.Named {
		label += " :="
	}
	if parameter.Type != "" {
		label += " " + parameter.Type
	}
	if parameter.Optional {
		label = "[" + label + "]"
	}
	return label
}

func builtinSignatureLabel(name string, sig golyglot.BuiltinSignature) string {
	if sig.Syntax != "" {
		return sig.Syntax
	}
	labels := make([]string, 0, len(sig.Parameters)+1)
	for _, parameter := range sig.Parameters {
		labels = append(labels, builtinParameterLabel(parameter))
	}
	if sig.VariadicType != "" {
		labels = append(labels, "… "+sig.VariadicType)
	}
	label := name + "(" + strings.Join(labels, ", ") + ")"
	if sig.ReturnType != "" {
		label += " → " + sig.ReturnType
	}
	return label
}

func builtinFunctionDocumentation(catalog golyglot.BuiltinFunctionCatalog, fn golyglot.BuiltinFunction) string {
	var b strings.Builder
	if fn.Description != "" {
		b.WriteString(fn.Description + "\n\n")
	}
	for i, sig := range fn.Signatures {
		if i == 3 {
			break
		}
		fmt.Fprintf(&b, "`%s`\n\n", builtinSignatureLabel(fn.Name, sig))
	}
	fmt.Fprintf(&b, "%s · %s %s builtin catalog.\n\nCatalog signatures describe available overloads, not the inferred type of this expression.", fn.Kind, catalog.Dialect, catalog.EngineVersion)
	if fn.Kind == golyglot.FunctionTable {
		b.WriteString(" The output schema may depend on arguments or files.")
	}
	if fn.Source != "" {
		fmt.Fprintf(&b, "\n\n[SQL expression reference](%s)", fn.Source)
	}
	return b.String()
}
