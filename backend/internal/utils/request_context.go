package utils

import "github.com/gin-gonic/gin"

// RequestIDKey is the gin.Context key the request-ID middleware stores the
// per-request correlation ID under.
const RequestIDKey = "request_id"

// RequestIDHeader is both the inbound header clients may supply and the
// outbound header the server always sets in response.
const RequestIDHeader = "X-Request-ID"

// SetRequestID stores the request ID on the context for downstream
// middleware, handlers, and loggers to read.
func SetRequestID(c *gin.Context, id string) {
	c.Set(RequestIDKey, id)
}

// GetRequestID returns the request ID set for this request, or "" if none
// was set (e.g. the request-ID middleware was not registered, such as in a
// handler unit test).
func GetRequestID(c *gin.Context) string {
	if v, ok := c.Get(RequestIDKey); ok {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}
