package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// BodyLimit caps request body size at maxBytes using the standard library's
// http.MaxBytesReader: once exceeded, subsequent reads from the body
// (typically inside a handler's JSON binding) return an error instead of
// allowing unbounded memory growth from an oversized request.
//
// This is a general foundation for this system's own (bodyless) endpoints.
// Document upload endpoints, added by a later system, will apply their own
// — likely much larger — limit rather than reuse this one.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
