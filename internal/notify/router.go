package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// RoutingRule 路由规则，用于将事件映射到通知渠道
type RoutingRule struct {
	// RepoPattern 仓库匹配模式："*" 匹配全部，"owner/repo" 精确匹配
	RepoPattern string
	// EventTypes 匹配的事件类型列表，空切片匹配全部事件
	EventTypes []EventType
	// Channels 目标通知渠道名称列表
	Channels []string
}

// Router 是通知路由器，负责将消息分发到匹配的通知渠道。
// 并发安全：Router 在 NewRouter 完成构建后，Send 方法可安全被多 goroutine 并发调用。
// 但 Router 的配置（notifiers/rules/fallback）在创建后不应被修改。
type Router struct {
	notifiers map[string]Notifier
	rules     []RoutingRule
	fallback  string
	logger    *slog.Logger
}

// RouterOption 路由器配置选项
type RouterOption func(*Router) error

// NewRouter 创建通知路由器实例
func NewRouter(opts ...RouterOption) (*Router, error) {
	r := &Router{
		notifiers: make(map[string]Notifier),
		logger:    slog.Default(),
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, fmt.Errorf("应用路由器选项: %w", err)
		}
	}
	return r, nil
}

// WithNotifier 注册通知渠道
func WithNotifier(n Notifier) RouterOption {
	return func(r *Router) error {
		if n == nil {
			return errors.New("通知渠道不能为 nil")
		}
		r.notifiers[n.Name()] = n
		return nil
	}
}

// WithRules 设置路由规则列表
func WithRules(rules []RoutingRule) RouterOption {
	return func(r *Router) error {
		r.rules = rules
		return nil
	}
}

// WithFallback 设置默认（兜底）通知渠道名称
func WithFallback(channel string) RouterOption {
	return func(r *Router) error {
		r.fallback = channel
		return nil
	}
}

// WithRouterLogger 设置路由器日志记录器
func WithRouterLogger(logger *slog.Logger) RouterOption {
	return func(r *Router) error {
		if logger != nil {
			r.logger = logger
		}
		return nil
	}
}

// Send 根据路由规则将消息发送到匹配的通知渠道
// 单个渠道失败不中止其余渠道，最终通过 errors.Join 返回聚合错误
func (r *Router) Send(ctx context.Context, msg Message) error {
	channels := r.resolveChannels(msg)

	// 注意：设计文档中无匹配渠道时静默返回 nil，实际实现选择返回错误以避免通知被静默丢弃
	if len(channels) == 0 {
		return ErrNoChannelMatched
	}

	var errs []error
	for _, ch := range channels {
		n, ok := r.notifiers[ch]
		if !ok {
			r.logger.WarnContext(ctx, "引用了未注册的通知渠道", "channel", ch)
			errs = append(errs, fmt.Errorf("渠道 %q: %w", ch, ErrNotifierNotFound))
			continue
		}
		if err := n.Send(ctx, msg); err != nil {
			r.logger.ErrorContext(ctx, "通知渠道发送失败",
				"channel", ch,
				"error", err,
			)
			errs = append(errs, fmt.Errorf("渠道 %q: %w", ch, err))
		} else {
			r.logger.InfoContext(ctx, "通知发送成功", "channel", ch, "event", string(msg.EventType))
		}
	}

	return errors.Join(errs...)
}

// resolveChannels 根据消息匹配路由规则，返回去重后的渠道名称列表
// 无匹配时返回 fallback（如果已配置）
func (r *Router) resolveChannels(msg Message) []string {
	repoKey := msg.Target.Owner + "/" + msg.Target.Repo

	seen := make(map[string]bool)
	var channels []string

	for _, rule := range r.rules {
		// 匹配仓库模式
		if rule.RepoPattern != "*" && rule.RepoPattern != repoKey {
			continue
		}

		// 匹配事件类型（空切片匹配全部）
		if !matchEventTypes(rule.EventTypes, msg.EventType) {
			continue
		}

		// 收集渠道并去重
		for _, ch := range rule.Channels {
			if !seen[ch] {
				seen[ch] = true
				channels = append(channels, ch)
			}
		}
	}

	// 无匹配时使用 fallback
	if len(channels) == 0 && r.fallback != "" {
		channels = []string{r.fallback}
	}

	return channels
}

// matchEventTypes 检查事件类型是否匹配规则中的事件列表
// 空列表或列表中包含 "*" 时匹配全部事件
func matchEventTypes(eventTypes []EventType, event EventType) bool {
	if len(eventTypes) == 0 {
		return true
	}
	for _, et := range eventTypes {
		if et == "*" || et == event {
			return true
		}
	}
	return false
}
