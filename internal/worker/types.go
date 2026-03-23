package worker

// SecretString 防止敏感信息在日志或 fmt.Printf 中泄漏
type SecretString string

func (s SecretString) String() string   { return "[REDACTED]" }
func (s SecretString) GoString() string { return "[REDACTED]" }

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
	ClaudeAPIKey SecretString `json:"-"` // Claude API Key
	WorkDir      string // 容器内工作目录
	NetworkName  string // Docker bridge 网络名，默认 "dtworkflow-net"
}

// PoolStats 池状态统计
type PoolStats struct {
	Active    int   `json:"active"`    // 当前活跃容器数
	Completed int64 `json:"completed"` // 已完成任务数
}
