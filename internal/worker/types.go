package worker

import (
	"encoding"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
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

// TaskTimeoutsConfig Worker 层的超时配置（从 config.TaskTimeouts 转换而来）
type TaskTimeoutsConfig struct {
	ReviewPR     time.Duration
	FixIssue     time.Duration
	GenTests     time.Duration
	AnalyzeIssue time.Duration // M3.4: 只读分析超时（默认 15m）
}

// Lookup 根据任务类型返回对应超时值。零值时回退到与 queue 层一致的按类型默认值。
func (c TaskTimeoutsConfig) Lookup(taskType model.TaskType) time.Duration {
	switch taskType {
	case model.TaskTypeReviewPR:
		if c.ReviewPR > 0 {
			return c.ReviewPR
		}
		return 10 * time.Minute
	case model.TaskTypeFixIssue:
		if c.FixIssue > 0 {
			return c.FixIssue
		}
		return 30 * time.Minute
	case model.TaskTypeGenTests:
		if c.GenTests > 0 {
			return c.GenTests
		}
		return 20 * time.Minute
	case model.TaskTypeAnalyzeIssue:
		if c.AnalyzeIssue > 0 {
			return c.AnalyzeIssue
		}
		return 15 * time.Minute
	default:
		return 10 * time.Minute
	}
}

// StreamMonitorConfig Worker 层的流式监控配置
type StreamMonitorConfig struct {
	Enabled         bool
	ActivityTimeout time.Duration
}

// PoolConfig Worker 池配置
type PoolConfig struct {
	Image        string // 锁定 tag，如 dtworkflow-worker:1.0
	ImageFull        string // 执行镜像（fix、gen_tests），可选
	MavenCacheVolume string // Maven 缓存 Docker named volume，非空时挂载到 /workspace/.m2/repository（仅 ImageFull 容器）
	CPULimit     string // 容器 CPU 限制，如 "2.0"
	MemoryLimit  string // 容器内存限制，如 "4g"
	GiteaURL     string // Gitea 实例地址
	GiteaToken   SecretString `json:"-"` // Gitea API Token
	ClaudeAPIKey  SecretString `json:"-"` // Claude API Key
	ClaudeBaseURL string       // Claude API 代理地址，留空使用官方地址
	WorkDir       string       // 容器内工作目录
	NetworkName  string // Docker bridge 网络名，默认 "dtworkflow-net"
	GiteaInsecureSkipVerify bool // 跳过 Gitea TLS 证书验证（自签名证书场景）
	Timeouts      TaskTimeoutsConfig  // 按任务类型的硬超时配置
	StreamMonitor StreamMonitorConfig // 流式心跳监控配置
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
	if c.ImageFull != "" && strings.Contains(c.ImageFull, " ") {
		return fmt.Errorf("PoolConfig.ImageFull 不可含空格: %q", c.ImageFull)
	}
	return nil
}

// PoolStats 池状态统计
type PoolStats struct {
	Active    int   `json:"active"`    // 当前活跃容器数
	Completed int64 `json:"completed"` // 已完成任务数
}
