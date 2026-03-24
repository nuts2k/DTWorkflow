package config

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
)

// Config 全局配置（与 YAML 结构一一对应）。
//
// 说明：本任务实现配置校验与仓库级覆盖（仅 notify 覆盖 + review 结构预留）。
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Gitea    GiteaConfig    `mapstructure:"gitea"`
	Claude   ClaudeConfig   `mapstructure:"claude"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Database DatabaseConfig `mapstructure:"database"`
	Log      LogConfig      `mapstructure:"log"`
	Worker   WorkerConfig   `mapstructure:"worker"`
	Webhook  WebhookConfig  `mapstructure:"webhook"`
	Notify   NotifyConfig   `mapstructure:"notify"`
	Review   ReviewOverride `mapstructure:"review"`
	Repos    []RepoConfig   `mapstructure:"repos"`
}

type ClaudeConfig struct {
	APIKey string `mapstructure:"api_key"`
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

	Image       string `mapstructure:"image"`
	CPULimit    string `mapstructure:"cpu_limit"`
	MemoryLimit string `mapstructure:"memory_limit"`
	NetworkName string `mapstructure:"network_name"`
}

type WebhookConfig struct {
	Secret string `mapstructure:"secret"`
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

// Manager 配置管理器，封装 Viper 的加载、校验与当前配置获取。
//
// 说明：
// - Load() 负责从数据源加载配置并校验，然后写入 current。
// - WatchConfig() 用于监听配置文件变更，在校验通过后更新 current，并触发回调。
//
// 重要约定：Get() 返回的 *Config 指向只读数据，调用者不应修改其内容。
type Manager struct {
	v *viper.Viper

	configFile            string
	useDefaultSearchPaths bool

	mu      sync.RWMutex
	current *Config

	onChangeMu sync.RWMutex
	onChange   []func(oldCfg, newCfg *Config)

	watchOnce sync.Once
	watchErr  error
	reloadMu  sync.Mutex
}

type ManagerOption func(*Manager) error

// NewManager 创建配置管理器。
func NewManager(opts ...ManagerOption) (*Manager, error) {
	m := &Manager{v: viper.New()}
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
		m.v.SetDefault("webhook.secret", "")
		m.v.SetDefault("notify.default_channel", "gitea")
		m.v.SetDefault("notify.channels.gitea.enabled", false)

		return nil
	}
}

// Load 从当前 Manager 已配置的数据源加载并解析为 Config。
//
// 说明：
// - 若通过 WithConfigFile()/SetConfigFile() 指定了配置文件，则会读取该文件。
// - 未指定配置文件时：
//   - 若启用了 WithDefaultSearchPaths()，则按默认搜索路径查找并读取；找不到会返回错误。
//   - 未启用 WithDefaultSearchPaths() 时，不读取文件，也不会因此报错。
//
// - 默认值与环境变量覆盖是否生效，取决于是否分别启用了 WithDefaults() 与 WithEnvPrefix()。
func (m *Manager) Load() error {
	if strings.TrimSpace(m.configFile) != "" {
		// 显式指定了配置文件：找不到文件必须报错。
		if err := m.v.ReadInConfig(); err != nil {
			return fmt.Errorf("读取配置文件: %w", err)
		}
	} else if m.useDefaultSearchPaths {
		// 启用了默认搜索路径：若没找到配置文件，视为“未提供配置文件”，不应报错。
		if err := m.v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			if errors.As(err, &notFound) {
				// ignore: continue with flags/env/defaults
			} else {
				return fmt.Errorf("读取配置文件: %w", err)
			}
		}
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

// Get 获取当前配置；若未加载则返回 nil。
//
// 重要约定：返回的 *Config 指向只读数据，调用者不应修改其内容。
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// SetConfigFile 设置配置文件路径（供 CLI --config flag 使用）。
func (m *Manager) SetConfigFile(path string) {
	m.configFile = path
	m.v.SetConfigFile(path)
}

// Viper 返回底层 viper 实例，供上层进行 BindPFlag 等高级操作。
func (m *Manager) Viper() *viper.Viper {
	return m.v
}
