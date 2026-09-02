// Package auth implements the /api/v1/auth HTTP handlers: parse/validate
// the request, delegate to internal/service.AuthService, shape the
// response. No database query, bcrypt call, or token generation happens
// in this package directly — that is the service's job (master prompt
// §37).
package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// userResponse is the public-safe user shape returned by login/refresh —
// never password_hash, never a refresh-token hash, never an internal ID
// beyond the user's own UUID.
type userResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Role      string `json:"role,omitempty"`
}

// tokenResponse is returned by both POST /auth/login and POST /auth/refresh.
type tokenResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	TokenType    string       `json:"token_type"`
	ExpiresIn    int64        `json:"expires_in"`
	User         userResponse `json:"user"`
}

func toTokenResponse(result *service.AuthResult, expiresIn int64) tokenResponse {
	return tokenResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    expiresIn,
		User: userResponse{
			ID:        result.User.ID.String(),
			Email:     result.User.Email,
			FirstName: result.User.FirstName,
			LastName:  result.User.LastName,
			Role:      result.User.Role,
		},
	}
}

// writeServiceError renders err through the standard envelope. If err is
// an *utils.AppError (which every AuthService error is — see
// utils.ErrUnauthorized/ErrInternal), its status/code/public message are
// used as-is; any other error type is treated as an unexpected internal
// failure with a safe, generic message (never the raw error text, which
// could contain a driver/SQL detail).
func writeServiceError(c *gin.Context, err error) {
	if appErr, ok := utils.AsAppError(err); ok {
		response.FromAppError(c, appErr)
		return
	}
	response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
}
