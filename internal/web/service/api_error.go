package service

import (
	"net/http"

	"renart/internal/web/apperror"
)

// APIError remains the service-facade name for the shared application error
// contract. New backend domains should depend on apperror directly instead of
// importing the broad service package.
type APIError = apperror.Error

func newAPIError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

func badRequestError(code, message string) *APIError {
	return newAPIError(http.StatusBadRequest, code, message)
}

func internalError(code, message string) *APIError {
	return newAPIError(http.StatusInternalServerError, code, message)
}
