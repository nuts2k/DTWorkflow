package dtw

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// HostsConfig 是 dtw 客户端的多服务器配置
type HostsConfig struct {
	Active  string                  `yaml:"active"`
	Servers map[string]ServerConfig `yaml:"servers"`
}

// ServerConfig 是单个服务器的连接信息
type ServerConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

// DefaultConfigPath 返回默认配置文件路径 ~/.config/dtw/hosts.yml
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dtw", "hosts.yml")
}

// LoadHostsConfig 从指定路径加载配置文件
func LoadHostsConfig(path string) (*HostsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	info, err := os.Stat(path)
	if err == nil && info.Mode().Perm() != 0600 {
		fmt.Fprintf(os.Stderr, "警告: %s 权限不安全 (%04o)，建议设置为 0600\n", path, info.Mode().Perm())
	}
	var cfg HostsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	return &cfg, nil
}

// SaveHostsConfig 将配置保存到指定路径，权限 0600
func SaveHostsConfig(path string, cfg *HostsConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// ResolveServer 解析最终目标服务器，优先级：flag > env > active
func (c *HostsConfig) ResolveServer(flagServer string) (ServerConfig, error) {
	if flagServer != "" {
		srv, ok := c.Servers[flagServer]
		if !ok {
			return ServerConfig{}, fmt.Errorf("服务器 %q 未配置，请运行 dtw auth login", flagServer)
		}
		return srv, nil
	}

	envURL := os.Getenv("DTW_SERVER_URL")
	envToken := os.Getenv("DTW_TOKEN")
	if envURL != "" && envToken != "" {
		return ServerConfig{URL: envURL, Token: envToken}, nil
	}

	name := c.Active
	if name == "" {
		return ServerConfig{}, fmt.Errorf("未指定目标服务器，请运行 dtw auth login 或使用 --server")
	}
	srv, ok := c.Servers[name]
	if !ok {
		return ServerConfig{}, fmt.Errorf("服务器 %q 未配置，请运行 dtw auth login", name)
	}
	return srv, nil
}
