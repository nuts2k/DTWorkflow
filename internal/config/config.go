package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
)

// Config 全局配置（与 YAML 结构一一对应）。
//
// 说明：本任务实现配置校验与仓库级覆盖（仅 notify 覆盖 + review 结构预留）。
type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	Gitea       GiteaConfig       `mapstructure:"gitea"`
	Claude      ClaudeConfig      `mapstructure:"claude"`
	Redis       RedisConfig       `mapstructure:"redis"`
	Database    DatabaseConfig    `mapstructure:"database"`
	Log         LogConfig         `mapstructure:"log"`
	Worker      WorkerConfig      `mapstructure:"worker"`
	Webhook     WebhookConfig     `mapstructure:"webhook"`
	Notify      NotifyConfig      `mapstructure:"notify"`
	Review      ReviewOverride    `mapstructure:"review"`
	DailyReport DailyReportConfig `mapstructure:"daily_report"`
	API         APIConfig         `mapstructure:"api" yaml:"api"`
	Repos       []RepoConfig      `mapstructure:"repos"`
}

type APIConfig struct {
	Tokens []TokenConfig `mapstructure:"tokens" yaml:"tokens"`
}

type TokenConfig struct {
	Token    string `mapstructure:"token" yaml:"token"`
	Identity string `mapstructure:"identity" yaml:"identity"`
}

