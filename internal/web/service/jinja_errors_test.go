package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/spf13/afero"
)

func TestTemplateErrorMessageReportsSyntaxErrorsAsUserMistakes(t *testing.T) {
	renderer := jinja.NewRendererWithYesterday("pipeline", "run")
	template := "SELECT 'a': (Line: 1)\nFROM orders\nWHERE ordered_at >= '{{ var. }}'"

	_, renderErr := renderer.Render(template)
	extractor := &query.WholeFileExtractor{Fs: afero.NewMemMapFs(), Renderer: renderer}
	_, extractErr := extractor.ExtractQueriesFromString(template)

	for name, err := range map[string]error{"renderer": renderErr, "extractor": extractErr} {
		got := templateErrorMessage(err)
		if !strings.HasPrefix(got, "invalid Jinja syntax: expected name or integer (Line: 3 ") {
			t.Fatalf("%s: message = %q", name, got)
		}
		for _, hidden := range []string{"bug", "report", "FROM orders", "could not render file"} {
			if strings.Contains(got, hidden) {
				t.Fatalf("%s: message %q contains %q", name, got, hidden)
			}
		}
	}
}

func TestTemplateErrorMessageDropsRendererBugClaim(t *testing.T) {
	renderer := jinja.NewRendererWithYesterday("pipeline", "run")
	_, err := renderer.Render("SELECT {{ var.region }}")

	got := templateErrorMessage(err)
	if !strings.HasPrefix(got, "unable to execute template: ") || strings.Contains(got, "bug") {
		t.Fatalf("message = %q", got)
	}
}

func TestTemplateErrorMessageKeepsOtherMessagesAndContext(t *testing.T) {
	renderer := jinja.NewRendererWithYesterday("pipeline", "run")
	_, classified := renderer.Render("SELECT {% if true %} 1")
	_, syntax := renderer.Render("SELECT {{ }}")

	if got, want := templateErrorMessage(classified), classified.Error(); got != want {
		t.Fatalf("classified message = %q, want %q", got, want)
	}
	if got := templateErrorMessage(errors.New("connection refused")); got != "connection refused" {
		t.Fatalf("unrelated message = %q", got)
	}
	got := templateErrorMessage(fmt.Errorf("render custom check 'positive': %w", syntax))
	if !strings.HasPrefix(got, "render custom check 'positive': invalid Jinja syntax: ") {
		t.Fatalf("wrapped message = %q", got)
	}
	if templateErrorMessage(nil) != "" {
		t.Fatal("nil error should have an empty message")
	}
}

func TestTemplateErrorKeepsCauseAndCleansMessage(t *testing.T) {
	renderer := jinja.NewRendererWithYesterday("pipeline", "run")
	_, cause := renderer.Render("SELECT {{ var. }}")

	err := templateError(cause)
	if !errors.Is(err, cause) {
		t.Fatal("templateError should keep the original error in the chain")
	}
	if !strings.HasPrefix(err.Error(), "invalid Jinja syntax: ") {
		t.Fatalf("message = %q", err.Error())
	}
	if templateError(nil) != nil {
		t.Fatal("nil error should stay nil")
	}
}

func TestTemplateRenderFindingPointsAtTheSyntaxErrorInTheSource(t *testing.T) {
	source := "SELECT order_date\nFROM orders\nWHERE order_date >= '{{ var. }}'"
	macros := "{% macro cents(x) %}{{ x }} * 100{% endmacro %}\n{% macro unused() %}{% endmacro %}\n"
	now := time.Now().UTC()
	renderer := jinja.NewRendererWithStartEndDatesAndMacros(&now, &now, &now, "pipeline", "run", nil, macros)
	_, err := renderer.Render(source)

	finding := templateRenderFinding(err, source)
	if finding.Line != 3 || finding.Column < 1 || finding.SourceFingerprint == "" {
		t.Fatalf("finding position = line %d column %d fingerprint %q", finding.Line, finding.Column, finding.SourceFingerprint)
	}

	withoutSource := templateRenderFinding(err, "")
	if withoutSource.Line != 0 || withoutSource.SourceFingerprint != "" {
		t.Fatalf("finding without source has a position: %+v", withoutSource)
	}

	_, evaluation := renderer.Render("SELECT {{ var.region }}")
	if got := templateRenderFinding(evaluation, "SELECT {{ var.region }}"); got.Line != 0 {
		t.Fatalf("evaluation error has a syntax position: line %d", got.Line)
	}
}
