// Package user implements the admin user-management HTTP handlers
// (/api/v1/admin/users*, /api/v1/admin/roles) plus the self-profile
// endpoint (/api/v1/users/me): parse/validate the request, obtain the
// already-authenticated (and, for most routes, already RBAC-authorized —
// see internal/middleware.RequirePermission in internal/httpserver/
// router.go) caller, delegate to internal/service.UserService, shape the
// response. No SQL, transaction, role matrix, or audit write happens in
// this package directly — that is the service's job (matches
// internal/handlers/case's own doc comment).
package user

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// createUserRequest is POST /admin/users's request body. Deliberately has
// no id/created_at/updated_at field — the client cannot supply them, not
// merely "should not" (master prompt §5): those are server-controlled and
// this struct has no field a client-sent value could even bind into.
// binding:"email" additionally shape-validates the address the same way
// loginRequest already does; UserService independently re-validates it
// (master prompt: service-layer validation, never trust that a handler's
// binding tag is the only check). max=72 on Password mirrors bcrypt's own
// hard limit (golang.org/x/crypto/bcrypt.GenerateFromPassword errors
// rather than silently truncating past 72 bytes) — rejecting it here
// produces a clean 400 instead of surfacing that error out of
// auth.HashPassword as an internal-error 500.
type createUserRequest struct {
	Email       string  `json:"email" binding:"required,email"`
	Password    string  `json:"password" binding:"required,min=8,max=72"`
	FirstName   string  `json:"first_name" binding:"required,max=255"`
	LastName    string  `json:"last_name" binding:"required,max=255"`
	DisplayName *string `json:"display_name" binding:"omitempty,max=255"`
	Phone       *string `json:"phone" binding:"omitempty,max=32"`
	Role        string  `json:"role" binding:"required"`
	Status      *string `json:"status" binding:"omitempty"`
}

// updateUserRequest is PUT /admin/users/:id's request body — a full
// replacement of every mutable profile field (see
// service.UpdateUserInput's doc comment). Excludes email/password/role/
// status, each of which has its own dedicated route.
type updateUserRequest struct {
	FirstName   string  `json:"first_name" binding:"required,max=255"`
	LastName    string  `json:"last_name" binding:"required,max=255"`
	DisplayName *string `json:"display_name" binding:"omitempty,max=255"`
	Phone       *string `json:"phone" binding:"omitempty,max=32"`
}

// updateRoleRequest is PUT /admin/users/:id/role's request body.
type updateRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

// updateStatusRequest is PUT /admin/users/:id/status's request body.
type updateStatusRequest struct {
	Status string `json:"status" binding:"required"`
}

// resetPasswordRequest is PUT /admin/users/:id/password's request body.
type resetPasswordRequest struct {
	Password string `json:"password" binding:"required,min=8,max=72"`
}

// userResponse/userListResponse are thin aliases over the service's own
// response-shaped DTOs — this package returns them directly rather than
// re-mapping field-by-field, matching handlers/case's caseDetailResponse
// convention exactly.
type userResponse = service.AdminUserSummary
type userListResponse = service.UserListResult
type roleListResponse = []service.RoleCatalogEntry

// writeServiceError renders err through the standard envelope, matching
// internal/handlers/case's helper of the same name/behavior exactly.
func writeServiceError(c *gin.Context, err error) {
	if appErr, ok := utils.AsAppError(err); ok {
		response.FromAppError(c, appErr)
		return
	}
	response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
}
