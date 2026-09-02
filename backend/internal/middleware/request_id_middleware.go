package middleware

import (
	"crypto/rand"
	"fmt"
	"regexp"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/utils"
)

// maxIncomingRequestIDLen bounds how much of a client-supplied X-Request-ID
// we will ever trust or echo back, so an oversized header can't be used to
// bloat logs or downstream responses.
const maxIncomingRequestIDLen = 128

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// RequestID assigns every request a correlation ID: it preserves a valid
// caller-supplied X-Request-ID, otherwise generates a new one. The ID is
// stored on the gin context (utils.GetRequestID) and always echoed back as
// a response header.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(utils.RequestIDHeader)
		if !isValidRequestID(id) {
			id = generateRequestID()
		}

		utils.SetRequestID(c, id)
		c.Writer.Header().Set(utils.RequestIDHeader, id)
		c.Next()
	}
}

func isValidRequestID(id string) bool {
	if id == "" || len(id) > maxIncomingRequestIDLen {
		return false
	}
	return requestIDPattern.MatchString(id)
}

// generateRequestID returns a random UUIDv4-formatted string using
// crypto/rand directly, avoiding a dependency on a UUID library for a
// single call site.
func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read failing indicates a broken host entropy source;
		// returning a fixed, clearly-fake ID keeps the request flowing
		// instead of failing the request over a logging concern.
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
