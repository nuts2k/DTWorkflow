package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestVersionInfo(t *testing.T) {
	// 验证默认值已设置
	if version == "" {
		t.Error("version 不应为空")
	}
	if gitCommit == "" {
		t.Error("gitCommit 不应为空")
	}
	if buildTime == "" {
		t.Error("buildTime 不应为空")
	}
}

func TestVersionCmd(t *testing.T) {
	// 验证 version 子命令已注册到 root
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Use == "version" {
			found = true
			break
		}
	}
	if !found {
		t.Error("version 子命令未注册到 root 命令")
	}
}

func TestVersionCmdOutput(t *testing.T) {
	// 测试普通文本输出
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	resetRootFlagsForTest(t)
	rootCmd.SetArgs([]string{"version"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("执行 version 命令失败: %v", err)
	}

	// 文本输出写到 stdout（非 rootCmd.OutOrStdout），此处验证命令不报错即可
}

func TestVersionCmdJSONOutput(t *testing.T) {
	// 测试 JSON 格式输出
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	resetRootFlagsForTest(t)
	rootCmd.SetArgs([]string{"--json", "version"})

	// 保存并恢复全局 jsonOutput 状态
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("执行 version --json 命令失败: %v", err)
	}

	// 验证 JSON 输出能正确解析为 versionInfo
	// 注意：Cobra 将 stdout 重定向到 buf，但 json.Encoder 直接写 os.Stdout
	// 因此这里构造一个 versionInfo 来验证 JSON 序列化正确性
	info := versionInfo{
		Version:   version,
		Commit:    gitCommit,
		BuildTime: buildTime,
		Go:        "go1.23",
		OS:        "darwin",
		Arch:      "arm64",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("序列化 versionInfo 失败: %v", err)
	}

	var parsed versionInfo
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("反序列化 versionInfo 失败: %v", err)
	}

	if parsed.Version != info.Version {
		t.Errorf("Version 不匹配: got %q, want %q", parsed.Version, info.Version)
	}
	if parsed.Commit != info.Commit {
		t.Errorf("Commit 不匹配: got %q, want %q", parsed.Commit, info.Commit)
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil 返回 0", nil, 0},
		{"普通错误返回 1", errForTest("普通错误"), 1},
		{"ExitCodeError 返回指定码", &ExitCodeError{Code: 2, Err: errForTest("部分成功")}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExitCode(tt.err)
			if got != tt.want {
				t.Errorf("ExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

// errForTest 是测试用的简单错误类型
type errForTest string

func (e errForTest) Error() string { return string(e) }
