# 仓库级飞书通知覆盖

> 日期：2026-04-15
> 状态：已完成（2026-04-15 实施，8 个原子提交）

## 背景

当前飞书通知只有一组全局 `webhook_url` + `secret`，对应单个飞书群机器人。实际使用中，不同仓库（项目）通常有各自的沟通群，需要将通知发送到不同的飞书群。

## 目标

- 支持仓库级飞书 Webhook 覆盖：不同仓库可配置不同的 `webhook_url` + `secret`，对应不同飞书群机器人
- 未配置覆盖的仓库自动回退全局飞书配置
- 完全向后兼容，不配置仓库级覆盖时行为不变

## 约束

- 仅针对飞书渠道，不做通用渠道覆盖机制
- 一个仓库只对应一个飞书群
- 仓库级覆盖的前提是全局飞书渠道已启用

## 设计

### 1. 配置结构变更

**新增 `FeishuOverride` 结构体**，添加到 `NotifyOverride`：

```go
// internal/config/config.go

type FeishuOverride struct {
    WebhookURL string `mapstructure:"webhook_url"`
    Secret     string `mapstructure:"secret"`
}
```

```go
// internal/config/repo_config.go

type NotifyOverride struct {
    Routes []RouteConfig  `mapstructure:"routes"`
    Feishu *FeishuOverride `mapstructure:"feishu"`
}
```

> 注意：仅使用 `mapstructure` tag，与现有 `NotifyOverride` / `ReviewOverride` 风格一致。

**YAML 配置示例**：

```yaml
# 全局飞书配置（已有）
notify:
  channels:
    feishu:
      enabled: true
      options:
        webhook_url: "https://open.feishu.cn/open-apis/bot/v2/hook/global-xxx"
        secret: "global-secret"

# 仓库级覆盖
repos:
  - name: "acme/frontend"
    notify:
      feishu:
        webhook_url: "https://open.feishu.cn/open-apis/bot/v2/hook/frontend-xxx"
        secret: "frontend-secret"
  - name: "acme/backend"
    notify:
      feishu:
        webhook_url: "https://open.feishu.cn/open-apis/bot/v2/hook/backend-xxx"
        secret: "backend-secret"
```

**语义**：

- `FeishuOverride` 为 `nil`（未配置）→ 回退全局飞书渠道
- `webhook_url` 必填，`secret` 可选（与全局飞书行为一致——无 secret 时不做签名校验）
- 仓库级覆盖不影响路由规则（路由照常走，只是飞书的发送目标变了）

### 2. 配置解析与回退逻辑

在 `repo_config.go` 中新增：

```go
// ResolveFeishuOverride 返回指定仓库的飞书配置覆盖。
// 返回 nil 表示使用全局配置。
func (c *Config) ResolveFeishuOverride(repoFullName string) *FeishuOverride {
    for _, repo := range c.Repos {
        if repo.Name == repoFullName && repo.Notify != nil && repo.Notify.Feishu != nil {
            return repo.Notify.Feishu
        }
    }
    return nil
}
```

### 3. Router 装配改造

**核心思路**：Router 构造后不可变（`router.go` 注释："Router 的配置在创建后不应被修改"）。不在发送时动态切换 notifier，而是在构造 per-repo Router 时注入仓库级 FeishuNotifier。复用已有的 `routers map[string]*notify.Router` 缓存。

**不需要新增 `feishuNotifiers` 缓存**——每个 per-repo Router 内部已持有正确的 feishu notifier 实例。

#### 3.1 `hasRepoNotifyOverride` 更新

当前仅检查 `Routes != nil`，需同时检查 `Feishu != nil`：

```go
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
```

> 这是关键改动。如果不更新此函数，仅配置了 `feishu` 覆盖但未覆盖 `routes` 的仓库会走全局 Router（内含全局 feishu notifier），覆盖完全失效。

#### 3.2 `newRouter` 注入仓库级 FeishuNotifier

```go
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

    // 仓库级飞书覆盖：构造专属 FeishuNotifier
    if override := n.cfg.ResolveFeishuOverride(repoFullName); override != nil {
        var feishuOpts []notify.FeishuOption
        if override.Secret != "" {
            feishuOpts = append(feishuOpts, notify.WithFeishuSecret(override.Secret))
        }
        feishuOpts = append(feishuOpts, notify.WithFeishuLogger(n.logger))
        fn, err := notify.NewFeishuNotifier(override.WebhookURL, feishuOpts...)
        if err != nil {
            return nil, fmt.Errorf("构造仓库 %s 飞书通知器失败: %w", repoFullName, err)
        }
        opts = append(opts, notify.WithNotifier(fn))
    } else if n.feishuNotifier != nil {
        // 无覆盖，使用全局
        opts = append(opts, notify.WithNotifier(n.feishuNotifier))
    }

    router, err := notify.NewRouter(opts...)
    if err != nil {
        return nil, fmt.Errorf("构造通知路由失败: %w", err)
    }
    return router, nil
}
```

