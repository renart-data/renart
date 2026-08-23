package notebookdoc

import (
	"net/http"
	"strings"

	"renart/internal/web/apperror"
	"renart/internal/web/presentation"
)

type APIError = apperror.Error

func badRequestError(code, message string) *APIError {
	return apiError(http.StatusBadRequest, code, message)
}

func internalError(code, message string) *APIError {
	return apiError(http.StatusInternalServerError, code, message)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func hasPresentationErrors(findings []presentation.Finding) bool {
	for _, finding := range findings {
		if strings.EqualFold(finding.Severity, "error") || strings.EqualFold(finding.Severity, "fatal") {
			return true
		}
	}
	return false
}
