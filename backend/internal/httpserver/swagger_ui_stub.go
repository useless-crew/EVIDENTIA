//go:build !swaggerui

package httpserver

import "github.com/gin-gonic/gin"

// registerSwaggerUI is a no-op in the default build (no `-tags
// swaggerui`) — see swagger_ui.go's doc comment for why the real
// implementation is opt-in rather than always compiled in.
func registerSwaggerUI(r *gin.Engine) {}
