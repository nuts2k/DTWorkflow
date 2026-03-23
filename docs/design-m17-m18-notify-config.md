# M1.7 通知框架 + M1.8 配置管理 -- 并行开发详细设计方案

**日期**：2026-03-23
**状态**：已批准（Planner/Architect/Critic 三方共识）
**范围**：`internal/notify/`、`internal/config/`、`configs/`
**预估复杂度**：MEDIUM

---

## 1. RALPLAN-DR 摘要

### 1.1 设计原则

1. **与现有模式一致**：沿用项目已建立的 Functional Options、Interface-based handler、策略模式路由、结构化日志、类型化错误等模式，降低认知负担。
2. **接口先行、实现后行**：M1.7 与 M1.8 之间通过明确的 Go interface 契约解耦，支持并行开发且各自可独立测试。
3. **最小可用、可扩展**：初期只实现 Gitea 评论通知器和全局+仓库级 YAML 配置；架构预留扩展点（新通知渠道、新配置源），但不提前实现。
4. **配置即数据、代码即行为**：通知路由规则属于"数据"，由 M1.8 配置管理提供；通知发送逻辑属于"行为"，由 M1.7 通知框架实现。两者通过共享类型契约衔接。
5. **可测试性优先**：所有外部依赖（Gitea API 调用、文件系统、Viper）均通过 interface 注入，便于 stub/mock 测试。

### 1.2 决策驱动因素（Top 3）

| 排序 | 驱动因素 | 说明 |
|------|---------|------|
| 1 | **并行开发可行性** | 两个模块必须可以由不同开发者（或不同时间段）独立开发，不产生合并冲突 |
| 2 | **与现有代码风格的一致性** | 已有 `gitea/`、`webhook/` 两个包建立了清晰的模式，新包必须遵循，不引入新的架构概念 |
| 3 | **后续 Phase 2/3 的消费者友好性** | M1.7 的 Notifier 接口将被 PR 评审（Phase 2）和 Issue 修复（Phase 3）直接调用，接口设计需满足这些场景 |

### 1.3 可行方案对比

#### 方案 A：包内 interface + 集成层映射（推荐）

**描述**：在 `internal/notify/` 包中定义核心 interface 和事件类型，M1.8 的 `internal/config/` 包定义配置结构体（包含通知路由配置）。两者通过 Go 类型系统天然衔接，无需额外共享包。

**优点**：
- 无新的架构概念，完全复用现有模式
- `notify.Notifier` interface 是 M1.7 自身的包内定义，M1.8 无需导入 notify 包
- 配置结构体是纯数据，不引入行为依赖
- 集成时只需一个胶水函数将 config 中的通知配置映射为 notify 包的路由规则

**缺点**：
- 集成阶段需编写映射代码（量小，约 30-50 行）
- 配置结构体中的通知相关字段名需事先约定

#### 方案 B：独立共享契约包 `internal/contract/`（排除）

**描述**：抽取 `internal/contract/` 包存放所有跨模块共享的 interface 和类型。

**排除理由**：
- 引入了现有代码库不存在的新架构概念（contract 包）
- 增加了间接层，违反 Go 社区"在消费者侧定义 interface"的最佳实践
- 对当前只有两个模块交互的场景而言过度设计
- 现有 `webhook/` 包已经在自身包内定义 `Handler` interface，不依赖任何 contract 包
- 本项目规模（<10 个 internal 包）不需要全局契约层

**结论**：采用方案 A。

---

## 2. M1.7 通知框架详细设计

### 2.1 包结构与文件清单

```
internal/notify/
  notifier.go          # Notifier interface + NotifyEvent 类型定义
  errors.go            # 类型化错误（ErrNotifierNotFound, ErrSendFailed 等）
  gitea_notifier.go    # GiteaNotifier 实现（通过 Gitea API 发评论）
  router.go            # Router 通知路由器（按仓库/事件类型分发到对应 Notifier）
  router_test.go       # Router 单元测试
  gitea_notifier_test.go  # GiteaNotifier 单元测试
  notifier_test.go     # 接口契约测试（验证 mock 实现行为正确性）
```

### 2.2 核心 Interface 定义

```go
package notify

import "context"

// EventType 通知事件类型
type EventType string

const (
    EventPRReviewDone       EventType = "pr.review.done"        // PR 评审完成
    EventPRRejected         EventType = "pr.rejected"           // PR 被自动打回
    EventIssueAnalysisDone  EventType = "issue.analysis.done"   // Issue 分析完成
    EventIssueNeedInfo      EventType = "issue.need_info"       // Issue 信息不足追问
    EventFixPRCreated       EventType = "fix.pr.created"        // 修复 PR 已创建
    EventE2ETestFailed      EventType = "e2e.test.failed"       // E2E 测试失败
    EventSystemError        EventType = "system.error"          // 系统异常
)

// Severity 通知紧急程度
type Severity string

const (
    SeverityInfo     Severity = "info"
    SeverityWarning  Severity = "warning"
    SeverityCritical Severity = "critical"
)

// Target 通知目标（在哪个仓库的哪个 Issue/PR 上发通知）
type Target struct {
    Owner  string // 仓库所有者
    Repo   string // 仓库名
    Number int64  // Issue 或 PR 编号（0 表示非特定条目）
    IsPR   bool   // true=PR, false=Issue
}

// Message 通知消息体
type Message struct {
    EventType EventType         // 事件类型
    Severity  Severity          // 紧急程度
    Target    Target            // 通知目标
    Title     string            // 通知标题（简短摘要）
    Body      string            // 通知正文（Markdown 格式）
    Metadata  map[string]string // 附加元数据（供渠道适配使用）
}

// Notifier 通知渠道接口（策略模式）
// 每种通知渠道（Gitea 评论、企业微信、钉钉等）实现此接口
type Notifier interface {
    // Name 返回通知渠道名称（如 "gitea", "wecom", "dingtalk"）
    Name() string

    // Send 发送通知。成功返回 nil，失败返回错误。
    // 实现方应使用 context 控制超时和取消。
    Send(ctx context.Context, msg Message) error
}
```

### 2.3 GiteaNotifier 实现方案

