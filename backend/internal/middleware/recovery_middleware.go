package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Recovery replaces Gin's default panic recovery so a panic in any handler
// produces the same JSON error envelope as any other internal error,
// instead of Gin's plain-text default. The stack trace is logged
// server-side only — never returned to the client.
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered",
					slog.String("request_id", utils.GetRequestID(c)),
					slog.String("method", c.Request.Method),
					slog.String("path", c.Request.URL.Path),
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
				)

				response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
				c.Abort()
			}
		}()
		c.Next()
	}
}