**发送流程不变**：`configDrivenNotifier.Send()` → `getRouter(repo)` → `router.Send()`。区别仅在于 `getRouter` 为有覆盖的仓库构造了包含专属 feishu notifier 的 Router。

### 4. Clone 深拷贝补充

`Config.Clone()` 中 Repos 深拷贝需包含 `FeishuOverride`：

```go
// 在现有 repo.Notify 深拷贝逻辑中补充：
if repo.Notify != nil {
    notifyCopy := *repo.Notify
    if repo.Notify.Routes != nil {
        notifyCopy.Routes = append([]RouteConfig(nil), repo.Notify.Routes...)
    }
    if repo.Notify.Feishu != nil {
        feishuCopy := *repo.Notify.Feishu
        notifyCopy.Feishu = &feishuCopy
    }
    cloned.Repos[i].Notify = &notifyCopy
}
```

### 5. 配置校验

在 `validate.go` 中补充仓库级飞书覆盖的校验：

```go
for _, repo := range cfg.Repos {
    if repo.Notify == nil || repo.Notify.Feishu == nil {
        continue
    }
    f := repo.Notify.Feishu

    // 1. webhook_url 必填（feishu: {} 空配置视为无效）
    if strings.TrimSpace(f.WebhookURL) == "" {
        errs = append(errs, fmt.Errorf(
            "repos[%s].notify.feishu: webhook_url 不能为空", repo.Name))
        continue
    }

    // 2. webhook_url 格式校验（复用全局同款逻辑）
    if u, err := url.Parse(f.WebhookURL); err != nil ||
        (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
        errs = append(errs, fmt.Errorf(
            "repos[%s].notify.feishu.webhook_url 格式无效", repo.Name))
    }

    // 3. 全局飞书渠道必须已启用
    if feishuCfg, ok := cfg.Notify.Channels["feishu"]; !ok || !feishuCfg.Enabled {
        errs = append(errs, fmt.Errorf(
            "repos[%s].notify.feishu: 全局飞书渠道未启用，仓库级覆盖无效", repo.Name))
    }
}
```

> `secret` 不做强制校验——与全局飞书行为一致，无 secret 时不做签名校验。
> `feishu: {}` 空配置会被拒绝（webhook_url 为空），避免运行时构造 notifier 失败。

### 6. 改动文件总结

| 文件 | 改动 |
|---|---|
| `internal/config/config.go` | 新增 `FeishuOverride` 结构体，`Clone()` 补充 `FeishuOverride` 深拷贝 |
| `internal/config/repo_config.go` | `NotifyOverride` 加 `Feishu` 字段，新增 `ResolveFeishuOverride()` |
| `internal/config/validate.go` | 仓库级飞书校验（webhook_url 必填、URL 格式、全局启用前提） |
| `internal/cmd/serve_notify.go` | `hasRepoNotifyOverride` 加 `Feishu` 检查，`newRouter` 注入仓库级 FeishuNotifier |
| `configs/dtworkflow.example.yaml` | 补充仓库级飞书覆盖示例 |

### 不改动的部分

- **`internal/notify/` 包不需要改**——这是刻意的设计选择：FeishuNotifier 已支持按实例传入不同 URL+Secret，Router 构造时通过 `WithNotifier()` 注入即可，无需修改 Router 的发送机制
- Gitea notifier 不受影响

## 测试计划

| 测试 | 文件 | 覆盖点 |
|---|---|---|
| `ResolveFeishuOverride` 单元测试 | `repo_config_test.go` | 有覆盖/无覆盖/nil notify/空 Feishu |
| `hasRepoNotifyOverride` 单元测试 | `serve_notify_test.go` | 仅 Routes/仅 Feishu/两者都有/都无 |
| 校验规则测试 | `validate_test.go` | webhook_url 为空/格式无效/全局未启用/正常 |
| `Clone` 深拷贝测试 | `config_test.go` | 修改 Clone 后的 FeishuOverride 不影响原始对象 |
| `newRouter` 集成测试 | `serve_notify_test.go` | 有覆盖时使用仓库级 notifier / 无覆盖时使用全局 |

## 已知限制

- **配置热更新不即时生效**：与现有 Router 缓存一致，已缓存的 per-repo Router 不会因配置热加载而自动刷新。这是已有限制（`serve_notify.go:55-56` 注释），本次不解决
- **重复 repo name**：`Repos` 列表中若出现同名仓库，`ResolveFeishuOverride` 返回第一个匹配项。这是 `ResolveNotifyRoutes` / `ResolveReviewConfig` 的已有行为，保持一致

## 向后兼容性

完全兼容。不配置 `repos[].notify.feishu` 时，行为与当前版本完全一致。
