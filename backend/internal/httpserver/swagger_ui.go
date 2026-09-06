//go:build swaggerui

// This file is compiled ONLY with `-tags swaggerui` (e.g. `go run -tags
// swaggerui ./cmd/server`) — never in the default build, and never in
// backend/Dockerfile's production image. Two reasons, both deliberate:
//
//  1. It imports the generated evidentia/backend/docs/swagger package
//     (`docs.go`, produced by `make swagger` / `swag init`), which is
//     gitignored on purpose (see the root .gitignore's comment) so a
//     fresh clone builds and tests with no swag CLI installed. An
//     unconditional import here would break that guarantee — this file
//     simply isn't part of the build unless you've both run `make
//     swagger` AND opted in with the tag.
//  2. An interactive "try it out" API console is a local-development
//     convenience, not something a production deployment should expose
//     by default alongside real evidence data — see
//     docs/DEPLOYMENT.md's "Swagger UI (local development)" note.
package httpserver

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "evidentia/backend/docs/swagger" // registers the generated spec with swag's registry — see its own doc comment
)

func registerSwaggerUI(r *gin.Engine) {
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}
