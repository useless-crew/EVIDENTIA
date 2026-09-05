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
	CodeForbidden             = "FORBIDDEN"
	CodeConflict              = "CONFLICT"
	CodeUnprocessableEntity   = "UNPROCESSABLE_ENTITY"
	CodeTooManyRequests       = "TOO_MANY_REQUESTS"
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

// ErrForbidden builds a 403 response: the caller is authenticated but the
// request is denied by RBAC or ABAC (System 4). Like ErrUnauthorized,
// message must always be a single generic string — never the specific
// permission, case, or document relationship that failed, which would
// hand a client a map of the authorization model (master prompt §21/§30).
// Log the specific reason server-side (see internal/authz.Decision.Reason)
// instead.
func ErrForbidden(message string) *AppError {
	return NewAppError(http.StatusForbidden, CodeForbidden, message, nil)
}

// ErrConflict builds a 409 response: the request is well-formed and
// authorized, but conflicts with existing state (e.g. a duplicate
// case_number colliding with cases_case_number_unique). message must be
// safe to show a client — never a raw database constraint name or driver
// error text.
func ErrConflict(message string) *AppError {
	return NewAppError(http.StatusConflict, CodeConflict, message, nil)
}

// ErrUnprocessableEntity builds a 422 response: the request is well-formed
// and authorized, but the operation cannot be safely carried out on THIS
// resource as it stands (e.g. a document whose file type has no safe
// content-redaction implementation). Distinct from ErrBadRequest (a
// malformed request) and ErrConflict (a state collision) — this is neither:
// the request itself is fine, but honoring it would require an
// implementation that does not exist. message must be safe to show a
// client.
func ErrUnprocessableEntity(message string) *AppError {
	return NewAppError(http.StatusUnprocessableEntity, CodeUnprocessableEntity, message, nil)
}

// ErrTooManyRequests builds a 429 response — used by System 13's SSE
// connection-limit guard (internal/sse.Manager.Register's
// ErrTooManyConnections) when a user already holds the maximum number of
// concurrent event streams; never a general-purpose HTTP rate limiter
// (this codebase has none — see docs/BACKGROUND_JOBS.md's own "Rate
// Limiting" finding), only this specific, self-contained protection.
func ErrTooManyRequests(message string) *AppError {
	return NewAppError(http.StatusTooManyRequests, CodeTooManyRequests, message, nil)
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
