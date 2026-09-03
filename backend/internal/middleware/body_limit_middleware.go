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
// This is the general foundation used for JSON-bodied routes (auth, cases).
// Document upload routes (System 6) apply their own, much larger limit via
// a separate BodyLimit call using config.DocumentsConfig.MaxUploadSize —
// see internal/httpserver/router.go — rather than sharing this one, and
// are therefore NOT covered by an engine-wide BodyLimit call: two
// http.MaxBytesReader wrappings on the same request compose by taking the
// SMALLER limit (each wrapper enforces its own cap against reads from
// whatever it wraps), so applying this smaller, JSON-sized limit globally
// would silently cap every upload at it regardless of any larger limit
// applied afterward.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
