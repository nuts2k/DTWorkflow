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

// GiteaNotifierOption 配置选项函数类型。
// 注意：此处签名为 func(*GiteaNotifier)（无返回值），与设计文档中
// func(*GiteaNotifier) error 的签名有所不同。当前所有 Option（如 WithLogger）
// 仅做简单赋值，无需返回错误，故选择更简洁的无错误返回形式。
// 若后续新增可能失败的 Option，应迁移为 func(*GiteaNotifier) error 签名。
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
	if msg.Target.Owner == "" || msg.Target.Repo == "" {
		return fmt.Errorf("gitea 通知器需要指定 Owner 和 Repo: %w", ErrInvalidTarget)
	}
	if msg.Target.Number <= 0 {
		return fmt.Errorf("gitea 通知器需要指定有效的 Issue/PR 编号（> 0）: %w", ErrInvalidTarget)
	}

	comment := formatGiteaComment(msg)

	n.logger.InfoContext(ctx, "发送 Gitea 评论通知",
		"owner", msg.Target.Owner,
		"repo", msg.Target.Repo,
		"number", msg.Target.Number,
		"event", msg.EventType,
	)

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发送通知前 context 已取消: %w", err)
	}

	if err := n.client.CreateIssueComment(ctx, msg.Target.Owner, msg.Target.Repo, msg.Target.Number, comment); err != nil {
		return fmt.Errorf("创建 Gitea 评论失败 (%w): %w", ErrSendFailed, err)
	}

	return nil
}

// formatGiteaComment 将消息格式化为 Gitea Markdown 评论
// 安全说明：msg.Title 和 msg.Body 的内容由 DTWorkflow 内部生成，不直接来自外部用户输入。
// 即便包含 HTML 标签，Gitea 服务端在渲染 Markdown 时会自行做 HTML sanitize，XSS 风险可接受。
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
