package apperror

import "testing"

func TestErrorReturnsMessage(t *testing.T) {
	err := &Error{Status: 409, Code: "revision_conflict", Message: "the document changed"}
	if got := err.Error(); got != err.Message {
		t.Fatalf("Error() = %q, want %q", got, err.Message)
	}
}

func TestNilErrorReturnsEmptyMessage(t *testing.T) {
	var err *Error
	if got := err.Error(); got != "" {
		t.Fatalf("nil Error() = %q, want empty string", got)
	}
}
