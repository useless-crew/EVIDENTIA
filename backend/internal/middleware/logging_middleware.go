package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/utils"
)

// RequestLogger emits one structured log line per completed request. This
// is operational logging for observability — method, path, status,
// duration, request ID — and is deliberately separate from the Evidentia
// audit trail (internal/audit), which records security-sensitive domain
// actions, not HTTP traffic.
//
// It never logs request/response bodies or headers, so it cannot leak
// credentials, tokens, or document content by construction.
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()

		attrs := []any{
			slog.String("request_id", utils.GetRequestID(c)),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", status),
			slog.Duration("duration", duration),
			slog.String("client_ip", c.ClientIP()),
		}

		if len(c.Errors) > 0 {
			attrs = append(attrs, slog.String("error", c.Errors.String()))
		}

		switch {
		case status >= 500:
			logger.Error("request completed", attrs...)
		case status >= 400:
			logger.Warn("request completed", attrs...)
		default:
			logger.Info("request completed", attrs...)
		}
	}
}
