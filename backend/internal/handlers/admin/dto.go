package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

func writeServiceError(c *gin.Context, err error) {
	if appErr, ok := utils.AsAppError(err); ok {
		response.FromAppError(c, appErr)
		return
	}
	response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
}
