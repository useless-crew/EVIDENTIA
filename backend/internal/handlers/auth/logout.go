package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// refresh_token is optional: logging out with none supplied still
// succeeds (there is a valid access token — proven by reaching this
// handler at all — but nothing else to revoke; access tokens are
// stateless and short-lived, see master prompt §27/§28).
type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout handles POST /api/v1/auth/logout. Unlike login/refresh, this
// route DOES require a valid access token (registered behind
// middleware.Auth in the router — master prompt §56's chosen, documented
// approach): logout is itself an authenticated action, and the caller's
// own identity (not merely "some session ID") determines which session it
// may revoke — see AuthService.Logout.
//
// @Summary      Log out
// @Description  Revokes the refresh-token session identified by the supplied refresh token, if any. Requires a valid access token. Idempotent.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request  body      logoutRequest  false  "Refresh token to revoke"
// @Success      200      {object}  response.Envelope
// @Failure      401      {object}  response.Envelope  "Authentication required"
// @Router       /api/v1/auth/logout [post]
func Logout(svc *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		current, ok := authpkg.CurrentUser(c)
		if !ok {
			// Unreachable in practice (this route sits behind
			// middleware.Auth), but CurrentUser's contract is "handle
			// ok==false as an authentication error, never assume" — honored
			// here too rather than trusting placement in the router.
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		var req logoutRequest
		// Malformed/empty body is fine — RefreshToken simply stays "".
		_ = c.ShouldBindJSON(&req)

		if err := svc.Logout(c.Request.Context(), req.RefreshToken, current.ID); err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, nil)
	}
}
