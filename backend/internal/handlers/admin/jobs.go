package admin

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/jobs"
	"evidentia/backend/pkg/response"
)

// QueueInfo carries inspection metrics for an Asynq queue.
type QueueInfo struct {
	Queue     string `json:"queue"`
	Size      int    `json:"size"`
	Active    int    `json:"active"`
	Pending   int    `json:"pending"`
	Scheduled int    `json:"scheduled"`
	Retry     int    `json:"retry"`
	Archived  int    `json:"archived"`
	Completed int    `json:"completed"`
	Paused    bool   `json:"paused"`
}

// JobsSummaryResponse is GET /api/v1/admin/jobs's payload shape.
type JobsSummaryResponse struct {
	Queues    []QueueInfo `json:"queues"`
	Timestamp time.Time   `json:"timestamp"`
}

// Jobs handles GET /api/v1/admin/jobs.
// Inspects active and pending jobs across QueueCritical and QueueDefault using asynq.Inspector.
func Jobs(jobClient *jobs.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		inspector := jobClient.Inspector()
		if inspector == nil {
			response.Success(c, http.StatusOK, JobsSummaryResponse{
				Queues:    []QueueInfo{},
				Timestamp: time.Now().UTC(),
			})
			return
		}
		defer inspector.Close()

		queueNames := []string{jobs.QueueCritical, jobs.QueueDefault}
		queueInfos := make([]QueueInfo, 0, len(queueNames))

		for _, qName := range queueNames {
			info, err := inspector.GetQueueInfo(qName)
			if err != nil {
				queueInfos = append(queueInfos, QueueInfo{
					Queue: qName,
				})
				continue
			}

			queueInfos = append(queueInfos, QueueInfo{
				Queue:     qName,
				Size:      info.Size,
				Active:    info.Active,
				Pending:   info.Pending,
				Scheduled: info.Scheduled,
				Retry:     info.Retry,
				Archived:  info.Archived,
				Completed: info.Completed,
				Paused:    info.Paused,
			})
		}

		response.Success(c, http.StatusOK, JobsSummaryResponse{
			Queues:    queueInfos,
			Timestamp: time.Now().UTC(),
		})
	}
}
