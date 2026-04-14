package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (h *handlers) getStatus(c *gin.Context) {
	redisOK := false
	if h.deps.QueueClient != nil {
		redisOK = h.deps.QueueClient.Ping(c.Request.Context()) == nil
	}

	activeWorkers := 0
	if h.deps.Pool != nil {
		activeWorkers = h.deps.Pool.Stats().Active
	}

	Success(c, http.StatusOK, gin.H{
		"version":         h.deps.Version,
		"uptime_seconds":  int(time.Since(h.deps.StartTime).Seconds()),
		"redis_connected": redisOK,
		"active_workers":  activeWorkers,
	})
}
