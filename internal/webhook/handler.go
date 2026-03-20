package webhook

import (
	"context"
	"log/slog"
)

type LogHandler struct {
	logger *slog.Logger
}

func NewLogHandler(logger *slog.Logger) *LogHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogHandler{logger: logger}
}

func (h *LogHandler) HandlePullRequest(ctx context.Context, event PullRequestEvent) error {
	h.logger.InfoContext(ctx, "收到 PR Webhook 事件",
		"delivery_id", event.DeliveryID,
		"event_type", event.EventType,
		"action", event.Action,
		"repo", event.Repository.FullName,
		"pr_number", event.PullRequest.Number,
	)
	return nil
}

func (h *LogHandler) HandleIssueLabel(ctx context.Context, event IssueLabelEvent) error {
	h.logger.InfoContext(ctx, "收到 Issue 标签 Webhook 事件",
		"delivery_id", event.DeliveryID,
		"event_type", event.EventType,
		"action", event.Action,
		"repo", event.Repository.FullName,
		"issue_number", event.Issue.Number,
		"label", event.Label.Name,
		"auto_fix_changed", event.AutoFixChanged,
		"auto_fix_added", event.AutoFixAdded,
		"auto_fix_removed", event.AutoFixRemoved,
	)
	return nil
}