```go
package notify

import (
    "context"
    "fmt"
    "log/slog"
)

// GiteaCommentCreator Gitea 评论创建接口（依赖倒置，不直接依赖 gitea.Client）
// 这是 M1.7 对 Gitea API 的最小依赖面：只需要"创建评论"这一个能力。
type GiteaCommentCreator interface {
    CreateIssueComment(ctx context.Context, owner, repo string, index int64, body string) error
}

// GiteaNotifier 通过 Gitea Issue/PR 评论发送通知
type GiteaNotifier struct {
    client GiteaCommentCreator
    logger *slog.Logger
}

// GiteaNotifierOption 配置选项
type GiteaNotifierOption func(*GiteaNotifier) error

// NewGiteaNotifier 创建 Gitea 通知器
func NewGiteaNotifier(client GiteaCommentCreator, opts ...GiteaNotifierOption) (*GiteaNotifier, error) {
    // ...Functional Options 初始化...
}

func (n *GiteaNotifier) Name() string { return "gitea" }

func (n *GiteaNotifier) Send(ctx context.Context, msg Message) error {
    if msg.Target.Number == 0 {
        return fmt.Errorf("gitea 通知器需要指定 Issue/PR 编号: %w", ErrInvalidTarget)
    }
    // 组装 Markdown 评论内容：标题 + 正文
    comment := formatGiteaComment(msg)
    // 调用 Gitea API 创建评论（Issue 和 PR 共用同一个评论 API）
    return n.client.CreateIssueComment(ctx, msg.Target.Owner, msg.Target.Repo, msg.Target.Number, comment)
}
```

**关键设计决策**：
- `GiteaCommentCreator` 是一个窄接口，只包含"创建评论"能力，签名经过简化：参数为 `body string` 而非 `gitea.CreateIssueCommentOption`，返回值为 `error` 而非 `(*Comment, *Response, error)`。这使得 `internal/notify` 包**无需 import `internal/gitea` 包**。
- 现有 `gitea.Client.CreateIssueComment` 的实际签名为：
  ```go
  func (c *Client) CreateIssueComment(ctx context.Context, owner, repo string, index int64, opts CreateIssueCommentOption) (*Comment, *Response, error)
  ```
  **签名不匹配**，`gitea.Client` 不能直接赋值给 `GiteaCommentCreator`。需要在集成层编写适配器（见 4.3 节）。
- 不直接 import `internal/gitea` 包，通过 interface 解耦，便于单元测试时使用 stub。
- Gitea 的 Issue 评论 API 同时适用于 PR（Gitea 中 PR 也是一种 Issue），因此不需要区分 PR 评论和 Issue 评论的 API。

### 2.4 通知路由器（Router）设计

```go
package notify

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
)

// RoutingRule 路由规则：匹配条件 -> 通知渠道列表
type RoutingRule struct {
    RepoPattern string      // 仓库匹配模式（"*" 表示全部，"owner/repo" 精确匹配）
    EventTypes  []EventType // 匹配的事件类型（空切片表示全部）
    Channels    []string    // 目标通知渠道名称列表（对应 Notifier.Name()）
}

// Router 通知路由器：根据消息的仓库和事件类型，分发到匹配的 Notifier。
// Router 是顶层分发器，不实现 Notifier 接口。
type Router struct {
    notifiers map[string]Notifier // 渠道名 -> Notifier 实例
    rules     []RoutingRule       // 路由规则列表（按顺序匹配，全部匹配的规则都会生效）
    fallback  string              // 无规则匹配时的默认渠道（通常为 "gitea"）
    logger    *slog.Logger
}

// RouterOption 路由器配置选项
type RouterOption func(*Router) error

// NewRouter 创建通知路由器
func NewRouter(opts ...RouterOption) (*Router, error) {
    // ...初始化...
}

// WithNotifier 注册通知渠道
func WithNotifier(n Notifier) RouterOption {
    return func(r *Router) error {
        r.notifiers[n.Name()] = n
        return nil
    }
}

// WithRules 设置路由规则
func WithRules(rules []RoutingRule) RouterOption {
    return func(r *Router) error {
        r.rules = rules
        return nil
    }
}

// WithFallback 设置默认渠道
func WithFallback(channel string) RouterOption {
    return func(r *Router) error {
        r.fallback = channel
        return nil
    }
}

// Send 根据路由规则分发消息到匹配的 Notifier。
// 如果多个规则匹配，所有匹配渠道都会收到通知。
// 如果无规则匹配，使用 fallback 渠道。
// 策略：尝试发送所有匹配渠道，不因单个渠道失败而中止。
// 收集全部错误，通过 errors.Join 返回聚合错误。
func (r *Router) Send(ctx context.Context, msg Message) error {
    channels := r.resolveChannels(msg)
    var errs []error
    for _, ch := range channels {
        notifier, ok := r.notifiers[ch]
        if !ok {
            r.logger.Warn("路由规则引用了未注册的通知渠道", "channel", ch)
            errs = append(errs, fmt.Errorf("渠道 %q: %w", ch, ErrNotifierNotFound))
            continue
        }
        if err := notifier.Send(ctx, msg); err != nil {
            r.logger.Error("通知发送失败", "channel", ch, "event", msg.EventType, "error", err)
            errs = append(errs, fmt.Errorf("渠道 %q 发送失败: %w", ch, err))
            continue
        }
        r.logger.Info("通知发送成功", "channel", ch, "event", msg.EventType)
    }
    return errors.Join(errs...) // Go 1.20+；全部成功时返回 nil
}

// resolveChannels 解析消息应发送到的渠道列表（去重）
func (r *Router) resolveChannels(msg Message) []string {
    // 遍历 rules，匹配 RepoPattern 和 EventTypes
    // 无匹配则返回 [fallback]
}
```

**路由匹配逻辑**：
1. 遍历所有 `RoutingRule`，检查 `RepoPattern` 是否匹配 `msg.Target.Owner + "/" + msg.Target.Repo`
2. 检查 `EventTypes` 是否包含 `msg.EventType`（空切片视为匹配全部）
3. 收集所有匹配规则的 `Channels` 并去重
4. 若无匹配，使用 `fallback` 渠道
5. 逐一调用对应的 `Notifier.Send()`，单个失败不中止其余

**RepoPattern 匹配语义（v1）**：

初始版本只支持两种匹配模式：
- `"*"` -- 匹配所有仓库
- `"owner/repo"` -- 精确匹配指定仓库（完整的 `owner/repo` 字符串比较）

**不支持**部分通配符（如 `"myorg/*"` 匹配某个 owner 下的所有仓库）。如需此能力，将在后续版本中扩展 `resolveChannels()` 的匹配逻辑。配置中如果写了不支持的模式，匹配时将作为精确字符串比较处理。

### 2.5 与 M1.8 配置的接口契约

M1.7 **不直接依赖** M1.8 的 `config` 包。它们之间的契约是：

