// Package response defines Evidentia's standard JSON response envelope so
// every handler — in this system and later ones — returns a consistent
// shape to API clients.
package response

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/utils"
)

// Envelope is the top-level shape of every API response.
type Envelope struct {
	Success bool       `json:"success"`
	Data    any        `json:"data,omitempty"`
	Error   *ErrorBody `json:"error,omitempty"`
}

// ErrorBody carries a stable machine-readable code, a human-readable
// message safe to show to a client, and the request ID for correlating a
// client-reported problem with server-side logs. It must never carry a
// stack trace, SQL text, internal file paths, or credentials.
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// Success writes a successful envelope with the given HTTP status and data
// payload. data may be nil for responses with no body content.
func Success(c *gin.Context, status int, data any) {
	c.JSON(status, Envelope{Success: true, Data: data})
}

// Error writes a failure envelope with the given HTTP status, stable error
// code, and public-safe message.
func Error(c *gin.Context, status int, code, message string) {
	c.JSON(status, Envelope{
		Success: false,
		Error: &ErrorBody{
			Code:      code,
			Message:   message,
			RequestID: utils.GetRequestID(c),
		},
	})
}

// FromAppError writes a failure envelope from an *utils.AppError, using its
// status/code/message. The wrapped internal error (AppError.Err) is never
// serialized — callers are expected to have already logged it.
func FromAppError(c *gin.Context, err *utils.AppError) {
	if err.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(int((err.RetryAfter+time.Second-1)/time.Second)))
	}
	Error(c, err.Status, err.Code, err.Message)
}
