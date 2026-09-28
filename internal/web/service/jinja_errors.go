package service

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/bruin-data/bruin/pkg/jinja"
)

// Bruin reports every Jinja parse or evaluation error it does not classify
// itself as a bug in its parser or renderer, and gonja quotes the whole
// template in parse errors. These are mistakes in the user's template, so
// messages shown to users drop the bug claim and the quoted template.
const (
	bruinJinjaParserBugPrefix   = "you have found a bug in the jinja parser, please report it: "
	bruinJinjaRendererBugPrefix = "you have found a bug in the jinja renderer, please report it: "
	gonjaParseErrorPrefix       = "failed to parse template '"
)

// bruinExtractorRenderPrefixes only restate that rendering failed; the
// template error that follows them says why.
var bruinExtractorRenderPrefixes = []string{
	"could not render file while extracting the queries from the whole file: ",
	"could not render file while extracting the queries with the split query extractor: ",
	"could not render file while extracting Oracle script queries: ",
}

// templateErrorMessage returns err's message with Bruin's Jinja error wording
// replaced; other messages are returned unchanged.
func templateErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return describeTemplateError(err.Error())
}

// templateError presents err with templateErrorMessage while keeping it
// available to errors.Is and errors.As.
func templateError(err error) error {
	if err == nil {
		return nil
	}
	return templateRenderError{err: err}
}

type templateRenderError struct{ err error }

func (e templateRenderError) Error() string { return templateErrorMessage(e.err) }

func (e templateRenderError) Unwrap() error { return e.err }

func describeTemplateError(message string) string {
	if prefix, rest, ok := strings.Cut(message, bruinJinjaParserBugPrefix); ok {
		return trimBruinExtractorPrefix(prefix) + "invalid Jinja syntax: " + gonjaParseErrorDetail(rest)
	}
	if prefix, rest, ok := strings.Cut(message, bruinJinjaRendererBugPrefix); ok {
		return trimBruinExtractorPrefix(prefix) + rest
	}
	return message
}

func trimBruinExtractorPrefix(prefix string) string {
	for _, extractorPrefix := range bruinExtractorRenderPrefixes {
		if trimmed, ok := strings.CutSuffix(prefix, extractorPrefix); ok {
			return trimmed
		}
	}
	return prefix
}

// gonjaParseErrorDetail strips the quoted template from gonja's
// "failed to parse template '<source>': <detail>". The detail ends with the
// error position, so the separator is the last "': " before that position.
func gonjaParseErrorDetail(message string) string {
	source, ok := strings.CutPrefix(message, gonjaParseErrorPrefix)
	if !ok {
		return message
	}
	end := len(source)
	if position := strings.LastIndex(source, " (Line: "); position >= 0 {
		end = position
	}
	separator := strings.LastIndex(source[:end], "': ")
	if separator < 0 {
		return message
	}
	return source[separator+len("': "):]
}

var gonjaParsePosition = regexp.MustCompile(`\(Line: (\d+) Col: (\d+)`)

// templateSyntaxErrorPosition returns the 1-based position of a Jinja syntax
// error in source. Bruin prepends macro files before parsing, which shifts the
// positions it reports, so the source is parsed again on its own here.
func templateSyntaxErrorPosition(source string) (line, column int, ok bool) {
	if strings.TrimSpace(source) == "" || !strings.Contains(source, "{") {
		return 0, 0, false
	}
	_, err := jinja.NewRendererWithYesterday("template-position", "template-position").Render(source)
	if err == nil {
		return 0, 0, false
	}
	match := gonjaParsePosition.FindStringSubmatch(err.Error())
	if match == nil {
		return 0, 0, false
	}
	line, _ = strconv.Atoi(match[1])
	column, _ = strconv.Atoi(match[2])
	if line < 1 || column < 1 {
		return 0, 0, false
	}
	return line, column, true
}