1. **M1.8 提供的配置数据结构**（纯数据，无行为）：
   ```yaml
   # 在全局配置中
   notify:
     default_channel: "gitea"
     channels:
       gitea:
         enabled: true
     routes:
       - repo: "*"
         events: ["pr.rejected", "system.error"]
         channels: ["gitea"]
       - repo: "owner/important-repo"
         events: ["*"]
         channels: ["gitea"]
   ```

2. **集成时的映射代码**（在 `internal/cmd/` 中编写，约 30-50 行）：
   - 读取 `config.NotifyConfig` 结构体
   - 转换为 `[]notify.RoutingRule`
   - 传入 `notify.NewRouter(notify.WithRules(rules), ...)`

这种方式使得 M1.7 和 M1.8 各自不需要 import 对方的包。

### 2.6 错误类型定义

```go
package notify

import "errors"

var (
    ErrNotifierNotFound = errors.New("通知渠道未注册")
    ErrInvalidTarget    = errors.New("无效的通知目标")
    ErrSendFailed       = errors.New("通知发送失败")
    ErrNoChannelMatched = errors.New("无匹配的通知渠道")
)
```

### 2.7 单元测试策略

| 测试文件 | 测试内容 | 方法 |
|---------|---------|------|
| `notifier_test.go` | 验证 Notifier interface 契约 | 编写 `stubNotifier`，验证 Name() 和 Send() 行为 |
| `gitea_notifier_test.go` | GiteaNotifier 评论组装和发送 | stub `GiteaCommentCreator`，验证调用参数正确性；验证 Target.Number==0 时返回 ErrInvalidTarget |
| `router_test.go` | 路由匹配逻辑 | 注册多个 stubNotifier + 多条规则，验证：精确仓库匹配、通配符匹配、事件类型过滤、无匹配时 fallback、多规则叠加去重、**多渠道中单个失败不阻塞其余渠道发送**、**返回的聚合错误包含所有失败渠道的错误信息** |

**测试覆盖目标**：>= 85%（与现有模块一致）

---

## 3. M1.8 配置管理详细设计

### 3.1 包结构与文件清单

```
internal/config/
  config.go            # 全局配置结构体定义 + Load/Reload 函数
  validate.go          # 配置校验逻辑
  repo_config.go       # 仓库级配置覆盖机制
  watcher.go           # 配置热加载（Viper WatchConfig 封装）
  errors.go            # 类型化错误
  config_test.go       # 配置加载单元测试
  validate_test.go     # 配置校验单元测试
  repo_config_test.go  # 仓库级配置测试
  watcher_test.go      # 热加载测试

configs/
  dtworkflow.example.yaml  # 更新现有模板（补充 notify + 仓库级配置示例）
```

### 3.2 全局配置结构体

```go
package config

import "time"

// Config 全局配置（与 YAML 结构一一对应）
type Config struct {
    Server   ServerConfig   `mapstructure:"server"`
    Gitea    GiteaConfig    `mapstructure:"gitea"`
    Redis    RedisConfig    `mapstructure:"redis"`
    Database DatabaseConfig `mapstructure:"database"`
    Log      LogConfig      `mapstructure:"log"`
    Worker   WorkerConfig   `mapstructure:"worker"`
    Webhook  WebhookConfig  `mapstructure:"webhook"`
    Notify   NotifyConfig   `mapstructure:"notify"`
    Repos    []RepoConfig   `mapstructure:"repos"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
    Host string `mapstructure:"host"`
    Port int    `mapstructure:"port"`
}

// GiteaConfig Gitea 连接配置
type GiteaConfig struct {
    URL                string `mapstructure:"url"`
    Token              string `mapstructure:"token"`
    InsecureSkipVerify bool   `mapstructure:"insecure_skip_verify"`
}