type ClaudeConfig struct {
	APIKey  string `mapstructure:"api_key"`
	BaseURL string `mapstructure:"base_url"` // 代理或自定义 API 端点，留空则使用官方地址
	Model   string `mapstructure:"model"`    // 使用的模型，默认 claude-sonnet-4-6
	Effort  string `mapstructure:"effort"`   // 推理强度：low / medium / high，默认 high
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

type GiteaConfig struct {
	URL                string `mapstructure:"url"`
	Token              string `mapstructure:"token"`
	InsecureSkipVerify bool   `mapstructure:"insecure_skip_verify"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type DatabaseConfig struct {
	Path string `mapstructure:"path"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

type WorkerConfig struct {
	Concurrency int           `mapstructure:"concurrency"`
	Timeout     time.Duration `mapstructure:"timeout"`

	Image            string `mapstructure:"image"`
	ImageFull        string `mapstructure:"image_full"`        // M3.4: 执行镜像（可选）
	MavenCacheVolume string `mapstructure:"maven_cache_volume"` // M3.5: Maven 缓存 named volume，挂载到 fix/gen_tests 容器
	CPULimit    string `mapstructure:"cpu_limit"`
	MemoryLimit string `mapstructure:"memory_limit"`
	NetworkName string `mapstructure:"network_name"`

	Timeouts      TaskTimeouts      `mapstructure:"timeouts"`       // 按任务类型的硬超时
	StreamMonitor StreamMonitorConf `mapstructure:"stream_monitor"` // 流式心跳监控配置
}

// TaskTimeouts 按任务类型配置硬超时，零值表示使用默认值
type TaskTimeouts struct {
	ReviewPR     time.Duration `mapstructure:"review_pr"`
	AnalyzeIssue time.Duration `mapstructure:"analyze_issue"` // M3.4: 默认 15m
	FixIssue     time.Duration `mapstructure:"fix_issue"`
	GenTests     time.Duration `mapstructure:"gen_tests"`
}

// StreamMonitorConf 流式心跳监控配置
type StreamMonitorConf struct {
	Enabled         bool          `mapstructure:"enabled"`
	ActivityTimeout time.Duration `mapstructure:"activity_timeout"`
}

type WebhookConfig struct {
	Secret string `mapstructure:"secret"`
}

// DailyReportConfig 每日评审统计报告配置
type DailyReportConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	Cron          string `mapstructure:"cron"`
	Timezone      string `mapstructure:"timezone"`
	SkipEmpty     bool   `mapstructure:"skip_empty"`
	FeishuWebhook string `mapstructure:"feishu_webhook"`
	FeishuSecret  string `mapstructure:"feishu_secret"`
}

type NotifyConfig struct {
	DefaultChannel string                   `mapstructure:"default_channel"`
	Channels       map[string]ChannelConfig `mapstructure:"channels"`
	Routes         []RouteConfig            `mapstructure:"routes"`
}

type ChannelConfig struct {
	Enabled bool              `mapstructure:"enabled"`
	Options map[string]string `mapstructure:"options"`
}

type RouteConfig struct {
	Repo     string   `mapstructure:"repo"`
	Events   []string `mapstructure:"events"`
	Channels []string `mapstructure:"channels"`
}

// FeishuOverride 仓库级飞书 Webhook 覆盖配置。
type FeishuOverride struct {
	WebhookURL string `mapstructure:"webhook_url"`
	Secret     string `mapstructure:"secret"`
}

// Manager 配置管理器，封装 Viper 的加载、校验与当前配置获取。
//
// 说明：
// - Load() 负责从数据源加载配置并校验，然后写入 current。
// - WatchConfig() 用于监听配置文件变更，在校验通过后更新 current，并触发回调。
//
// 重要约定：Get() 返回配置快照的深拷贝，调用者可安全读取和修改副本，不影响 Manager 内部状态。
type Manager struct {
	v       *viper.Viper
	viperMu sync.Mutex

	configFile            string
	useDefaultSearchPaths bool

	mu      sync.RWMutex
	current *Config

	onChangeMu sync.RWMutex
	onChange   []func(oldCfg, newCfg *Config)

	watchOnce sync.Once
	watchErr  error
	reloadMu  sync.Mutex

	stopCh   chan struct{}
	stopOnce sync.Once
}

type ManagerOption func(*Manager) error

// NewManager 创建配置管理器。
func NewManager(opts ...ManagerOption) (*Manager, error) {
	m := &Manager{
		v:      viper.New(),
		stopCh: make(chan struct{}),
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// WithConfigFile 指定配置文件路径。
//
// 注意：只有通过该 Option 显式指定配置文件时，Load() 才会读取配置文件。
func WithConfigFile(path string) ManagerOption {
	return func(m *Manager) error {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("配置文件路径不能为空")
		}
		m.SetConfigFile(path)
		return nil
	}
}

// WithDefaultSearchPaths 配置默认搜索路径。
//
// 当未通过 WithConfigFile/SetConfigFile 显式指定配置文件时，Load() 将按以下顺序搜索：
// 1) 当前工作目录：./dtworkflow.yaml
// 2) 用户配置目录：$HOME/.config/dtworkflow/dtworkflow.yaml
// 3) 系统配置目录：/etc/dtworkflow/dtworkflow.yaml
func WithDefaultSearchPaths() ManagerOption {
	return func(m *Manager) error {
		m.useDefaultSearchPaths = true
		m.v.SetConfigName("dtworkflow")
		m.v.SetConfigType("yaml")
		m.v.AddConfigPath(".")
		m.v.AddConfigPath("$HOME/.config/dtworkflow")
		m.v.AddConfigPath("/etc/dtworkflow")
		return nil
	}
}

// WithEnvPrefix 启用环境变量覆盖能力。
//
// 注意：只有在 NewManager 时传入该 Option，Load() 才会读取环境变量。
//
// 约定：支持 DTWORKFLOW_REDIS_ADDR 这种 key（点转下划线）。
func WithEnvPrefix(prefix string) ManagerOption {
	return func(m *Manager) error {
		m.v.SetEnvPrefix(prefix)
		m.v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
		m.v.AutomaticEnv()

		// 兼容旧环境变量名：历史上数据库路径使用 DTWORKFLOW_DB_PATH。
		// M1.8 统一为 DTWORKFLOW_DATABASE_PATH，但过渡期内仍需兼容旧变量，避免现有部署静默回退到默认数据库路径。
		if err := m.v.BindEnv("database.path", prefix+"_DATABASE_PATH", prefix+"_DB_PATH"); err != nil {
			return fmt.Errorf("绑定环境变量 database.path 失败: %w", err)
		}
		return nil
	}
}

// WithDefaults 注册默认值。
//
// 注意：只有在 NewManager 时传入该 Option，Load() 后的配置才会包含这些默认值。
func WithDefaults() ManagerOption {
	return func(m *Manager) error {
		m.v.SetDefault("server.host", "0.0.0.0")
		m.v.SetDefault("server.port", 8080)

		m.v.SetDefault("log.level", "info")
		m.v.SetDefault("log.format", "text")

		m.v.SetDefault("worker.concurrency", 3)
		m.v.SetDefault("worker.timeout", 30*time.Minute)
		m.v.SetDefault("worker.image", "dtworkflow-worker:1.0")
		m.v.SetDefault("worker.cpu_limit", "2.0")
		m.v.SetDefault("worker.memory_limit", "4g")
		m.v.SetDefault("worker.network_name", "dtworkflow-net")

		m.v.SetDefault("database.path", "data/dtworkflow.db")

		m.v.SetDefault("redis.addr", "localhost:6379")
		m.v.SetDefault("redis.db", 0)

		// 让环境变量覆盖可被 Unmarshal 感知：这些 key 必须出现在 viper 的 AllKeys() 中。
		m.v.SetDefault("gitea.url", "")
		m.v.SetDefault("gitea.token", "")
		m.v.SetDefault("claude.api_key", "")
		m.v.SetDefault("claude.model", "claude-sonnet-4-6")
		m.v.SetDefault("claude.effort", "high")
		m.v.SetDefault("webhook.secret", "")
		m.v.SetDefault("notify.default_channel", "gitea")
		m.v.SetDefault("notify.channels.gitea.enabled", false)

		m.v.SetDefault("daily_report.enabled", false)
		m.v.SetDefault("daily_report.cron", "0 9 * * *")
		m.v.SetDefault("daily_report.timezone", "Asia/Shanghai")
		m.v.SetDefault("daily_report.skip_empty", false)
		m.v.SetDefault("daily_report.feishu_webhook", "")
		m.v.SetDefault("daily_report.feishu_secret", "")

		return nil
	}
}

// Load 从当前 Manager 已配置的数据源加载并解析为 Config。
//
// 说明：
// - 若通过 WithConfigFile()/SetConfigFile() 指定了配置文件，则会读取该文件。
// - 未指定配置文件时：
//   - 若启用了 WithDefaultSearchPaths()，则按默认搜索路径查找并读取；若未找到配置文件，视为“未提供配置文件”，不报错。
//   - 未启用 WithDefaultSearchPaths() 时，不读取文件，也不会因此报错。
//
// - 默认值与环境变量覆盖是否生效，取决于是否分别启用了 WithDefaults() 与 WithEnvPrefix()。
func (m *Manager) Load() error {
	// viper 不是显式并发安全；Load 与 SetConfigFile/reload 必须串行访问同一个实例。
	m.viperMu.Lock()
	cfgFile := m.configFile

	if strings.TrimSpace(cfgFile) != "" {
		// 显式指定了配置文件：找不到文件必须报错。
		if err := m.v.ReadInConfig(); err != nil {
			m.viperMu.Unlock()
			return fmt.Errorf("读取配置文件: %w", err)
		}
	} else if m.useDefaultSearchPaths {
		// 启用了默认搜索路径：若没找到配置文件，视为“未提供配置文件”，不应报错。
		if err := m.v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) {
				m.viperMu.Unlock()
				return fmt.Errorf("读取配置文件: %w", err)
			}
		}
	}

	cfg := &Config{}
	if err := m.v.Unmarshal(cfg); err != nil {
		m.viperMu.Unlock()
		return fmt.Errorf("解析配置: %w", err)
	}
	m.viperMu.Unlock()

	if err := Validate(cfg); err != nil {
		return fmt.Errorf("配置校验失败: %w", err)
	}

	m.mu.Lock()
	m.current = cfg
	m.mu.Unlock()
	return nil
}

