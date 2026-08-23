// Package apperror owns the application error contract shared by backend
// domains and their transport adapters.
package apperror

// Error is the single structured error shape returned by application services.
// Status is the HTTP status code selected by the application boundary, Code is
// a stable machine-readable identifier, and Message is safe user-facing text.
//
// Keeping the contract below the broad service facade lets extracted domains
// report failures without depending back on that facade or on HTTP handlers.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}