// RedisConfig Redis 连接配置
type RedisConfig struct {
    Addr     string `mapstructure:"addr"`
    Password string `mapstructure:"password"`
    DB       int    `mapstructure:"db"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
    Path string `mapstructure:"path"`
}

// LogConfig 日志配置
type LogConfig struct {
    Level  string `mapstructure:"level"`  // debug / info / warn / error
    Format string `mapstructure:"format"` // text / json
}

// WorkerConfig Worker 池配置
type WorkerConfig struct {
    Concurrency int           `mapstructure:"concurrency"`
    Timeout     time.Duration `mapstructure:"timeout"`
}

// WebhookConfig Webhook 配置
type WebhookConfig struct {
    Secret string `mapstructure:"secret"`
}

// NotifyConfig 通知框架配置
type NotifyConfig struct {
    DefaultChannel string                  `mapstructure:"default_channel"`
    Channels       map[string]ChannelConfig `mapstructure:"channels"`
    Routes         []RouteConfig            `mapstructure:"routes"`
}

// ChannelConfig 单个通知渠道配置
type ChannelConfig struct {
    Enabled bool              `mapstructure:"enabled"`
    Options map[string]string `mapstructure:"options"` // 渠道特定选项（预留扩展）
}

// RouteConfig 通知路由配置
type RouteConfig struct {
    Repo     string   `mapstructure:"repo"`     // 仓库匹配模式
    Events   []string `mapstructure:"events"`   // 事件类型列表
    Channels []string `mapstructure:"channels"` // 目标渠道列表
}

// RepoConfig 仓库级配置覆盖
type RepoConfig struct {
    Name   string          `mapstructure:"name"`             // "owner/repo" 格式
    Review *ReviewOverride `mapstructure:"review,omitempty"` // 评审配置覆盖（Phase 2 使用）
    Notify *NotifyOverride `mapstructure:"notify,omitempty"` // 通知配置覆盖
}

// ReviewOverride 仓库级评审配置覆盖
type ReviewOverride struct {
    Enabled        *bool    `mapstructure:"enabled,omitempty"`
    IgnorePatterns []string `mapstructure:"ignore_patterns,omitempty"` // 忽略的文件模式
    Severity       string   `mapstructure:"severity,omitempty"`        // 最低报告级别
}

// NotifyOverride 仓库级通知配置覆盖
type NotifyOverride struct {
    Routes []RouteConfig `mapstructure:"routes,omitempty"` // 覆盖全局路由规则
}
```

### 3.3 仓库级配置覆盖机制

```go
package config

// ResolveNotifyRoutes 解析指定仓库的最终通知路由规则。
// 优先使用仓库级覆盖，无覆盖时回退到全局配置。
// 覆盖策略：仓库级 NotifyOverride.Routes 非空时，完全替换全局路由（不合并）。
func (c *Config) ResolveNotifyRoutes(repoFullName string) []RouteConfig {
    for _, repo := range c.Repos {
        if repo.Name == repoFullName && repo.Notify != nil && len(repo.Notify.Routes) > 0 {
            return repo.Notify.Routes
        }
    }
    return c.Notify.Routes
}

// ResolveReviewConfig 解析指定仓库的最终评审配置（全局 + 仓库覆盖合并）。
// 仓库级配置中的非 nil 字段覆盖全局默认值（类似 CSS 继承）。
func (c *Config) ResolveReviewConfig(repoFullName string) ReviewOverride {
    // ...合并逻辑...
}
```

### 3.4 Viper 集成方案

```go
package config

import (
    "fmt"
    "log/slog"
    "sync"

    "github.com/fsnotify/fsnotify"
    "github.com/spf13/viper"
)

// Manager 配置管理器，封装 Viper 的加载、校验、热加载逻辑
type Manager struct {
    v       *viper.Viper
    mu      sync.RWMutex // 保护 current 的并发读写
    current *Config
    logger  *slog.Logger

    // onChange 配置变更回调（热加载时调用）
    // 重要约定：OnChange 必须在 WatchConfig() 之前调用，不可并发注册。
    onChange []func(old, new *Config)
}

// ManagerOption 管理器配置选项
type ManagerOption func(*Manager) error

// NewManager 创建配置管理器
func NewManager(opts ...ManagerOption) (*Manager, error) {
    // ...使用 viper.New() 避免全局状态...
}

// WithConfigFile 指定配置文件路径
func WithConfigFile(path string) ManagerOption {
    return func(m *Manager) error {
        m.v.SetConfigFile(path)
        return nil
    }
}

// WithDefaultSearchPaths 配置默认搜索路径
// 当 --config 未指定时，按以下顺序搜索：
// 1. 当前工作目录：./dtworkflow.yaml
// 2. 用户配置目录：$HOME/.config/dtworkflow/dtworkflow.yaml
// 3. 系统配置目录：/etc/dtworkflow/dtworkflow.yaml（仅 Linux）
func WithDefaultSearchPaths() ManagerOption {
    return func(m *Manager) error {
        m.v.SetConfigName("dtworkflow")
        m.v.SetConfigType("yaml")
        m.v.AddConfigPath(".")
        m.v.AddConfigPath("$HOME/.config/dtworkflow")
        m.v.AddConfigPath("/etc/dtworkflow")
        return nil
    }
}

// WithDefaults 设置默认值
func WithDefaults() ManagerOption {
    return func(m *Manager) error {
        m.v.SetDefault("server.host", "0.0.0.0")
        m.v.SetDefault("server.port", 8080)
        m.v.SetDefault("log.level", "info")
        m.v.SetDefault("log.format", "text")
        m.v.SetDefault("worker.concurrency", 3)
        m.v.SetDefault("worker.timeout", "30m")
        m.v.SetDefault("database.path", "data/dtworkflow.db")
        m.v.SetDefault("redis.addr", "localhost:6379")
        m.v.SetDefault("redis.db", 0)
        m.v.SetDefault("notify.default_channel", "gitea")
        return nil
    }
}

// WithEnvPrefix 设置环境变量前缀
func WithEnvPrefix(prefix string) ManagerOption {
    return func(m *Manager) error {
        m.v.SetEnvPrefix(prefix)
        m.v.AutomaticEnv()
        return nil
    }
}

// OnChange 注册配置变更回调。
// 重要约定：必须在 WatchConfig() 之前调用完毕，不可在 WatchConfig 运行期间并发调用。
func (m *Manager) OnChange(fn func(old, new *Config)) {
    m.onChange = append(m.onChange, fn)
}

// Viper 返回底层 Viper 实例（用于 BindPFlag 等高级操作）
func (m *Manager) Viper() *viper.Viper {
    return m.v
}

// SetConfigFile 设置配置文件路径（供 CLI --config flag 使用）
func (m *Manager) SetConfigFile(path string) {
    m.v.SetConfigFile(path)
}

// Load 加载配置文件并校验
func (m *Manager) Load() error {
    if err := m.v.ReadInConfig(); err != nil {
        return fmt.Errorf("读取配置文件: %w", err)
    }
    cfg := &Config{}
    if err := m.v.Unmarshal(cfg); err != nil {
        return fmt.Errorf("解析配置: %w", err)
    }
    if err := Validate(cfg); err != nil {
        return fmt.Errorf("配置校验失败: %w", err)
    }
    m.mu.Lock()
    m.current = cfg
    m.mu.Unlock()
    return nil
}

// Get 获取当前配置（线程安全）。
// 重要约定：返回的 *Config 指针指向的内容为只读，调用者不得修改。
// 如需修改配置，应通过配置文件变更 + 热加载机制实现。
// 直接修改返回的 *Config 会导致数据竞争和未定义行为。
func (m *Manager) Get() *Config {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.current
}

// WatchConfig 启动配置文件热加载监听
func (m *Manager) WatchConfig() {
    m.v.OnConfigChange(func(e fsnotify.Event) {
        // 重新加载 + 校验，校验失败则记录日志但不更新 current
        // 校验通过则更新 current 并调用 onChange 回调
    })
    m.v.WatchConfig()
}
```

**注意**：Viper 的 `WatchConfig()` 依赖 `fsnotify`，这是纯 Go 实现，无 CGO 依赖。

#### 3.4.1 热加载生效范围

`WatchConfig()` 热加载更新 `Manager.current` 后，不同配置项的生效方式不同：

| 类别 | 配置项 | 热加载后是否立即生效 | 说明 |
|------|--------|---------------------|------|
| **可热加载** | `log.level` | 是 | 通过 `OnChange` 回调动态调整 slog Level |
| **可热加载** | `notify.default_channel` | 是 | Router 发送时每次从 Config 读取 fallback |
| **可热加载** | `notify.channels.*.enabled` | 是 | 渠道启用/禁用在发送时动态判断 |
| **需要重启** | `server.host` / `server.port` | 否 | HTTP listener 在启动时绑定，运行中不可更换 |
| **需要重启** | `gitea.url` / `gitea.token` | 否 | Gitea Client 在启动时构造，持有固定的 base URL 和 token |
| **需要重启** | `redis.*` | 否 | Redis 连接在启动时建立 |
| **需要重启** | `database.path` | 否 | SQLite 数据库文件在启动时打开 |
| **需要重启** | `webhook.secret` | 否 | SignatureVerifier 在启动时构造，持有固定的 secret |
| **需要重启** | `notify.routes` | 否 | Router 路由规则的初始版本不支持热加载。Router 的 `rules` 在构造时通过 `WithRules()` 传入，运行期间不会自动更新。后续版本如需支持，需为 Router 增加 `UpdateRules()` 方法并在 `OnChange` 回调中调用。 |

**设计约束**：初始版本（M1.7/M1.8）只对日志级别和部分通知开关支持热加载。路由规则和基础设施连接参数的变更需要重启服务。这是有意的简化，避免引入复杂的运行时重建逻辑。

### 3.5 配置校验策略

```go
package config

import "fmt"

// Validate 校验配置的完整性和合法性
func Validate(cfg *Config) error {
    var errs []error

    // 必填项校验
    if cfg.Gitea.URL == "" {
        errs = append(errs, fmt.Errorf("gitea.url 不能为空"))
    }
    if cfg.Gitea.Token == "" {
        errs = append(errs, fmt.Errorf("gitea.token 不能为空"))
    }
    if cfg.Webhook.Secret == "" {
        errs = append(errs, fmt.Errorf("webhook.secret 不能为空"))
    }

    // 范围校验
    if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
        errs = append(errs, fmt.Errorf("server.port 必须在 1-65535 之间，当前值: %d", cfg.Server.Port))
    }
    if cfg.Worker.Concurrency < 1 {
        errs = append(errs, fmt.Errorf("worker.concurrency 必须 >= 1，当前值: %d", cfg.Worker.Concurrency))
    }

    // 日志级别校验
    validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
    if !validLevels[cfg.Log.Level] {
        errs = append(errs, fmt.Errorf("log.level 必须是 debug/info/warn/error 之一，当前值: %q", cfg.Log.Level))
    }

    // 通知渠道校验
    if cfg.Notify.DefaultChannel != "" {
        if ch, ok := cfg.Notify.Channels[cfg.Notify.DefaultChannel]; !ok || !ch.Enabled {
            errs = append(errs, fmt.Errorf("notify.default_channel %q 未配置或未启用", cfg.Notify.DefaultChannel))
        }
    }

    // 路由规则中引用的渠道必须存在
    for i, route := range cfg.Notify.Routes {
        for _, ch := range route.Channels {
            if _, ok := cfg.Notify.Channels[ch]; !ok {
                errs = append(errs, fmt.Errorf("notify.routes[%d] 引用了未配置的渠道: %q", i, ch))
            }
        }
    }

    // 仓库配置校验
    for i, repo := range cfg.Repos {
        if repo.Name == "" {
            errs = append(errs, fmt.Errorf("repos[%d].name 不能为空", i))
        }
    }

    if len(errs) > 0 {
        return &ValidationError{Errors: errs}
    }
    return nil
}

// ValidationError 配置校验聚合错误
type ValidationError struct {
    Errors []error
}

func (e *ValidationError) Error() string {
    // 格式化输出所有校验错误
}
```

### 3.6 与现有 CLI flags 的集成

#### 3.6.1 使用 PersistentPreRunE 加载配置

配置加载统一在 `rootCmd.PersistentPreRunE` 中完成（不在 `init()` 中，因为 `init()` 在命令解析前执行，此时 flag 值尚未就绪）。

```go
// internal/cmd/root.go

var cfgManager *config.Manager

func init() {
    rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "以 JSON 格式输出")
    rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "配置文件路径")
    rootCmd.PersistentFlags().BoolVarP(&verboseOutput, "verbose", "v", false, "详细日志输出")

    rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
        // config-optional 命令不需要加载配置文件
        if isConfigOptionalCmd(cmd) {
            return nil
        }

        mgr, err := config.NewManager(
            config.WithDefaults(),
            config.WithDefaultSearchPaths(),
            config.WithEnvPrefix("DTWORKFLOW"),
        )
        if err != nil {
            return err
        }

        // --config flag 优先；无则按默认搜索路径
        if configFile != "" {
            mgr.SetConfigFile(configFile)
        }

        // CLI flags 绑定到 Viper（使 flag 值优先于配置文件）
        v := mgr.Viper()
        _ = v.BindPFlag("server.host", serveCmd.Flags().Lookup("host"))
        _ = v.BindPFlag("server.port", serveCmd.Flags().Lookup("port"))
        _ = v.BindPFlag("webhook.secret", serveCmd.Flags().Lookup("webhook-secret"))

        if err := mgr.Load(); err != nil {
            return err
        }

        cfgManager = mgr
        return nil
    }
}

// isConfigOptionalCmd 判断命令是否不需要配置文件
func isConfigOptionalCmd(cmd *cobra.Command) bool {
    switch cmd.Name() {
    case "version", "help":
        return true
    }
    return false
}
```

#### 3.6.2 config-optional 命令清单

以下命令不需要配置文件即可运行：
- `version` -- 显示版本信息，无外部依赖
- `help` -- Cobra 内置帮助命令，无外部依赖

所有其他命令（`serve`、`review-pr`、`fix-issue`、`gen-tests`、`task`）均需要配置文件。

#### 3.6.3 现有 serve.go 包级变量迁移方案

现有 `serve.go` 中定义了三个包级变量：
```go
var (
    serveHost          string
    servePort          int
    serveWebhookSecret string
)
```

迁移策略为**渐进式**：
1. **Phase 1（本次 M1.8）**：保留包级变量和 `init()` 中的 flag 注册不变。在 `PersistentPreRunE` 中通过 `viper.BindPFlag` 将这些 flag 绑定到 Viper。`runServe()` 中从 `cfgManager.Get()` 读取配置值，不再直接读包级变量。
2. **Phase 2（后续清理）**：当所有命令都迁移到 config.Manager 后，移除包级变量，flag 注册改为直接在 `PersistentPreRunE` 或命令的 `PreRunE` 中完成。

Phase 1 中 `runServe()` 的改动示意：
```go
func runServe(cmd *cobra.Command, args []string) error {
    cfg := cfgManager.Get()

    // 从统一配置中获取值（CLI flag > 环境变量 > 配置文件 > 默认值）
    host := cfg.Server.Host
    port := cfg.Server.Port
    secret := cfg.Webhook.Secret

    if secret == "" {
        return fmt.Errorf("webhook.secret 不能为空（通过 --webhook-secret flag、DTWORKFLOW_WEBHOOK_SECRET 环境变量或配置文件设置）")
    }
    // ... 后续逻辑使用 host, port, secret ...
}
```

#### 3.6.4 优先级规则（从高到低）

1. CLI flags（`--host`, `--port`, `--webhook-secret` 等）
2. 环境变量（`DTWORKFLOW_SERVER_HOST`, `DTWORKFLOW_WEBHOOK_SECRET` 等）
3. 配置文件（`dtworkflow.yaml`）
4. 默认值（`WithDefaults()` 中设定）

#### 3.6.5 配置文件默认搜索路径

当 `--config` flag 未指定时，`config.Manager` 按以下顺序搜索配置文件：

1. 当前工作目录：`./dtworkflow.yaml`
2. 用户配置目录：`$HOME/.config/dtworkflow/dtworkflow.yaml`
3. 系统配置目录：`/etc/dtworkflow/dtworkflow.yaml`（仅 Linux）

找到第一个存在的文件即加载，全部不存在则返回错误（config-optional 命令除外）。

### 3.7 配置文件模板（YAML 示例）

```yaml
# DTWorkflow 配置文件模板
# 复制为 dtworkflow.yaml 后修改

# 服务器配置
server:
  host: "0.0.0.0"
  port: 8080

# Gitea 配置
gitea:
  url: "https://your-gitea-instance.com"
  token: ""
  insecure_skip_verify: false

# Webhook 配置
webhook:
  secret: ""

# Redis 配置（任务队列使用）
redis:
  addr: "localhost:6379"
  password: ""
  db: 0

# 数据库配置（SQLite，纯 Go 实现）
database:
  path: "data/dtworkflow.db"

# 日志配置
log:
  level: "info"    # debug / info / warn / error
  format: "text"   # text / json

# Worker 池配置
worker:
  concurrency: 3
  timeout: "30m"

# 通知框架配置
notify:
  # 无路由规则匹配时的默认通知渠道
  default_channel: "gitea"

  # 通知渠道定义
  channels:
    gitea:
      enabled: true
    # 后续扩展示例（当前不实现）：
    # wecom:
    #   enabled: false
    #   options:
    #     webhook_url: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx"

  # 通知路由规则（按顺序匹配，所有匹配的规则都会生效）
  routes:
    # PR 被自动打回 -> Gitea 评论
    - repo: "*"
      events: ["pr.rejected"]
      channels: ["gitea"]
    # 系统异常 -> Gitea（后续可加企业微信等）
    - repo: "*"
      events: ["system.error"]
      channels: ["gitea"]
    # 重要仓库的所有事件都通知
    - repo: "myorg/core-service"
      events: ["*"]
      channels: ["gitea"]

# 仓库级配置覆盖
repos:
  - name: "myorg/core-service"
    review:
      enabled: true
      ignore_patterns:
        - "**/*_generated.go"
        - "**/vendor/**"
      severity: "WARNING"  # 最低报告级别
    notify:
      routes:
        - repo: "myorg/core-service"
          events: ["*"]
          channels: ["gitea"]

  - name: "myorg/frontend-app"
    review:
      enabled: true
      ignore_patterns:
        - "dist/**"
        - "node_modules/**"
```

### 3.8 单元测试策略

| 测试文件 | 测试内容 | 方法 |
|---------|---------|------|
| `config_test.go` | 配置加载：从 YAML 文件加载、默认值填充、环境变量覆盖 | 使用临时文件写入 YAML，通过 Manager.Load() 加载后验证各字段值 |
| `validate_test.go` | 配置校验：必填项缺失、范围越界、渠道引用不存在、正常配置通过 | 构造各种 Config 实例，调用 Validate()，检查 ValidationError 内容 |
| `repo_config_test.go` | 仓库覆盖：有覆盖时返回仓库配置、无覆盖时回退全局、空仓库列表 | 构造 Config 实例，调用 ResolveNotifyRoutes() 和 ResolveReviewConfig() |
| `watcher_test.go` | 热加载：配置文件变更后 Get() 返回新值、无效配置变更不生效、OnChange 回调触发 | 使用临时文件 + 修改文件内容，通过 OnChange 回调同步等待或设置最大等待时间 + 轮询验证 |

**测试覆盖目标**：>= 85%

### 3.9 webhook.Config 与 config.WebhookConfig 的关系

项目中存在两个包含 `Secret` 字段的结构体，它们职责不同、层次不同：

| 结构体 | 所在包 | 职责 | 生命周期 |
|--------|--------|------|---------|
| `config.WebhookConfig` | `internal/config/` | **数据源**：从 YAML 配置文件反序列化而来，是配置管理层的纯数据结构体 | 随配置文件加载/热加载而更新 |
| `webhook.Config` | `internal/webhook/` | **运行时装配结构体**：在 `serve.go` 中将各来源的值组装为 webhook 包所需的完整运行时配置 | 在 `runServe()` 中一次性构造，传入 `webhook.RegisterRoutes()` |

**数据流向**（在 `internal/cmd/serve.go` 中）：

```go
// serve.go 中的装配逻辑
cfg := cfgManager.Get()  // 从 config.Manager 获取全局配置

// config.WebhookConfig（数据源） -> webhook.Config（运行时装配）
webhook.RegisterRoutes(router, webhook.Config{
    Secret:  cfg.Webhook.Secret,   // 来自配置文件的 config.WebhookConfig.Secret
    Handler: actualHandler,         // 运行时构造的 Handler 实例
})
```

**设计原则**：`webhook.Config` 是 `webhook` 包的内部契约，包含行为依赖（`Handler` 接口）。`config.WebhookConfig` 是纯数据结构，不包含行为。两者通过 `serve.go` 中的装配代码桥接，各自保持独立性。

---

## 4. 并行开发计划

### 4.1 接口契约定义（先做，约 0.5 天）

在正式并行开发前，两个模块的开发者需共同确认以下契约：

**已确认的共享类型**（无需额外包，纯约定）：

| M1.8 配置侧 | M1.7 通知侧 | 映射关系 |
|-------------|-------------|---------|
| `config.RouteConfig.Repo` (string) | `notify.RoutingRule.RepoPattern` (string) | 直接赋值 |
| `config.RouteConfig.Events` ([]string) | `notify.RoutingRule.EventTypes` ([]EventType) | string -> EventType 类型转换 |
| `config.RouteConfig.Channels` ([]string) | `notify.RoutingRule.Channels` ([]string) | 直接赋值 |
| `config.NotifyConfig.DefaultChannel` (string) | `notify.Router` fallback (string) | 直接赋值 |

**约定事项**：
1. 事件类型字符串格式：`"<domain>.<action>"` 如 `"pr.rejected"`，`"*"` 表示全部
2. 仓库匹配格式：`"owner/repo"` 精确匹配，`"*"` 匹配全部
3. 渠道名称：小写字母，如 `"gitea"`, `"wecom"`, `"dingtalk"`

### 4.2 各自独立开发的任务分解

#### M1.7 通知框架（可独立开发，约 2-3 天）

| 步骤 | 任务 | 验收标准 |
|------|------|---------|
| 1 | 定义 `EventType`、`Severity`、`Target`、`Message`、`Notifier` interface | 类型编译通过，godoc 注释完整 |
| 2 | 定义错误类型 `errors.go` | 所有错误变量可被 `errors.Is()` 匹配 |
| 3 | 实现 `GiteaNotifier`（含 `GiteaCommentCreator` 窄接口） | 单元测试通过，覆盖：正常发送、Target.Number==0 错误、API 调用失败错误包装 |
| 4 | 实现 `Router`（含路由匹配逻辑 + 聚合错误处理） | 单元测试通过，覆盖：精确匹配、通配符匹配、事件过滤、fallback、多渠道去重、单渠道失败不阻塞、聚合错误 |
| 5 | 编写文档注释 | 每个导出类型和函数有中文 godoc 注释 |

#### M1.8 配置管理（可独立开发，约 2-3 天）

| 步骤 | 任务 | 验收标准 |
|------|------|---------|
| 1 | 定义全局 `Config` 结构体及所有子结构体（含 `mapstructure` tag） | 结构体可被 Viper Unmarshal，godoc 注释完整 |
| 2 | 实现 `Manager`（NewManager、Load、Get）+ Viper 初始化 | 从 YAML 文件加载配置通过，默认值填充正确，环境变量覆盖生效 |
| 3 | 实现 `Validate()` 配置校验 | 单元测试通过，覆盖：必填项缺失、范围越界、渠道引用错误、正常通过 |
| 4 | 实现仓库级配置覆盖 `ResolveNotifyRoutes()` + `ResolveReviewConfig()` | 单元测试通过，覆盖：有覆盖、无覆盖、空列表 |
| 5 | 实现 `WatchConfig()` 热加载 | 单元测试通过，覆盖：文件变更后配置更新、无效变更不影响当前配置、回调触发 |
| 6 | 更新 `configs/dtworkflow.example.yaml` | 新模板包含所有配置项及注释说明 |

### 4.3 集成阶段的任务（约 1 天）

| 步骤 | 任务 | 验收标准 |
|------|------|---------|
| 1 | 编写 `gitea.Client` -> `notify.GiteaCommentCreator` 适配器 | 适配器编译通过 + 单元测试验证调用转发正确性（参数映射、错误传播） |
| 2 | 编写 `config.NotifyConfig` -> `[]notify.RoutingRule` 映射函数 | 映射函数单元测试通过，约 30-50 行 |
| 3 | 在 `internal/cmd/root.go` 中集成 `config.Manager` | `--config` flag 正确加载配置文件，`serve` 命令使用配置中的参数 |
| 4 | 在 `internal/cmd/serve.go` 中集成通知路由器 | `serve` 启动时根据配置初始化 Router + GiteaNotifier |
| 5 | 添加 `go.sum` 更新（Viper 依赖） | `go mod tidy` 通过 |

**步骤 1 适配器代码草案**：

```go
// 文件：internal/cmd/adapter.go
package cmd

import (
    "context"

    "otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
)

// giteaCommentAdapter 将 gitea.Client 适配为 notify.GiteaCommentCreator 窄接口。
//
// notify 包定义的窄接口签名为：
//   CreateIssueComment(ctx, owner, repo string, index int64, body string) error
//
// 而 gitea.Client 的实际签名为：
//   CreateIssueComment(ctx, owner, repo string, index int64, opts CreateIssueCommentOption) (*Comment, *Response, error)
//
// 适配器负责：
// (a) 将 body string 包装为 CreateIssueCommentOption{Body: body}
// (b) 丢弃 *Comment 和 *Response 返回值，只保留 error
type giteaCommentAdapter struct {
    client *gitea.Client
}

func (a *giteaCommentAdapter) CreateIssueComment(ctx context.Context, owner, repo string, index int64, body string) error {
    _, _, err := a.client.CreateIssueComment(ctx, owner, repo, index, gitea.CreateIssueCommentOption{
        Body: body,
    })
    return err
}
```

### 4.4 依赖关系图

```
                 契约定义（0.5 天）
                /                  \
               v                    v
    M1.7 通知框架              M1.8 配置管理
    （2-3 天，独立）            （2-3 天，独立）
               \                    /
                v                  v
              集成阶段（1 天）
                      |
                      v
               验收测试（0.5 天）
```

**关键路径**：契约定义 -> max(M1.7, M1.8) -> 集成 -> 验收
**最短工期**：并行开发时约 4-5 天
**最长工期**：串行开发时约 6-7 天

---

## 5. 验收标准

### 5.1 M1.7 通知框架验收标准

| 编号 | 验收标准 | 验证方法 |
|------|---------|---------|
| N-1 | `Notifier` interface 定义完整，包含 `Name()` 和 `Send()` 两个方法 | 代码审查 |
| N-2 | `GiteaNotifier` 可通过 stub 的 `GiteaCommentCreator` 发送评论 | 单元测试：传入 stubCreator，调用 Send()，验证 stubCreator 收到正确的 owner/repo/number/body |
| N-3 | `GiteaNotifier` 在 Target.Number == 0 时返回 `ErrInvalidTarget` | 单元测试 |
| N-4 | `Router` 精确仓库匹配正确 | 单元测试：配置 `repo: "owner/repo"` 规则，验证匹配 `owner/repo` 的消息走对应渠道 |
| N-5 | `Router` 通配符 `"*"` 匹配所有仓库 | 单元测试 |
| N-6 | `Router` 事件类型过滤正确 | 单元测试：配置 `events: ["pr.rejected"]`，验证只有 `pr.rejected` 消息匹配 |
| N-7 | `Router` 无规则匹配时使用 fallback 渠道 | 单元测试 |
| N-8 | `Router` 多规则叠加时渠道去重 | 单元测试 |
| N-8a | `Router.Send()` 多渠道发送时，单个渠道失败不阻塞其余渠道，返回聚合错误 | 单元测试：注册 2 个 stubNotifier，第一个返回 error，第二个返回 nil；验证两个 stub 均被调用，返回的聚合错误包含第一个渠道的错误 |
| N-9 | 所有 7 种 EventType 常量定义完整 | 代码审查 |
| N-10 | 单元测试覆盖率 >= 85% | `go test -cover` |
| N-11 | `Router.Send()` 的路由规则引用了未注册的 Notifier 名称时，返回包含 `ErrNotifierNotFound` 的错误并记录警告日志 | 单元测试：配置 RoutingRule 的 Channels 包含 `"nonexistent"`，验证返回的 error 满足 `errors.Is(err, ErrNotifierNotFound)` |

### 5.2 M1.8 配置管理验收标准

| 编号 | 验收标准 | 验证方法 |
|------|---------|---------|
| C-1 | `Config` 结构体包含 Server/Gitea/Redis/Database/Log/Worker/Webhook/Notify/Repos 所有配置段 | 代码审查 |
| C-2 | `Manager.Load()` 可从 YAML 文件加载配置并正确填充结构体 | 单元测试：写入临时 YAML，Load()，Get() 验证值 |
| C-3 | 默认值正确填充（port=8080, concurrency=3 等） | 单元测试：空配置文件（仅必填项），验证默认值 |
| C-4 | 环境变量 `DTWORKFLOW_*` 可覆盖配置文件值 | 单元测试：设置环境变量，验证覆盖生效 |
| C-5 | `Validate()` 检测必填项缺失并返回 `ValidationError` | 单元测试：空 Config，验证 Gitea.URL/Token/Webhook.Secret 错误 |
| C-6 | `Validate()` 检测范围越界（port, concurrency） | 单元测试 |
| C-7 | `Validate()` 检测通知渠道引用错误 | 单元测试：routes 中引用不存在的 channel |
| C-8 | `ResolveNotifyRoutes()` 有仓库覆盖时返回仓库配置 | 单元测试 |
| C-9 | `ResolveNotifyRoutes()` 无覆盖时回退全局配置 | 单元测试 |
| C-10 | `WatchConfig()` 配置文件变更后 `Get()` 返回新值 | 单元测试：修改临时文件，通过 OnChange 回调或短暂轮询验证值更新 |
| C-11 | `WatchConfig()` 无效变更不影响当前配置 | 单元测试：写入不合法 YAML，验证 Get() 仍返回旧值 |
| C-12 | `configs/dtworkflow.example.yaml` 包含所有配置段和注释 | 代码审查 |
| C-13 | 单元测试覆盖率 >= 85% | `go test -cover` |

### 5.3 集成验收标准

| 编号 | 验收标准 | 验证方法 |
|------|---------|---------|
| I-1 | `dtworkflow serve --config dtworkflow.yaml` 启动时正确加载配置 | 手动测试：使用示例配置启动 serve |
| I-2 | `--host/--port/--webhook-secret` CLI flags 优先于配置文件值 | 手动测试：配置文件写 port=8080，命令行传 --port=9090，验证监听 9090 |
| I-3 | `config.RouteConfig` 正确映射为 `notify.RoutingRule` | 单元测试（映射函数测试） |
| I-4 | `giteaCommentAdapter` 正确适配 `gitea.Client` 到 `notify.GiteaCommentCreator` 接口 | 适配器编译通过 + 单元测试验证：(1) `body` 参数正确包装为 `CreateIssueCommentOption{Body: body}`；(2) `gitea.Client` 返回的 error 正确传播；(3) 返回 nil error 时适配器也返回 nil |
| I-5 | `go test ./...` 全部通过 | CI 验证 |
| I-6 | `go vet ./...` 无警告 | CI 验证 |
| I-7 | 新增依赖（Viper + fsnotify）均为纯 Go，无 CGO | 检查 `go.mod`，`CGO_ENABLED=0 go build` 通过 |

---

## 6. ADR（架构决策记录）

### ADR-001：通知框架与配置管理的解耦方式

**决策**：采用"包内 interface + 集成层映射"方式解耦 M1.7 和 M1.8，不引入共享契约包。

**驱动因素**：
1. 并行开发可行性（两个包不互相 import）
2. 与现有代码风格一致性（webhook 包已使用此模式）
3. Go 社区最佳实践（消费者侧定义 interface）

**考虑的替代方案**：
- 方案 B：共享契约包 `internal/contract/` -- 排除，引入不必要的架构复杂度

**选择理由**：方案 A 完全复用现有模式，无新架构概念，集成成本极低（30-50 行映射代码），且两个包可独立编译和测试。

**后果**：
- 正面：两个包完全独立，可并行开发，无循环依赖风险
- 负面：集成阶段需编写少量映射代码；配置字段名和通知类型名之间的对应关系依赖约定而非类型系统
- 缓解：通过集成测试验证映射正确性

### ADR-002：Viper 作为配置管理库

**决策**：使用 Viper 进行配置管理，搭配 `mapstructure` tag 映射到 Go 结构体。

**驱动因素**：
1. PRD 和 ROADMAP 明确指定使用 Viper
2. Viper 提供 YAML 解析、环境变量覆盖、CLI flag 绑定、热加载的完整方案
3. 纯 Go 实现，无 CGO 依赖

**考虑的替代方案**：
- 手动 `os.ReadFile` + `gopkg.in/yaml.v3` -- 排除，缺少环境变量覆盖和热加载能力
- koanf -- 排除，ROADMAP 已明确选型 Viper

**后果**：
- 正面：开箱即用的多源配置、热加载、Cobra 集成
- 负面：Viper 全局状态问题（通过封装 Manager 使用 `viper.New()` 规避）
- 注意：使用 `viper.New()` 而非全局 `viper.Get*` 函数，避免测试间状态污染

### ADR-003：GiteaCommentCreator 窄接口

**决策**：M1.7 的 `GiteaNotifier` 通过窄接口 `GiteaCommentCreator`（仅含 `CreateIssueComment` 一个方法）依赖 Gitea API，而非直接依赖 `gitea.Client` 结构体。

**驱动因素**：
1. 可测试性：单元测试只需 stub 一个方法
2. 最小依赖面：通知器只需要"发评论"能力
3. 解耦：`internal/notify` 不 import `internal/gitea`

**后果**：
- 正面：单元测试简单（stub 一个方法 vs 整个 Client）；包间无依赖
- 负面：`gitea.Client` 签名与窄接口不匹配（参数类型 `CreateIssueCommentOption` vs `string`，返回值 `(*Comment, *Response, error)` vs `error`），需在集成层编写适配器（约 15 行）进行参数包装和返回值裁剪
- 后续：如果未来通知器需要更多 Gitea API（如添加标签），扩展此接口即可
