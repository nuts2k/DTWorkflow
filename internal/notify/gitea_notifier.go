package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// GiteaCommentCreator Gitea 评论创建窄接口
// notify 包不直接依赖 internal/gitea，通过此接口解耦
type GiteaCommentCreator interface {
	CreateIssueComment(ctx context.Context, owner, repo string, index int64, body string) error
}

// GiteaCommentInfo 评论简化视图（仅 upsert 匹配所需字段）。
// 避免 notify 直接依赖 *gitea.Comment，保持窄接口。
type GiteaCommentInfo struct {
	ID   int64
	Body string
}

// GiteaPRCommentManager 扩展接口：支持列表与编辑，以便对 PR 评论做幂等 upsert。
//
// 调用方（serve 装配层）可提供一个同时实现 CreateIssueComment / ListIssueComments /
// EditIssueComment 的适配器；GiteaNotifier 在 CommentOnGenTestsPR 路径上会做类型断言
// 升级，若断言失败则退化为仅 Create（附警告日志，指出可能产生重复评论）。
type GiteaPRCommentManager interface {
	GiteaCommentCreator
	ListIssueComments(ctx context.Context, owner, repo string, index int64) ([]GiteaCommentInfo, error)
	EditIssueComment(ctx context.Context, owner, repo string, commentID int64, body string) error
}

// genTestsDoneAnchor gen_tests Done 评论的幂等锚点（不可见 HTML 注释）。
//
// 锚点不携带 task_id —— 同一 PR 上后续任何 gen_tests 成功 Done 都**覆盖**上一次评论体，
// 代表当前稳定分支的最新真相（审计链路通过 test_gen_results.output_json 按 task_id 反查）。
const genTestsDoneAnchor = "<!-- dtworkflow:gen_tests:done -->"

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

	// 提前快速失败：如果 context 已取消则避免网络调用和无效日志。
	// 注意：此检查存在 TOCTOU 窗口，底层 HTTP 客户端也会处理 context 取消，
	// 此处仅为减少无效网络请求的优化。
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发送通知前 context 已取消: %w", err)
	}

	n.logger.InfoContext(ctx, "发送 Gitea 评论通知",
		"owner", msg.Target.Owner,
		"repo", msg.Target.Repo,
		"number", msg.Target.Number,
		"event", msg.EventType,
	)

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
	title := sanitizeGiteaText(msg.Title)
	if title == "" {
		title = string(msg.EventType)
	}
	return sanitizeGiteaText(fmt.Sprintf("## %s %s\n\n%s\n\n---\n_由 DTWorkflow 自动发送 | 事件类型: %s_",
		emoji, title, msg.Body, string(msg.EventType)))
}

func sanitizeGiteaText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t', r == '\n':
			return r
		case r < 0x20:
			return -1
		case r == 0x7F:
			return -1
		case r > 0xFFFF:
			return -1
		default:
			return r
		}
	}, s)
}

// CommentOnGenTestsPR 在 gen_tests Done 时对 PR 发一条评论（含 GapAnalysis 摘要 + TestResults）。
//
// 幂等语义：评论正文开头拼接不可见锚点 genTestsDoneAnchor；若 PR 上已存在含此锚点
// 的评论（由本服务历史发送），则用 EditIssueComment 覆盖其内容；否则 CreateIssueComment
// 新增。跨任务覆盖语义见 genTestsDoneAnchor 注释。
//
// 调用方提供的 body 不应包含锚点（本方法会自行拼接）。
//
// 兼容策略：当 GiteaNotifier.client 未实现 GiteaPRCommentManager 时（仅提供最窄的
// CreateIssueCommentCreator），退化为仅 Create 并在日志中记录警告——这会导致重复评论，
// 但不会破坏通知链路；serve 装配层应尽快升级适配器到 GiteaPRCommentManager。
func (n *GiteaNotifier) CommentOnGenTestsPR(ctx context.Context, owner, repo string, prNumber int64, body string) error {
	if owner == "" || repo == "" {
		return fmt.Errorf("CommentOnGenTestsPR 需要 owner/repo: %w", ErrInvalidTarget)
	}
	if prNumber <= 0 {
		return fmt.Errorf("CommentOnGenTestsPR 需要有效的 PR 编号（> 0）: %w", ErrInvalidTarget)
	}

	full := sanitizeGiteaText(genTestsDoneAnchor + "\n\n" + body)

	mgr, ok := n.client.(GiteaPRCommentManager)
	if !ok {
		n.logger.WarnContext(ctx,
			"GiteaNotifier.client 未实现 GiteaPRCommentManager，退化为仅 Create（可能产生重复评论）",
			"owner", owner, "repo", repo, "pr", prNumber,
		)
		if err := n.client.CreateIssueComment(ctx, owner, repo, prNumber, full); err != nil {
			return fmt.Errorf("创建 gen_tests PR 评论失败 (%w): %w", ErrSendFailed, err)
		}
		return nil
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发送通知前 context 已取消: %w", err)
	}

	comments, err := mgr.ListIssueComments(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("列出 PR 评论失败 (%w): %w", ErrSendFailed, err)
	}
	for _, cm := range comments {
		if commentHasGenTestsAnchor(cm.Body) {
			n.logger.InfoContext(ctx, "覆盖已有 gen_tests PR 评论",
				"owner", owner, "repo", repo, "pr", prNumber, "comment_id", cm.ID,
			)
			if err := mgr.EditIssueComment(ctx, owner, repo, cm.ID, full); err != nil {
				return fmt.Errorf("编辑 PR 评论失败 (%w): %w", ErrSendFailed, err)
			}
			return nil
		}
	}

	n.logger.InfoContext(ctx, "新增 gen_tests PR 评论",
		"owner", owner, "repo", repo, "pr", prNumber,
	)
	if err := mgr.CreateIssueComment(ctx, owner, repo, prNumber, full); err != nil {
		return fmt.Errorf("创建 gen_tests PR 评论失败 (%w): %w", ErrSendFailed, err)
	}
	return nil
}

// commentHasGenTestsAnchor 严格子串匹配锚点。
//
// 只要评论正文中出现完整的锚点字符串即视为命中，避免被相似但非本服务的注释误匹配。
// 锚点本身不含正则特殊字符，直接使用 strings.Contains 足够安全。
func commentHasGenTestsAnchor(body string) bool {
	return strings.Contains(body, genTestsDoneAnchor)
}

// severityEmoji 根据紧急程度返回对应的 emoji
func severityEmoji(s Severity) string {
	switch s {
	case SeverityWarning:
		return "⚠️"
	case SeverityCritical:
		return "‼️"
	default:
		// info 及其他未知类型均使用 info emoji
		return "ℹ️"
	}
}
