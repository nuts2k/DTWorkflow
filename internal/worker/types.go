package worker

import (
	"encoding"
	"encoding/json"
	"fmt"
	"log/slog"
)

// 编译时接口检查
var (
	_ json.Marshaler         = SecretString("")
	_ encoding.TextMarshaler = SecretString("")
	_ slog.LogValuer         = SecretString("")
)

// SecretString 防止敏感信息在日志或 fmt.Printf 中泄漏
type SecretString string

func (s SecretString) String() string                { return "[REDACTED]" }
func (s SecretString) GoString() string              { return "[REDACTED]" }
func (s SecretString) MarshalJSON() ([]byte, error)  { return []byte(`"[REDACTED]"`), nil }
func (s SecretString) MarshalText() ([]byte, error)  { return []byte("[REDACTED]"), nil }
func (s SecretString) LogValue() slog.Value          { return slog.StringValue("[REDACTED]") }

// ExecutionResult Worker 执行结果
type ExecutionResult struct {
	ExitCode    int    `json:"exit_code"`
	Output      string `json:"output"`
	Error       string `json:"error"`
	Duration    int64  `json:"duration"`     // 毫秒
	ContainerID string `json:"container_id"`
}

// PoolConfig Worker 池配置
type PoolConfig struct {
	Image        string // 锁定 tag，如 dtworkflow-worker:1.0
	CPULimit     string // 容器 CPU 限制，如 "2.0"
	MemoryLimit  string // 容器内存限制，如 "4g"
	GiteaURL     string // Gitea 实例地址
	GiteaToken   SecretString `json:"-"` // Gitea API Token
	ClaudeAPIKey  SecretString `json:"-"` // Claude API Key
	ClaudeBaseURL string       // Claude API 代理地址，留空使用官方地址
	WorkDir       string       // 容器内工作目录
	NetworkName  string // Docker bridge 网络名，默认 "dtworkflow-net"
}

// Validate 校验 PoolConfig 必填字段，在 NewPool 中调用
func (c PoolConfig) Validate() error {
	if c.Image == "" {
		return fmt.Errorf("PoolConfig.Image 不可为空")
	}
	if c.GiteaURL == "" {
		return fmt.Errorf("PoolConfig.GiteaURL 不可为空")
	}
	if c.GiteaToken == "" {
		return fmt.Errorf("PoolConfig.GiteaToken 不可为空")
	}
	if c.ClaudeAPIKey == "" {
		return fmt.Errorf("PoolConfig.ClaudeAPIKey 不可为空")
	}
	return nil
}

// PoolStats 池状态统计
type PoolStats struct {
	Active    int   `json:"active"`    // 当前活跃容器数
	Completed int64 `json:"completed"` // 已完成任务数
}
