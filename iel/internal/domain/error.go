package domain

import (
	"fmt"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"
)

// Error is a refused or failed intent operation with an API error code (api/openapi.yaml
// ErrorCode). The northbound interface maps the code to an HTTP status.
type Error struct {
	Code    apitypes.ErrorCode
	Message string
	Details map[string]any
	// Rollbacks are the LKG rollbacks a failed provisioning caused, one per affected cell.
	Rollbacks []apitypes.RollbackInfo
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Fail returns an Error with a formatted message.
func Fail(code apitypes.ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// NotFound is a RESOURCE_NOT_FOUND error for what.
func NotFound(what string) *Error {
	return Fail(apitypes.ErrorCodeRESOURCENOTFOUND, "%s not found", what)
}

// Internal is an INTERNAL_ERROR for an unexpected failure.
func Internal(err error) *Error {
	return &Error{Code: apitypes.ErrorCodeINTERNALERROR, Message: err.Error()}
}
