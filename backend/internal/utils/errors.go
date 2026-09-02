// Package utils holds small, dependency-light helpers shared across
// middleware, handlers, and services — request-context accessors, the
// application error type, and (in later systems) validation/pagination
// helpers.
package utils

import (
	"errors"
	"fmt"
	"net/http"
)

// Common, stable error codes returned to API clients. Business-domain codes
// (e.g. CASE_NOT_FOUND) belong to the systems that own those domains.
const (
	CodeInternal              = "INTERNAL_ERROR"
	CodeBadRequest            = "BAD_REQUEST"
	CodeNotFound              = "NOT_FOUND"
	CodeServiceUnavailable    = "SERVICE_UNAVAILABLE"
	CodeMethodNotAllowed      = "METHOD_NOT_ALLOWED"
	CodeRequestEntityTooLarge = "REQUEST_ENTITY_TOO_LARGE"
	CodeUnauthorized          = "UNAUTHORIZED"
)

// AppError is the application-wide error type. Status and Code/Message are
// safe to expose to API clients; Err carries the underlying cause for
// internal logging only and must never be serialized in an HTTP response.
type AppError struct {
	Status  int
	Code    string
	Message string
	Err     error
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// NewAppError builds an AppError. err may be nil when there is no
// underlying cause to wrap (e.g. a validation failure).
func NewAppError(status int, code, message string, err error) *AppError {
	return &AppError{Status: status, Code: code, Message: message, Err: err}
}

func ErrInternal(err error) *AppError {
	return NewAppError(http.StatusInternalServerError, CodeInternal, "An unexpected error occurred", err)
}

func ErrBadRequest(message string) *AppError {
	return NewAppError(http.StatusBadRequest, CodeBadRequest, message, nil)
}

func ErrNotFound(message string) *AppError {
	return NewAppError(http.StatusNotFound, CodeNotFound, message, nil)
}

func ErrServiceUnavailable(message string) *AppError {
	return NewAppError(http.StatusServiceUnavailable, CodeServiceUnavailable, message, nil)
}

// ErrUnauthorized builds a 401 response. message must always be a single
// generic string (e.g. "Invalid email or password", "Authentication
// required") — never a specific reason like "user not found" or "token
// expired", which would leak account existence or validation internals to
// the client (master prompt §8/§30/§46). Log the specific reason
// server-side instead.
func ErrUnauthorized(message string) *AppError {
	return NewAppError(http.StatusUnauthorized, CodeUnauthorized, message, nil)
}

// AsAppError unwraps err looking for an *AppError, so callers that receive a
// wrapped error can still recover the original status/code/message.
func AsAppError(err error) (*AppError, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}
