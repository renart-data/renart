package policy

import (
	"fmt"
	"net/http"

	"renart/internal/web/apperror"
)

type AccessMode string

const (
	ReadOnly  AccessMode = "read_only"
	ReadWrite AccessMode = "read_write"
)

type ConnectionPolicy struct {
	AccessMode AccessMode `yaml:"access_mode" json:"access_mode"`
}

// Effect describes operations, not credential purposes or coordination locks.
type Effect string

const (
	Read    Effect = "read"
	Write   Effect = "write"
	Unknown Effect = "unknown"
)

type Requirement struct {
	Connection string `json:"connection"`
	Effect     Effect `json:"effect"`
	Operation  string `json:"operation"`
}

func EffectiveMode(p EnvironmentPolicy, connection string, nativeReadOnly bool) AccessMode {
	if nativeReadOnly || p.Connections[connection].AccessMode == ReadOnly {
		return ReadOnly
	}
	return ReadWrite
}

func InvalidError(reason string) error {
	return &apperror.Error{Status: http.StatusConflict, Code: "connection_policy_invalid", Message: "Connection policy is invalid: " + reason}
}

func CheckAccess(p EnvironmentPolicy, environment string, req Requirement, nativeReadOnly bool) error {
	if p.Invalid != "" {
		return InvalidError(p.Invalid)
	}
	if EffectiveMode(p, req.Connection, nativeReadOnly) != ReadOnly || req.Effect == Read {
		return nil
	}
	code := "connection_read_only"
	if req.Effect != Write {
		code = "connection_access_unknown"
	}
	return &apperror.Error{Status: http.StatusConflict, Code: code, Message: fmt.Sprintf("Connection %q in environment %q is read-only: %s requires %s access. Use a writable destination or change the connection's access mode.", req.Connection, environment, req.Operation, req.Effect)}
}
