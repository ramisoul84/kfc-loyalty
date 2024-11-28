package domain

import (
	"errors"
	"fmt"
	"net/http"
)

// ═══════════════════════════════════════════════════════════════════
// ERROR TYPE
// ═══════════════════════════════════════════════════════════════════

// ErrorType categorizes an error.
type ErrorType string

const (
	ErrorTypeValidation     ErrorType = "validation"
	ErrorTypeAuthentication ErrorType = "authentication"
	ErrorTypeAuthorization  ErrorType = "authorization"
	ErrorTypeNotFound       ErrorType = "not_found"
	ErrorTypeConflict       ErrorType = "conflict"
	ErrorTypeInternal       ErrorType = "internal"
)

// HTTPStatus maps an error type to its HTTP status code.
func (t ErrorType) HTTPStatus() int {
	switch t {
	case ErrorTypeValidation:
		return http.StatusBadRequest
	case ErrorTypeAuthentication:
		return http.StatusUnauthorized
	case ErrorTypeAuthorization:
		return http.StatusForbidden
	case ErrorTypeNotFound:
		return http.StatusNotFound
	case ErrorTypeConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// ═══════════════════════════════════════════════════════════════════
// APP ERROR
// ═══════════════════════════════════════════════════════════════════

// AppError is an application-level error with a type, a message,
// an HTTP status code, and optional details.
type AppError struct {
	Type       ErrorType   `json:"type"`
	Message    string      `json:"message"`
	StatusCode int         `json:"-"`
	Details    interface{} `json:"details,omitempty"`
	Err        error       `json:"-"`
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s", e.Message, e.Err.Error())
	}
	return e.Message
}

// Unwrap returns the wrapped error, if any.
func (e *AppError) Unwrap() error {
	return e.Err
}

// WithDetails attaches structured details to the error.
func (e *AppError) WithDetails(details interface{}) *AppError {
	e.Details = details
	return e
}

// WithError wraps an underlying error.
func (e *AppError) WithError(err error) *AppError {
	e.Err = err
	return e
}

// ═══════════════════════════════════════════════════════════════════
// CONSTRUCTORS
// ═══════════════════════════════════════════════════════════════════

// NewValidationError creates a 400 validation error.
func NewValidationError(message string) *AppError {
	return &AppError{
		Type:       ErrorTypeValidation,
		Message:    message,
		StatusCode: http.StatusBadRequest,
	}
}

// NewAuthenticationError creates a 401 authentication error.
func NewAuthenticationError(message string) *AppError {
	return &AppError{
		Type:       ErrorTypeAuthentication,
		Message:    message,
		StatusCode: http.StatusUnauthorized,
	}
}

// NewAuthorizationError creates a 403 authorization error.
func NewAuthorizationError(message string) *AppError {
	return &AppError{
		Type:       ErrorTypeAuthorization,
		Message:    message,
		StatusCode: http.StatusForbidden,
	}
}

// NewNotFoundError creates a 404 not-found error.
func NewNotFoundError(message string) *AppError {
	return &AppError{
		Type:       ErrorTypeNotFound,
		Message:    message,
		StatusCode: http.StatusNotFound,
	}
}

// NewConflictError creates a 409 conflict error.
func NewConflictError(message string) *AppError {
	return &AppError{
		Type:       ErrorTypeConflict,
		Message:    message,
		StatusCode: http.StatusConflict,
	}
}

// NewInternalError creates a 500 internal error.
// The message is intentionally generic to avoid leaking internals.
func NewInternalError(err error) *AppError {
	return &AppError{
		Type:       ErrorTypeInternal,
		Message:    "internal server error",
		StatusCode: http.StatusInternalServerError,
		Err:        err,
	}
}

// ═══════════════════════════════════════════════════════════════════
// HELPERS
// ═══════════════════════════════════════════════════════════════════

// AsAppError converts any error to *AppError, if possible.
func AsAppError(err error) (*AppError, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}

// IsAppError reports whether the error is an *AppError.
func IsAppError(err error) bool {
	var appErr *AppError
	return errors.As(err, &appErr)
}

// GetStatusCode returns the HTTP status code for any error.
func GetStatusCode(err error) int {
	if appErr, ok := AsAppError(err); ok {
		return appErr.StatusCode
	}
	return http.StatusInternalServerError
}

// IsErrorType reports whether the error is of the given type.
func IsErrorType(err error, errType ErrorType) bool {
	appErr, ok := AsAppError(err)
	if !ok {
		return false
	}
	return appErr.Type == errType
}

// Type-specific checks — handy in handlers and tests.

func IsValidationError(err error) bool     { return IsErrorType(err, ErrorTypeValidation) }
func IsAuthenticationError(err error) bool { return IsErrorType(err, ErrorTypeAuthentication) }
func IsAuthorizationError(err error) bool  { return IsErrorType(err, ErrorTypeAuthorization) }
func IsNotFoundError(err error) bool       { return IsErrorType(err, ErrorTypeNotFound) }
func IsConflictError(err error) bool       { return IsErrorType(err, ErrorTypeConflict) }
func IsInternalError(err error) bool       { return IsErrorType(err, ErrorTypeInternal) }
