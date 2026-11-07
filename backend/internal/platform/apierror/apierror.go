// Package apierror defines the uniform API error format used by every HTTP
// endpoint:
//
//	{"error": {"code": "VALIDATION_ERROR", "message": "...", "request_id": "..."}}
//
// Errors carry their HTTP status, a stable machine-readable code and optional
// field-level details. Internal causes are logged but never leaked to clients.
package apierror

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable error identifier.
type Code string

// Error codes exposed by the public API.
const (
	CodeBadRequest       Code = "BAD_REQUEST"
	CodeValidation       Code = "VALIDATION_ERROR"
	CodeUnauthorized     Code = "UNAUTHORIZED"
	CodeForbidden        Code = "FORBIDDEN"
	CodeNotFound         Code = "NOT_FOUND"
	CodeConflict         Code = "CONFLICT"
	CodeRateLimited      Code = "RATE_LIMITED"
	CodePlanLimit        Code = "PLAN_LIMIT_EXCEEDED"
	CodeUnsupported      Code = "UNSUPPORTED"
	CodeUnavailable      Code = "SERVICE_UNAVAILABLE"
	CodeUpstream         Code = "UPSTREAM_ERROR"
	CodePayloadTooLarge  Code = "PAYLOAD_TOO_LARGE"
	CodeInternal         Code = "INTERNAL_ERROR"
	CodeNotImplemented   Code = "NOT_IMPLEMENTED"
	CodeInvariantFailure Code = "INVARIANT_VIOLATION"
)

// statusByCode maps a code to its default HTTP status.
var statusByCode = map[Code]int{
	CodeBadRequest:       http.StatusBadRequest,
	CodeValidation:       http.StatusUnprocessableEntity,
	CodeUnauthorized:     http.StatusUnauthorized,
	CodeForbidden:        http.StatusForbidden,
	CodeNotFound:         http.StatusNotFound,
	CodeConflict:         http.StatusConflict,
	CodeRateLimited:      http.StatusTooManyRequests,
	CodePlanLimit:        http.StatusPaymentRequired,
	CodeUnsupported:      http.StatusBadRequest,
	CodeUnavailable:      http.StatusServiceUnavailable,
	CodeUpstream:         http.StatusBadGateway,
	CodePayloadTooLarge:  http.StatusRequestEntityTooLarge,
	CodeInternal:         http.StatusInternalServerError,
	CodeNotImplemented:   http.StatusNotImplemented,
	CodeInvariantFailure: http.StatusInternalServerError,
}

// FieldError describes a single invalid input field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error is the API-level error type. It is safe to hand to clients.
type Error struct {
	Code    Code         `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
	Status  int          `json:"-"`
	// cause is logged server-side and never serialised.
	cause error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the wrapped cause for errors.Is/As.
func (e *Error) Unwrap() error { return e.cause }

// WithCause attaches an internal cause (logged, never returned).
func (e *Error) WithCause(err error) *Error {
	clone := *e
	clone.cause = err
	return &clone
}

// Cause returns the wrapped internal error.
func (e *Error) Cause() error { return e.cause }

// New builds an error with the default status for its code.
func New(code Code, message string) *Error {
	status, ok := statusByCode[code]
	if !ok {
		status = http.StatusInternalServerError
	}
	return &Error{Code: code, Message: message, Status: status}
}

// Newf builds a formatted error.
func Newf(code Code, format string, args ...any) *Error {
	return New(code, fmt.Sprintf(format, args...))
}

// Wrap converts an internal error into an API error, keeping the cause for logs.
func Wrap(code Code, message string, cause error) *Error {
	return New(code, message).WithCause(cause)
}

// Validation creates a VALIDATION_ERROR with field details.
func Validation(details ...FieldError) *Error {
	e := New(CodeValidation, "Проверьте корректность входных данных")
	e.Details = details
	return e
}

// Field builds a FieldError.
func Field(field, message string) FieldError {
	return FieldError{Field: field, Message: message}
}

// NotFound builds a NOT_FOUND error for a resource.
func NotFound(resource string) *Error {
	return Newf(CodeNotFound, "%s не найден", resource)
}

// Forbidden builds a FORBIDDEN error.
func Forbidden(message string) *Error {
	if message == "" {
		message = "Недостаточно прав для выполнения операции"
	}
	return New(CodeForbidden, message)
}

// Unauthorized builds an UNAUTHORIZED error.
func Unauthorized(message string) *Error {
	if message == "" {
		message = "Требуется авторизация"
	}
	return New(CodeUnauthorized, message)
}

// Internal builds a generic INTERNAL_ERROR.
func Internal(cause error) *Error {
	return Wrap(CodeInternal, "Внутренняя ошибка сервера", cause)
}

// As extracts an *Error from err.
func As(err error) (*Error, bool) {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// From converts any error into an *Error, defaulting to INTERNAL_ERROR.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	if apiErr, ok := As(err); ok {
		return apiErr
	}
	return Internal(err)
}

// StatusOf returns the HTTP status for err.
func StatusOf(err error) int {
	if apiErr, ok := As(err); ok && apiErr.Status != 0 {
		return apiErr.Status
	}
	return http.StatusInternalServerError
}
