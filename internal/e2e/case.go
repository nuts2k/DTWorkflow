package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	CaseFileName       = "case.yaml"
	DefaultCaseTimeout = 5 * time.Minute
	MinCaseTimeout     = 30 * time.Second
	MaxCaseTimeout     = 30 * time.Minute
)

// CaseSpec case.yaml 的 Go 映射。
type CaseSpec struct {
	Name         string        `yaml:"name"`
	Description  string        `yaml:"description,omitempty"`
	Timeout      Duration      `yaml:"timeout,omitempty"`
	Tags         []string      `yaml:"tags,omitempty"`
	Expectations []Expectation `yaml:"expectations,omitempty"`
	Setup        []string      `yaml:"setup,omitempty"`
	Test         []string      `yaml:"test"`
	Teardown     []string      `yaml:"teardown,omitempty"`
}

// Expectation 业务意图描述，作为 auto-fix 的修复基准。
type Expectation struct {
	Step   string `yaml:"step"`
	Expect string `yaml:"expect"`
}

// Duration 支持 YAML 解析 Go duration 字符串（如 "5m", "30s"）。
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		d.Duration = DefaultCaseTimeout
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("无效的 timeout 格式: %w", err)
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (interface{}, error) {
	if d.Duration == 0 {
		return "", nil
	}
	return d.Duration.String(), nil
}

// ValidateCaseSpec 校验 case.yaml 内容。
func ValidateCaseSpec(spec *CaseSpec) error {
	if spec == nil {
		return fmt.Errorf("case spec 为 nil")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("name 必填且不能为空")
	}
	if len(spec.Test) == 0 {
		return fmt.Errorf("test 必填且至少包含一个脚本")
	}

	timeout := spec.Timeout.Duration
	if timeout == 0 {
		timeout = DefaultCaseTimeout
	}
	if timeout < MinCaseTimeout || timeout > MaxCaseTimeout {
		return fmt.Errorf("timeout %v 超出合法范围 [%v, %v]", timeout, MinCaseTimeout, MaxCaseTimeout)
	}

	allScripts := make([]string, 0, len(spec.Setup)+len(spec.Test)+len(spec.Teardown))
	allScripts = append(allScripts, spec.Setup...)
	allScripts = append(allScripts, spec.Test...)
	allScripts = append(allScripts, spec.Teardown...)

	for _, script := range allScripts {
		if err := validateScriptName(script); err != nil {
			return err
		}
	}

	for _, script := range spec.Test {
		if !strings.HasSuffix(script, ".spec.ts") {
			return fmt.Errorf("test 脚本必须以 .spec.ts 结尾: %q", script)
		}
	}

	return nil
}

var validScriptExtensions = map[string]bool{
	".sql":     true,
	".js":      true,
	".ts":      true,
	".spec.ts": true,
}

func validateScriptName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("脚本名不能为空")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("脚本名不能为绝对路径: %q", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("脚本名不能包含路径遍历: %q", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("脚本名不能包含路径分隔符: %q", name)
	}

	ext := filepath.Ext(name)
	if strings.HasSuffix(name, ".spec.ts") {
		ext = ".spec.ts"
	}
	if !validScriptExtensions[ext] {
		return fmt.Errorf("不支持的脚本扩展名 %q（支持: .sql, .js, .ts, .spec.ts）", ext)
	}
	return nil
}
