package service

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/afero"

	webmodel "renart/internal/web/model"
	"renart/internal/web/presentation"
)

// AppendPresentationTypeChecks adds strict dashboard/report findings only for
// artifacts that consume an asset from pipelineID. This keeps a broken,
// unrelated presentation from blocking every pipeline deployment while still
// making a producer schema change fail before its consumers are deployed.
func AppendPresentationTypeChecks(
	ctx context.Context,
	fs afero.Fs,
	workspaceRoot string,
	pipelineID string,
	state webmodel.WorkspaceState,
	report *TypeCheckReport,
) {
	if report == nil || strings.TrimSpace(workspaceRoot) == "" || strings.TrimSpace(pipelineID) == "" {
		return
	}
	target, ok := presentationTargetPipeline(state, pipelineID)
	if !ok {
		return
	}
	paths, err := presentation.DiscoverArtifacts(fs, workspaceRoot)
	if err != nil {
		return
	}
	for _, path := range paths {
		artifact, loadErr := presentation.LoadArtifact(fs, path)
		if loadErr != nil || !presentationConsumesPipeline(*artifact, target) {
			continue
		}
		datasetSchemas, resolutionFindings := resolvePresentationDatasetSchemas(ctx, workspaceRoot, state, artifact)
		findings := append(
			(presentation.Checker{}).CheckArtifact(
				ctx,
				*artifact,
				datasetSchemas,
				presentation.CheckOptions{Strict: true},
			),
			resolutionFindings...,
		)
		findings = uniquePresentationFindings(findings)
		entry := TypeCheckPresentation{
			ID:          artifact.ID,
			WorkspaceID: EncodeID(relativePresentationPath(workspaceRoot, artifact.Path)),
			Kind:        string(artifact.Kind),
			Title:       artifact.Title,
			Path:        relativePresentationPath(workspaceRoot, artifact.Path),
			Status:      typeCheckStatusOK,
			Findings:    make([]TypeCheckPresentationFinding, 0, len(findings)),
		}
		for _, finding := range findings {
			entry.Findings = append(entry.Findings, TypeCheckPresentationFinding{
				Code: finding.Code, Severity: finding.Severity, Message: finding.Message,
				Path: finding.Path, Field: finding.Field, PhysicalType: finding.PhysicalType,
			})
			switch finding.Severity {
			case typeCheckSeverityError:
				report.Summary.Errors++
				entry.Status = typeCheckStatusError
			case typeCheckSeverityWarning:
				report.Summary.Warnings++
				if entry.Status != typeCheckStatusError {
					entry.Status = typeCheckStatusWarning
				}
			}
		}
		report.Presentations = append(report.Presentations, entry)
		report.Summary.Presentations++
	}
	sort.Slice(report.Presentations, func(i, j int) bool {
		return report.Presentations[i].Path < report.Presentations[j].Path
	})
	updateTypeCheckReportStatus(report)
}

func presentationTargetPipeline(state webmodel.WorkspaceState, pipelineID string) (webmodel.Pipeline, bool) {
	for _, candidate := range state.Pipelines {
		if candidate.ID == pipelineID {
			return candidate, true
		}
	}
	return webmodel.Pipeline{}, false
}

func presentationConsumesPipeline(artifact presentation.Artifact, target webmodel.Pipeline) bool {
	for _, dataset := range artifact.Datasets {
		reference := strings.TrimSpace(dataset.Asset)
		if reference == "" {
			continue
		}
		for _, asset := range target.Assets {
			if strings.EqualFold(strings.TrimSpace(asset.Name), reference) ||
				(strings.TrimSpace(asset.URI) != "" && strings.EqualFold(strings.TrimSpace(asset.URI), reference)) {
				return true
			}
		}
	}
	return false
}

func relativePresentationPath(workspaceRoot, path string) string {
	relPath, err := filepath.Rel(workspaceRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relPath)
}
