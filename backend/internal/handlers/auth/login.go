package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// loginRequest is intentionally validated only for SHAPE here (present,
// syntactically an email, minimum length) — whether the email/password
// pair is actually correct is a business decision made by AuthService.
type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,max=72"`
}

// Login handles POST /api/v1/auth/login.
//
// @Summary      Log in
// @Description  Validates email/password and returns a short-lived access token plus a refresh token. Returns a single generic error for any invalid-credential case (unknown email, wrong password, inactive account) — never which one, to avoid user enumeration. Subject to a per-IP and per-account login throttle (System 15) — see the 429 response.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request  body      loginRequest  true  "Login credentials"
// @Success      200      {object}  response.Envelope{data=tokenResponse}
// @Failure      400      {object}  response.Envelope  "Invalid request body"
// @Failure      401      {object}  response.Envelope  "Invalid email or password"
// @Failure      429      {object}  response.Envelope  "Too many login attempts from this IP or against this account — see the Retry-After response header for when to retry"
// @Router       /api/v1/auth/login [post]
func Login(svc *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		result, err := svc.Login(c.Request.Context(), req.Email, req.Password, c.ClientIP(), c.Request.UserAgent())
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, toTokenResponse(result, svc.AccessTTLSeconds()))
	}
}
