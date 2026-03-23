package notify

import (
	"context"
	"fmt"
	"log/slog"
)

// GiteaCommentCreator Gitea 评论创建窄接口
// notify 包不直接依赖 internal/gitea，通过此接口解耦
type GiteaCommentCreator interface {
	CreateIssueComment(ctx context.Context, owner, repo string, index int64, body string) error
}

// GiteaNotifierOption 配置选项
type GiteaNotifierOption func(*GiteaNotifier)

// GiteaNotifier 通过 Gitea Issue/PR 评论发送通知
type GiteaNotifier struct {
	client GiteaCommentCreator
	logger *slog.Logger
}

// NewGiteaNotifier 创建 GiteaNotifier 实例
func NewGiteaNotifier(client GiteaCommentCreator, opts ...GiteaNotifierOption) (*GiteaNotifier, error) {
	if client == nil {
		return nil, fmt.Errorf("gitea 评论客户端不能为 nil: %w", ErrInvalidTarget)
	}
	n := &GiteaNotifier{
		client: client,
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(n)
	}
	return n, nil
}

// WithLogger 设置自定义日志记录器
func WithLogger(logger *slog.Logger) GiteaNotifierOption {
	return func(n *GiteaNotifier) {
		if logger != nil {
			n.logger = logger
		}
	}
}

// Name 返回通知渠道名称
func (n *GiteaNotifier) Name() string {
	return "gitea"
}

// Send 发送通知到 Gitea Issue/PR 评论
func (n *GiteaNotifier) Send(ctx context.Context, msg Message) error {
	if msg.Target.Number == 0 {
		return fmt.Errorf("gitea 通知器需要指定 Issue/PR 编号: %w", ErrInvalidTarget)
	}

	comment := formatGiteaComment(msg)

	n.logger.InfoContext(ctx, "发送 Gitea 评论通知",
		"owner", msg.Target.Owner,
		"repo", msg.Target.Repo,
		"number", msg.Target.Number,
		"event", msg.EventType,
	)

	if err := n.client.CreateIssueComment(ctx, msg.Target.Owner, msg.Target.Repo, msg.Target.Number, comment); err != nil {
		return fmt.Errorf("创建 Gitea 评论失败: %w", err)
	}

	return nil
}

// formatGiteaComment 将消息格式化为 Gitea Markdown 评论
func formatGiteaComment(msg Message) string {
	emoji := severityEmoji(msg.Severity)
	return fmt.Sprintf("## %s %s\n\n%s\n\n---\n_由 DTWorkflow 自动发送 | 事件类型: %s_",
		emoji, msg.Title, msg.Body, string(msg.EventType))
}

// severityEmoji 根据紧急程度返回对应的 emoji
func severityEmoji(s Severity) string {
	switch s {
	case SeverityWarning:
		return "⚠️"
	case SeverityCritical:
		return "🚨"
	default:
		// info 及其他未知类型均使用 info emoji
		return "ℹ️"
	}
}
