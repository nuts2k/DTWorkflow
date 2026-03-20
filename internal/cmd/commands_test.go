package cmd

import (
	"testing"
)

// TestCommandsRegistered 验证 M1.2 所有命令已注册到 root
func TestCommandsRegistered(t *testing.T) {
	// 收集所有已注册的顶层命令
	registered := make(map[string]bool)
	for _, c := range rootCmd.Commands() {
		registered[c.Use] = true
		// 对于有前缀参数的命令（如 "status <task-id>"），只取第一个词
		if name := c.Name(); name != "" {
			registered[name] = true
		}
	}

	topLevel := []string{"version", "review-pr", "fix-issue", "gen-tests", "task", "serve"}
	for _, name := range topLevel {
		if !registered[name] {
			t.Errorf("命令 %q 未注册到 root", name)
		}
	}
}

// TestTaskSubcommands 验证 task 的子命令已正确注册
func TestTaskSubcommands(t *testing.T) {
	// 找到 task 命令
	var taskCommand = taskCmd

	subcommands := make(map[string]bool)
	for _, c := range taskCommand.Commands() {
		subcommands[c.Name()] = true
	}

	expected := []string{"status", "list", "retry"}
	for _, name := range expected {
		if !subcommands[name] {
			t.Errorf("task 子命令 %q 未注册", name)
		}
	}
}

// TestReviewPR_MissingRepo 验证 review-pr 缺少 --repo 时返回错误
func TestReviewPR_MissingRepo(t *testing.T) {
	// 保存并恢复全局状态
	oldRepo := reviewRepo
	oldPR := reviewPR
	defer func() {
		reviewRepo = oldRepo
		reviewPR = oldPR
	}()

	reviewRepo = ""
	reviewPR = 0

	rootCmd.SetArgs([]string{"review-pr"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("缺少 --repo 应返回错误")
	}
}

// TestReviewPR_MissingPR 验证 review-pr 缺少 --pr 时返回错误
func TestReviewPR_MissingPR(t *testing.T) {
	oldRepo := reviewRepo
	oldPR := reviewPR
	defer func() {
		reviewRepo = oldRepo
		reviewPR = oldPR
	}()

	rootCmd.SetArgs([]string{"review-pr", "--repo", "myrepo"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("缺少 --pr 应返回错误")
	}
}

// TestReviewPR_Stub 验证 review-pr 参数齐全时返回"未实现"错误
func TestReviewPR_Stub(t *testing.T) {
	oldRepo := reviewRepo
	oldPR := reviewPR
	defer func() {
		reviewRepo = oldRepo
		reviewPR = oldPR
	}()

	rootCmd.SetArgs([]string{"review-pr", "--repo", "myrepo", "--pr", "42"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("空壳命令应返回错误")
	}
	if ExitCode(err) != 1 {
		t.Errorf("退出码应为 1, got %d", ExitCode(err))
	}
}

// TestFixIssue_MissingParams 验证 fix-issue 参数校验
func TestFixIssue_MissingParams(t *testing.T) {
	oldRepo := fixRepo
	oldIssue := fixIssue
	defer func() {
		fixRepo = oldRepo
		fixIssue = oldIssue
	}()

	rootCmd.SetArgs([]string{"fix-issue"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("缺少参数应返回错误")
	}
}

// TestGenTests_MissingRepo 验证 gen-tests 缺少 --repo 时返回错误
func TestGenTests_MissingRepo(t *testing.T) {
	oldRepo := genTestsRepo
	defer func() { genTestsRepo = oldRepo }()

	rootCmd.SetArgs([]string{"gen-tests"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("缺少 --repo 应返回错误")
	}
}

// TestGlobalFlags 验证全局 flags 注册正确
func TestGlobalFlags(t *testing.T) {
	flags := []string{"json", "config", "verbose"}
	for _, name := range flags {
		f := rootCmd.PersistentFlags().Lookup(name)
		if f == nil {
			t.Errorf("全局 flag %q 未注册", name)
		}
	}

	// 验证 verbose 有短标志 -v
	v := rootCmd.PersistentFlags().ShorthandLookup("v")
	if v == nil {
		t.Error("--verbose 缺少短标志 -v")
	}
}

// TestServeFlags 验证 serve 命令的 flags
func TestServeFlags(t *testing.T) {
	hostFlag := serveCmd.Flags().Lookup("host")
	if hostFlag == nil {
		t.Fatal("serve 缺少 --host flag")
	}
	if hostFlag.DefValue != "0.0.0.0" {
		t.Errorf("--host 默认值应为 0.0.0.0, got %q", hostFlag.DefValue)
	}

	portFlag := serveCmd.Flags().Lookup("port")
	if portFlag == nil {
		t.Fatal("serve 缺少 --port flag")
	}
	if portFlag.DefValue != "8080" {
		t.Errorf("--port 默认值应为 8080, got %q", portFlag.DefValue)
	}
}
