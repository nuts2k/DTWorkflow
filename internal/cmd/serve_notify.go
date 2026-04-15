package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
)

// buildNotifyRules / buildNotifier 属于 serve 命令的装配层逻辑，用于将统一配置入口中的 notify 配置
// 映射为运行时可用的 Router/Notifier。
//
// 注意：本任务仅完成"通知路由配置驱动"，不会扩展为完整 serve 配置迁移（Task 6）。
// buildNotifyRules 将配置中的路由规则映射为 notify.Router 可识别的规则结构。
//
// repoFullName 用于触发仓库级覆盖（ResolveNotifyRoutes）。
// 返回值 fallback 直接来自 cfg.Notify.DefaultChannel。
func buildNotifyRules(cfg *config.Config, repoFullName string) ([]notify.RoutingRule, string) {
	if cfg == nil {
		return nil, ""
	}

	routes := cfg.ResolveNotifyRoutes(repoFullName)
	rules := make([]notify.RoutingRule, 0, len(routes))
	for _, r := range routes {
		rule := notify.RoutingRule{
			RepoPattern: r.Repo,
			Channels:    append([]string(nil), r.Channels...),
		}
		if len(r.Events) > 0 {
			rule.EventTypes = make([]notify.EventType, 0, len(r.Events))
			for _, e := range r.Events {
				rule.EventTypes = append(rule.EventTypes, notify.EventType(e))
			}
		}
		rules = append(rules, rule)
	}

	return rules, cfg.Notify.DefaultChannel
}

type configDrivenNotifier struct {
	cfg            *config.Config
	giteaNotifier  notify.Notifier
	feishuNotifier notify.Notifier // 飞书通知器（可选）
	logger         *slog.Logger

	// 对未声明仓库级 notify 覆盖的仓库，复用同一个全局 Router，避免按 repoFullName 无上限缓存。
	// 对显式声明了 repo.notify.routes 的仓库，才按仓库缓存 Router。
	// 说明：当前实现不支持配置热更新"即时生效"。即使 cfgManager 热加载更新了 cfg，
	// 已缓存的 router 也不会自动刷新；后续如需支持，需要引入显式的更新机制。
	mu           sync.Mutex
	globalRouter *notify.Router
	routers      map[string]*notify.Router
}

func (n *configDrivenNotifier) Send(ctx context.Context, msg notify.Message) error {
	if n == nil {
		return nil
	}
	repoFullName := msg.Target.Owner + "/" + msg.Target.Repo
	router, err := n.getRouter(repoFullName)
	if err != nil {
		return err
	}
	return router.Send(ctx, msg)
}

func (n *configDrivenNotifier) getRouter(repoFullName string) (*notify.Router, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.hasRepoNotifyOverride(repoFullName) {
		if n.globalRouter != nil {
			return n.globalRouter, nil
		}
		router, err := n.newRouter(repoFullName)
		if err != nil {
			return nil, err
		}
		n.globalRouter = router
		return router, nil
	}

	if n.routers == nil {
		n.routers = make(map[string]*notify.Router)
	}
	if r, ok := n.routers[repoFullName]; ok {
		return r, nil
	}

	router, err := n.newRouter(repoFullName)
	if err != nil {
		return nil, err
	}

	n.routers[repoFullName] = router
	return router, nil
}

func (n *configDrivenNotifier) hasRepoNotifyOverride(repoFullName string) bool {
	if n == nil || n.cfg == nil {
		return false
	}
	for _, repo := range n.cfg.Repos {
		if repo.Name == repoFullName && repo.Notify != nil &&
			(repo.Notify.Routes != nil || repo.Notify.Feishu != nil) {
			return true
		}
	}
	return false
}

func (n *configDrivenNotifier) newRouter(repoFullName string) (*notify.Router, error) {
	rules, fallback := buildNotifyRules(n.cfg, repoFullName)

	opts := []notify.RouterOption{
		notify.WithRules(rules),
		notify.WithFallback(fallback),
		notify.WithRouterLogger(n.logger),
	}

	if n.giteaNotifier != nil {
		opts = append(opts, notify.WithNotifier(n.giteaNotifier))
	}
	if n.feishuNotifier != nil {
		opts = append(opts, notify.WithNotifier(n.feishuNotifier))
	}

	router, err := notify.NewRouter(opts...)
	if err != nil {
		return nil, fmt.Errorf("构造通知路由失败: %w", err)
	}
	return router, nil
}

func buildNotifier(cfg *config.Config, giteaClient *gitea.Client) (queue.TaskNotifier, error) {
	if cfg == nil {
		return nil, nil
	}

	giteaCh, giteaEnabled := cfg.Notify.Channels["gitea"]
	feishuCfg, feishuEnabled := cfg.Notify.Channels["feishu"]
	feishuEnabled = feishuEnabled && feishuCfg.Enabled
	giteaEnabled = giteaEnabled && giteaCh.Enabled

	if !giteaEnabled && !feishuEnabled {
		return nil, nil
	}

	var giteaNotifier notify.Notifier
	if giteaEnabled && giteaClient != nil {
		gn, err := notify.NewGiteaNotifier(&giteaCommentAdapter{client: giteaClient}, notify.WithLogger(slog.Default()))
		if err != nil {
			return nil, fmt.Errorf("构造 GiteaNotifier 失败: %w", err)
		}
		giteaNotifier = gn
	}

	// 按配置构造飞书通知器（可选）
	var feishuNotifier notify.Notifier
	if feishuEnabled {
		webhookURL := feishuCfg.Options[config.FeishuOptionWebhookURL]
		var feishuOpts []notify.FeishuOption
		if secret := feishuCfg.Options[config.FeishuOptionSecret]; secret != "" {
			feishuOpts = append(feishuOpts, notify.WithFeishuSecret(secret))
		}
		feishuOpts = append(feishuOpts, notify.WithFeishuLogger(slog.Default()))
		fn, err := notify.NewFeishuNotifier(webhookURL, feishuOpts...)
		if err != nil {
			return nil, fmt.Errorf("构造 FeishuNotifier 失败: %w", err)
		}
		feishuNotifier = fn
	}

	if giteaNotifier == nil && feishuNotifier == nil {
		return nil, nil
	}

	return &configDrivenNotifier{
		cfg:            cfg,
		giteaNotifier:  giteaNotifier,
		feishuNotifier: feishuNotifier,
		logger:         slog.Default(),
	}, nil
}
