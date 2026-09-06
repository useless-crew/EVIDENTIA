package admin

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/internal/service"
	"evidentia/backend/pkg/response"
)

// BlockchainAdminInfo carries operational configuration and anchor ledger counts.
type BlockchainAdminInfo struct {
	Enabled          bool      `json:"enabled"`
	Status           string    `json:"status"` // CONNECTED, DISABLED, or UNAVAILABLE
	Channel          string    `json:"channel,omitempty"`
	Chaincode        string    `json:"chaincode,omitempty"`
	MSPID            string    `json:"msp_id,omitempty"`
	TotalAnchors     int64     `json:"total_anchors"`
	PendingAnchors   int64     `json:"pending_anchors"`
	ConfirmedAnchors int64     `json:"confirmed_anchors"`
	FailedAnchors    int64     `json:"failed_anchors"`
	Timestamp        time.Time `json:"timestamp"`
}

// Blockchain handles GET /api/v1/admin/blockchain.
// Provides administrative visibility into the Hyperledger Fabric anchor status.
func Blockchain(pool *pgxpool.Pool, anchorSvc *service.BlockchainAnchorService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		enabled := anchorSvc.BlockchainService().IsEnabled()
		status := "DISABLED"
		if enabled {
			status = "CONNECTED"
		}

		info := BlockchainAdminInfo{
			Enabled:   enabled,
			Status:    status,
			Channel:   anchorSvc.Channel(),
			Chaincode: anchorSvc.Chaincode(),
			MSPID:     anchorSvc.MSPID(),
			Timestamp: time.Now().UTC(),
		}

		// Count anchors from postgres
		row := pool.QueryRow(ctx, "SELECT count(*), count(*) FILTER (WHERE status = 'PENDING'), count(*) FILTER (WHERE status = 'CONFIRMED'), count(*) FILTER (WHERE status = 'FAILED') FROM blockchain_anchors")
		_ = row.Scan(&info.TotalAnchors, &info.PendingAnchors, &info.ConfirmedAnchors, &info.FailedAnchors)

		response.Success(c, http.StatusOK, info)
	}
}