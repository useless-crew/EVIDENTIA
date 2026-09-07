package cases

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

type addMemberRequest struct {
	UserID         uuid.UUID `json:"user_id" binding:"required"`
	MembershipType string    `json:"membership_type" binding:"required"`
}

// AddMember handles POST /api/v1/cases/:id/members.
// Allows case owner or admin to assign a user to the case (e.g., FORENSICS, INVESTIGATOR, LAWYER).
func AddMember(svc *service.CaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		caseID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, "You do not have permission to perform this action")
			return
		}

		var req addMemberRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body: user_id and membership_type are required")
			return
		}

		member, err := svc.AddMember(c.Request.Context(), user, caseID, service.AddCaseMemberInput{
			UserID:         req.UserID,
			MembershipType: req.MembershipType,
		})
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusCreated, member)
	}
}

// RemoveMember handles DELETE /api/v1/cases/:id/members/:userId.
// Allows case owner or admin to remove a member from the case.
func RemoveMember(svc *service.CaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		caseID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, "You do not have permission to perform this action")
			return
		}

		targetUserID, err := uuid.Parse(c.Param("userId"))
		if err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid target user ID")
			return
		}

		if err := svc.RemoveMember(c.Request.Context(), user, caseID, targetUserID); err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, gin.H{"removed": true})
	}
}

// ListMembers handles GET /api/v1/cases/:id/members.
// Returns all active team members/collaborators for the case.
func ListMembers(svc *service.CaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		caseID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, "You do not have permission to perform this action")
			return
		}

		members, err := svc.ListMembers(c.Request.Context(), user, caseID)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, members)
	}
}
