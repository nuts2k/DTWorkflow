package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
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

	tasks := make([]gin.H, 0, len(records))
	for _, r := range records {
		tasks = append(tasks, taskToJSON(r))
	}

	Success(c, http.StatusOK, gin.H{
		"tasks":  tasks,
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
	if h.deps.Enqueuer == nil {
		h.deps.Logger.Error("任务重试失败：入队器未配置", "id", id)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "重试任务失败")
		return
	}

	if _, _, err := queue.RetryTask(ctx, h.deps.Store, h.deps.Enqueuer, id); err != nil {
		h.deps.Logger.Error("重试任务失败", "id", id, "error", err)
		Error(c, http.StatusInternalServerError, ErrCodeInternalError, "重试任务失败")
		return
	}

	Success(c, http.StatusOK, gin.H{
		"task_id": id,
		"message": "任务已重新入队",
	})
}

// taskToJSON 将 TaskRecord 转为列表项 JSON（精简字段）
func taskToJSON(r *model.TaskRecord) gin.H {
	return gin.H{
		"id":           r.ID,
		"type":         r.TaskType,
		"status":       r.Status,
		"repo":         r.RepoFullName,
		"pr_number":    r.PRNumber,
		"issue_number": r.Payload.IssueNumber,
		"triggered_by": r.TriggeredBy,
		"created_at":   r.CreatedAt,
		"updated_at":   r.UpdatedAt,
	}
}

// taskDetailToJSON 将 TaskRecord 转为详情 JSON（完整字段）
func taskDetailToJSON(r *model.TaskRecord) gin.H {
	j := taskToJSON(r)
	j["result"] = r.Result
	j["error_message"] = r.Error
	j["started_at"] = r.StartedAt
	j["completed_at"] = r.CompletedAt
	return j
}
