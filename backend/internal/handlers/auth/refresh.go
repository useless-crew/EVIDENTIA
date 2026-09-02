package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Refresh handles POST /api/v1/auth/refresh. It is public (no access token
// required — master prompt §56): the refresh token itself is the
// credential presented.
//
// @Summary      Refresh an access token
// @Description  Rotates the presented refresh token: it is revoked and a new access token plus new refresh token (same session family) are issued. Reusing an already-rotated token is treated as compromise and revokes the entire token family.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request  body      refreshRequest  true  "Refresh token"
// @Success      200      {object}  response.Envelope{data=tokenResponse}
// @Failure      400      {object}  response.Envelope  "Invalid request body"
// @Failure      401      {object}  response.Envelope  "Invalid or expired refresh token"
// @Router       /api/v1/auth/refresh [post]
func Refresh(svc *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req refreshRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		result, err := svc.Refresh(c.Request.Context(), req.RefreshToken, c.ClientIP(), c.Request.UserAgent())
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, toTokenResponse(result, svc.AccessTTLSeconds()))
	}
}
