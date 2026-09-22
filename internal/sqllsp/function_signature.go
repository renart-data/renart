package sqllsp

import (
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"
)

type builtinCall struct {
	name              string
	start, argument   int
	qualified, quoted bool
	argumentTokens    int
	argumentCandidate string
	argumentName      string
}

func (e *Engine) builtinSignatureHelp(doc TextDocumentItem, offset int) *SignatureHelp {
	dialectName := e.dialectForDocument(doc)
	dialect, err := golyglot.ParseDialect(dialectName)
	if err != nil {
		return nil
	}
	tokens, _, err := golyglot.Tokenize(doc.Text[:min(max(offset, 0), len(doc.Text))], dialect)
	if err != nil {
		return nil
	}
	var stack []builtinCall
	var previous, beforePrevious golyglot.Token
	for _, token := range tokens {
		if token.Kind == golyglot.TokenEOF {
			continue
		}
		if token.Kind == golyglot.TokenComment || token.Kind == golyglot.TokenUnterminatedComment {
			if token.Span.End == offset && (token.Kind == golyglot.TokenUnterminatedComment || !strings.HasSuffix(token.Text, "*/")) {
				return nil
			}
			continue
		}
		if len(stack) > 0 {
			call := &stack[len(stack)-1]
			if call.argumentTokens == 0 && (token.Kind == golyglot.TokenIdentifier || token.Kind == golyglot.TokenKeyword || token.Kind == golyglot.TokenQuotedIdentifier) {
				call.argumentCandidate = token.Text
				if token.Kind == golyglot.TokenQuotedIdentifier {
					call.argumentCandidate, _ = readIdentifier(token.Text, 0)
				}
			}
			if call.argumentTokens == 1 && (token.Text == "=" || token.Text == ":=" || token.Text == "=>") {
				call.argumentName = call.argumentCandidate
			}
			call.argumentTokens++
		}
		if token.Kind == golyglot.TokenPunctuation {
			switch token.Text {
			case "(", "[", "{":
				call := builtinCall{}
				if token.Text == "(" && (previous.Kind == golyglot.TokenIdentifier || previous.Kind == golyglot.TokenKeyword || previous.Kind == golyglot.TokenQuotedIdentifier) {
					call.name, call.start = previous.Text, previous.Span.Start
					call.qualified = beforePrevious.Text == "."
					call.quoted = previous.Kind == golyglot.TokenQuotedIdentifier
					if call.quoted {
						call.name, _ = readIdentifier(previous.Text, 0)
					}
				}
				stack = append(stack, call)
			case ")", "]", "}":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			case ",":
				if len(stack) > 0 {
					call := &stack[len(stack)-1]
					call.argument++
					call.argumentTokens = 0
					call.argumentCandidate, call.argumentName = "", ""
				}
			case ";":
				stack = nil
			}
		}
		beforePrevious, previous = previous, token
	}
	for i := len(stack) - 1; i >= 0; i-- {
		call := stack[i]
		if call.name == "" {
			continue
		}
		if call.qualified {
			return nil
		} // no assumption about UDFs in another schema
		catalog := functionCatalog(dialectName)
		context, hasContext := syntacticCompletionContext(doc.Text, call.start, dialectName)
		table := hasContext && contextExpects(context, golyglot.ExpectedTable)
		for _, fn := range catalog.Functions {
			matches := strings.EqualFold(fn.Name, call.name)
			if fn.CaseSensitive || call.quoted {
				matches = fn.Name == call.name
			}
			if !matches || table != (fn.Kind == golyglot.FunctionTable) {
				continue
			}
			if dialect != golyglot.DialectDuckDB || fn.Kind != golyglot.FunctionTable {
				call.argumentName = "" // '=' in ordinary expressions is comparison
			}
			help := &SignatureHelp{ActiveParameter: call.argument}
			selected := false
			documentation := builtinFunctionDocumentation(catalog, fn)
			for _, sig := range fn.Signatures {
				active, accepts := builtinActiveParameter(sig, call)
				if call.argumentName != "" && !accepts {
					continue
				}
				parameters := make([]SignatureParameter, 0, len(sig.Parameters)+1)
				for _, parameter := range sig.Parameters {
					parameters = append(parameters, SignatureParameter{Label: builtinParameterLabel(parameter)})
				}
				if sig.VariadicType != "" {
					parameters = append(parameters, SignatureParameter{Label: "… " + sig.VariadicType})
				}
				if len(parameters) > 0 {
					active = min(active, len(parameters)-1)
				} else {
					active = 0
				}
				if accepts && !selected {
					help.ActiveSignature, selected = len(help.Signatures), true
				}
				help.Signatures = append(help.Signatures, SignatureInformation{Label: builtinSignatureLabel(fn.Name, sig), Documentation: documentation, Parameters: parameters, ActiveParameter: active})
			}
			if len(help.Signatures) == 0 {
				return nil
			}
			help.ActiveParameter = help.Signatures[help.ActiveSignature].ActiveParameter
			return help
		}
		return nil
	}
	return nil
}

func builtinActiveParameter(sig golyglot.BuiltinSignature, call builtinCall) (int, bool) {
	if call.argumentName != "" {
		for i, parameter := range sig.Parameters {
			if parameter.Named && strings.EqualFold(parameter.Name, call.argumentName) {
				return i, true
			}
		}
		return 0, false
	}
	return call.argument, len(sig.Parameters) > call.argument || sig.VariadicType != ""
}
