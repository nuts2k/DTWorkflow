package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

func (h *handlers) listTasks(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	opts := store.ListOptions{
		RepoFullName: c.Query("repo"),
		Limit:        limit,
		Offset:       offset,
	}
	if s := c.Query("status"); s != "" {
		st := model.TaskStatus(s)
		if !st.IsValid() {
			Error(c, http.StatusBadRequest, ErrCodeBadRequest, "无效的 status 参数: "+s)
			return
		}
		opts.Status = st
	}
	if t := c.Query("type"); t != "" {
		tt := model.TaskType(t)
		if !tt.IsValid() {
			Error(c, http.StatusBadRequest, ErrCodeBadRequest, "无效的 type 参数: "+t)
			return
		}
		opts.TaskType = tt
	}

	records, err := h.deps.Store.ListTasks(c.Request.Context(), opts)
	if err != nil {
		h.deps.Logger.Error("查询任务列表失败", "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "查询任务列表失败")
		return
	}

	items := make([]gin.H, 0, len(records))
	for _, r := range records {
		items = append(items, taskToJSON(r))
	}

	Success(c, http.StatusOK, gin.H{
		"items":  items,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *handlers) getTask(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	record, err := h.deps.Store.GetTask(ctx, id)
	if err != nil {
		h.deps.Logger.Error("查询任务失败", "id", id, "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "查询任务失败")
		return
	}
	if record == nil {
		Error(c, http.StatusNotFound, ErrCodeNotFound, "任务不存在: "+id)
		return
	}

	Success(c, http.StatusOK, taskDetailToJSON(record))
}

func (h *handlers) retryTask(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()

	record, err := h.deps.Store.GetTask(ctx, id)
	if err != nil {
		h.deps.Logger.Error("查询任务失败", "id", id, "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "查询任务失败")
		return
	}
	if record == nil {
		Error(c, http.StatusNotFound, ErrCodeNotFound, "任务不存在: "+id)
		return
	}
	if record.Status != model.TaskStatusFailed && record.Status != model.TaskStatusCancelled {
		Error(c, http.StatusConflict, ErrCodeConflict,
			"任务状态为 "+string(record.Status)+"，只有 failed 或 cancelled 状态的任务可以重试")
		return
	}

	// 重置为 pending，让 RecoveryLoop 重新入队
	record.Status = model.TaskStatusPending
	record.Error = ""
	record.RetryCount = 0
	record.AsynqID = ""
	record.StartedAt = nil
	record.CompletedAt = nil
	if err := h.deps.Store.UpdateTask(ctx, record); err != nil {
		h.deps.Logger.Error("重置任务状态失败", "id", id, "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "重试任务失败")
		return
	}

	Success(c, http.StatusOK, gin.H{
		"task_id": id,
		"message": "任务已重置为 pending，将自动重新入队",
	})
}

// taskToJSON 将 TaskRecord 转为列表项 JSON（精简字段）
func taskToJSON(r *model.TaskRecord) gin.H {
	return gin.H{
		"id":             r.ID,
		"task_type":      r.TaskType,
		"status":         r.Status,
		"repo_full_name": r.RepoFullName,
		"triggered_by":   r.TriggeredBy,
		"created_at":     r.CreatedAt,
		"updated_at":     r.UpdatedAt,
	}
}

// taskDetailToJSON 将 TaskRecord 转为详情 JSON（完整字段）
func taskDetailToJSON(r *model.TaskRecord) gin.H {
	return gin.H{
		"id":             r.ID,
		"asynq_id":       r.AsynqID,
		"task_type":      r.TaskType,
		"status":         r.Status,
		"priority":       r.Priority,
		"repo_full_name": r.RepoFullName,
		"pr_number":      r.PRNumber,
		"result":         r.Result,
		"error":          r.Error,
		"retry_count":    r.RetryCount,
		"max_retry":      r.MaxRetry,
		"worker_id":      r.WorkerID,
		"delivery_id":    r.DeliveryID,
		"triggered_by":   r.TriggeredBy,
		"created_at":     r.CreatedAt,
		"updated_at":     r.UpdatedAt,
		"started_at":     r.StartedAt,
		"completed_at":   r.CompletedAt,
	}
}
