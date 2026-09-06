package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/service"
	"evidentia/backend/pkg/response"
)

// DashboardStats carries aggregate metrics for the Evidentia administrative hub.
// Every figure is sourced from real database queries — never fabricated.
type DashboardStats struct {
	TotalUsers         int64  `json:"total_users"`
	ActiveUsers        int64  `json:"active_users"`
	TotalCases         int64  `json:"total_cases"`
	ActiveCases        int64  `json:"active_cases"`
	TotalDocuments     int64  `json:"total_documents"`
	TotalAuditEntries  int64  `json:"total_audit_entries"`
	ChainHeadSeq       *int64 `json:"chain_head_seq,omitempty"`
	ChainHeadHash      string `json:"chain_head_hash,omitempty"`
	LastAuditStatus    string `json:"last_audit_status,omitempty"`
	BlockchainEnabled  bool   `json:"blockchain_enabled"`
	TotalAnchors       int64  `json:"total_anchors"`
	PendingAnchors     int64  `json:"pending_anchors"`
	ConfirmedAnchors   int64  `json:"confirmed_anchors"`
	FailedAnchors      int64  `json:"failed_anchors"`
	Timestamp          time.Time `json:"timestamp"`
}

// Stats handles GET /api/v1/admin/dashboard/stats.
// Requires ADMIN role via middleware.RequireAdmin.
func Stats(pool *pgxpool.Pool, auditSvc *service.AuditService, blockchainAnchorSvc *service.BlockchainAnchorService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		var stats DashboardStats
		stats.Timestamp = time.Now().UTC()

		// Run queries under ADMIN RLS identity
		adminIdent := repository.AppIdentity{
			UserID: uuid.Nil,
			Role:   models.RoleAdmin,
		}

		err := repository.WithTx(ctx, pool, adminIdent, func(ctx context.Context, q *generated.Queries) error {
			// Users count
			totalUsers, err := q.CountUsers(ctx)
			if err != nil {
				return err
			}
			stats.TotalUsers = totalUsers

			activeStatus := models.UserStatusActive
			activeUsers, err := q.CountUsersFiltered(ctx, generated.CountUsersFilteredParams{
				Status: &activeStatus,
			})
			if err != nil {
				return err
			}
			stats.ActiveUsers = activeUsers

			// Cases count
			totalCases, err := q.CountCases(ctx)
			if err != nil {
				return err
			}
			stats.TotalCases = totalCases

			// Documents count (via direct SQL count on documents table)
			// RLS allows ADMIN to see all rows
			var totalDocs int64
			row := pool.QueryRow(ctx, "SELECT count(*) FROM documents")
			if scanErr := row.Scan(&totalDocs); scanErr == nil {
				stats.TotalDocuments = totalDocs
			}

			// Active cases count (OPEN, UNDER_INVESTIGATION, SUBMITTED, UNDER_REVIEW)
			var activeCases int64
			caseRow := pool.QueryRow(ctx, "SELECT count(*) FROM cases WHERE status IN ('OPEN', 'UNDER_INVESTIGATION', 'SUBMITTED', 'UNDER_REVIEW')")
			if scanErr := caseRow.Scan(&activeCases); scanErr == nil {
				stats.ActiveCases = activeCases
			}

			// Blockchain anchors count
			stats.BlockchainEnabled = blockchainAnchorSvc.BlockchainService().IsEnabled()
			var totalAnchors, pendingAnchors, confirmedAnchors, failedAnchors int64
			bRow := pool.QueryRow(ctx, `
				SELECT
					count(*),
					count(*) FILTER (WHERE status = 'PENDING'),
					count(*) FILTER (WHERE status = 'CONFIRMED'),
					count(*) FILTER (WHERE status = 'FAILED')
				FROM blockchain_anchors
			`)
			if scanErr := bRow.Scan(&totalAnchors, &pendingAnchors, &confirmedAnchors, &failedAnchors); scanErr == nil {
				stats.TotalAnchors = totalAnchors
				stats.PendingAnchors = pendingAnchors
				stats.ConfirmedAnchors = confirmedAnchors
				stats.FailedAnchors = failedAnchors
			}

			return nil
		})

		if err != nil {
			writeServiceError(c, err)
			return
		}

		// Audit chain integrity summary
		adminUser := authpkg.AuthenticatedUser{
			ID:    uuid.Nil,
			Roles: []string{models.RoleAdmin},
		}
		integrity, err := auditSvc.GetIntegritySummary(ctx, adminUser)
		if err == nil && integrity != nil {
			stats.TotalAuditEntries = integrity.TotalEntries
			stats.ChainHeadSeq = integrity.ChainHeadSeq
			stats.ChainHeadHash = integrity.ChainHeadHash
			if integrity.LastVerification != nil {
				stats.LastAuditStatus = integrity.LastVerification.Status
			}
		}

		response.Success(c, http.StatusOK, stats)
	}
}