// Get 获取当前配置的深拷贝；若未加载则返回 nil。
//
// 返回的 *Config 是独立副本，调用者可安全修改而不影响 Manager 内部状态。
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current.Clone()
}

// Clone 返回 Config 的深拷贝。
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	// 值拷贝
	clone := *c

	// 深拷贝 Notify.Channels map
	if c.Notify.Channels != nil {
		clone.Notify.Channels = make(map[string]ChannelConfig, len(c.Notify.Channels))
		for k, v := range c.Notify.Channels {
			ch := v
			if v.Options != nil {
				ch.Options = make(map[string]string, len(v.Options))
				for ok, ov := range v.Options {
					ch.Options[ok] = ov
				}
			}
			clone.Notify.Channels[k] = ch
		}
	}

	// 深拷贝 Notify.Routes slice
	if c.Notify.Routes != nil {
		clone.Notify.Routes = make([]RouteConfig, len(c.Notify.Routes))
		for i, r := range c.Notify.Routes {
			clone.Notify.Routes[i] = r
			if r.Events != nil {
				clone.Notify.Routes[i].Events = append([]string(nil), r.Events...)
			}
			if r.Channels != nil {
				clone.Notify.Routes[i].Channels = append([]string(nil), r.Channels...)
			}
		}
	}

	// 深拷贝 Review
	if c.Review.IgnorePatterns != nil {
		clone.Review.IgnorePatterns = append([]string(nil), c.Review.IgnorePatterns...)
	}
	if c.Review.Enabled != nil {
		v := *c.Review.Enabled
		clone.Review.Enabled = &v
	}
	if c.Review.Dimensions != nil {
		clone.Review.Dimensions = append([]string(nil), c.Review.Dimensions...)
	}

	// 深拷贝 API.Tokens
	if c.API.Tokens != nil {
		clone.API.Tokens = make([]TokenConfig, len(c.API.Tokens))
		copy(clone.API.Tokens, c.API.Tokens)
	}

	// 深拷贝 Repos
	if c.Repos != nil {
		clone.Repos = make([]RepoConfig, len(c.Repos))
		for i, repo := range c.Repos {
			clone.Repos[i] = repo
			// 深拷贝 repo 内的 Notify
			if repo.Notify != nil {
				notifyCopy := *repo.Notify
				if repo.Notify.Routes != nil {
					notifyCopy.Routes = make([]RouteConfig, len(repo.Notify.Routes))
					for j, r := range repo.Notify.Routes {
						notifyCopy.Routes[j] = r
						if r.Events != nil {
							notifyCopy.Routes[j].Events = append([]string(nil), r.Events...)
						}
						if r.Channels != nil {
							notifyCopy.Routes[j].Channels = append([]string(nil), r.Channels...)
						}
					}
				}
				if repo.Notify.Feishu != nil {
					feishuCopy := *repo.Notify.Feishu
					notifyCopy.Feishu = &feishuCopy
				}
				clone.Repos[i].Notify = &notifyCopy
			}
			// 深拷贝 repo.Review
			if repo.Review != nil {
				reviewCopy := *repo.Review
				if repo.Review.IgnorePatterns != nil {
					reviewCopy.IgnorePatterns = append([]string(nil), repo.Review.IgnorePatterns...)
				}
				if repo.Review.Enabled != nil {
					v := *repo.Review.Enabled
					reviewCopy.Enabled = &v
				}
				if repo.Review.Dimensions != nil {
					reviewCopy.Dimensions = append([]string(nil), repo.Review.Dimensions...)
				}
				clone.Repos[i].Review = &reviewCopy
			}
		}
	}

	return &clone
}

// SetConfigFile 设置配置文件路径（供 CLI --config flag 使用）。
func (m *Manager) SetConfigFile(path string) {
	m.viperMu.Lock()
	defer m.viperMu.Unlock()
	m.configFile = path
	m.v.SetConfigFile(path)
}

// Stop 停止配置文件监听（关闭 watcher goroutine）。
//
// 多次调用是安全的（幂等）。未调用 WatchConfig() 时调用 Stop() 同样安全。
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() {
		close(m.stopCh)
		slog.Debug("配置文件 watcher 已停止")
	})
}

// Viper 返回底层 viper 实例，供上层进行 BindPFlag 等高级操作。
func (m *Manager) Viper() *viper.Viper {
	return m.v
}
