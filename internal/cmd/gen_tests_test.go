package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
)

// 注：flag 级别的框架 / 模块校验已抽出到 internal/validation 包统一维护，
// 对应单测见 internal/validation/gen_tests_test.go。本文件仅保留 Cobra 装配与
// RunE 集成相关测试。

// TestBuildGenTestsTriggeredBy_PrefixedWithCLI
// 验证 CLI 触发者标识以 "cli:" 前缀开头，使 TaskRecord.TriggeredBy 可区分 webhook/API/CLI 三种来源。
func TestBuildGenTestsTriggeredBy_PrefixedWithCLI(t *testing.T) {
	got := buildGenTestsTriggeredBy()
	if !strings.HasPrefix(got, "cli:") {
		t.Fatalf("triggeredBy = %q, want prefix %q", got, "cli:")
	}
	// hostname 可为空（测试环境下 os.Hostname 出错时兜底为 "local"），
	// 但最终字符串必然长于 "cli:" 本身。
	if len(got) <= len("cli:") {
		t.Fatalf("triggeredBy = %q, 期望至少包含 hostname 后缀", got)
	}
}

func TestBuildGenTestsEnqueueOptions(t *testing.T) {
	if opts := buildGenTestsEnqueueOptions(nil); len(opts) != 0 {
		t.Fatalf("nil gitea client 不应注入任何 enqueue option，实际 %d 个", len(opts))
	}

	client, err := gitea.NewClient("https://gitea.example.com", gitea.WithToken("test-token"))
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	opts := buildGenTestsEnqueueOptions(client)
	if len(opts) != 3 {
		t.Fatalf("非 nil gitea client 应注入 3 个 enqueue option（BranchCleaner + ModuleScanner + PRClient），实际 %d 个", len(opts))
	}
}

func TestPrintGenTestsResults_SplitJSON(t *testing.T) {
	oldJSON := jsonOutput
	oldStdout := os.Stdout
	defer func() {
		jsonOutput = oldJSON
		os.Stdout = oldStdout
	}()
	jsonOutput = true

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe error: %v", err)
	}
	os.Stdout = w

	printGenTestsResults([]queue.EnqueuedTask{
		{TaskID: "task-backend", Module: "backend", Framework: "junit5"},
		{TaskID: "task-frontend", Module: "frontend", Framework: "vitest"},
	}, model.TaskPayload{RepoFullName: "org/repo"}, "main", "")

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	var got genTestsResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("拆分结果应输出合法 JSON，实际 error=%v output=%s", err, buf.String())
	}
	if !got.Split {
		t.Fatal("split 应为 true")
	}
	if got.TaskID != "" {
		t.Errorf("拆分结果不应输出顶层 task_id，实际 %q", got.TaskID)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("期望 2 个任务，实际 %+v", got.Tasks)
	}
	if got.Tasks[0].TaskID != "task-backend" || got.Tasks[1].Framework != "vitest" {
		t.Errorf("tasks 内容不符合预期: %+v", got.Tasks)
	}
}

// TestGenTestsCmd_RegisteredOnRoot 验证 gen-tests 子命令已挂载到 rootCmd。
func TestGenTestsCmd_RegisteredOnRoot(t *testing.T) {
	var found *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "gen-tests" {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("gen-tests 命令未注册到 rootCmd")
	}
	if found != genTestsCmd {
		t.Fatalf("rootCmd 下的 gen-tests 命令指针与 genTestsCmd 不一致")
	}
}

// TestGenTestsCmd_FlagsDefined 验证本地 flag 均已定义，并确认 --json 通过 rootCmd 的 persistent flag 暴露。
func TestGenTestsCmd_FlagsDefined(t *testing.T) {
	localFlags := []string{"owner", "repo", "module", "ref", "framework"}
	for _, name := range localFlags {
		if f := genTestsCmd.Flags().Lookup(name); f == nil {
			t.Errorf("gen-tests 缺少 flag --%s", name)
		}
	}
	// --json 是 rootCmd 的 persistent flag，在真实调用链上 Cobra 会将其合并到叶子命令上下文。
	// 测试环境下（未走 Execute）只在 rootCmd 上可查得，这里直接校验根命令即可。
	if f := rootCmd.PersistentFlags().Lookup("json"); f == nil {
		t.Error("rootCmd 缺少 --json persistent flag")
	}
}

// TestGenTestsCmd_RequiredFlagsAnnotated 验证 --owner 与 --repo 被标记为必填。
// Cobra 的 MarkFlagRequired 通过 Flag.Annotations[cobra.BashCompOneRequiredFlag] 实现。
func TestGenTestsCmd_RequiredFlagsAnnotated(t *testing.T) {
	required := []string{"owner", "repo"}
	for _, name := range required {
		f := genTestsCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("gen-tests 缺少 flag --%s", name)
		}
		annotations := f.Annotations[cobra.BashCompOneRequiredFlag]
		if len(annotations) == 0 || annotations[0] != "true" {
			t.Errorf("flag --%s 应为必填，但未带 required 注解", name)
		}
	}
}

// TestGenTestsCmd_FlagDefaults 验证可选 flag 默认值为空字符串。
func TestGenTestsCmd_FlagDefaults(t *testing.T) {
	cases := []string{"owner", "repo", "module", "ref", "framework"}
	for _, name := range cases {
		f := genTestsCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("flag --%s 未定义", name)
		}
		if f.DefValue != "" {
			t.Errorf("flag --%s 默认值 = %q，期望空字符串", name, f.DefValue)
		}
	}
}

// TestRunGenTests_FrameworkInvalid_ReturnsExitCode1
// 验证 RunE 在参数校验阶段对非法 --framework 返回 ExitCodeError{Code:1}，
// 避免走到 SQLite/Redis/Gitea 依赖初始化。
//
// 注意：这里直接调用 runGenTests 而非走 cobra.Execute，以跳过 root.PersistentPreRunE
// 对完整 gitea/claude 配置的要求；RunE 的早期校验不会用到 cfgManager。
func TestRunGenTests_FrameworkInvalid_ReturnsExitCode1(t *testing.T) {
	// 保存并恢复全局 flag 变量，防止污染其他测试。
	origOwner, origRepo, origFramework := genTestsOwner, genTestsRepo, genTestsFramework
	defer func() {
		genTestsOwner, genTestsRepo, genTestsFramework = origOwner, origRepo, origFramework
	}()

	genTestsOwner = "acme"
	genTestsRepo = "widgets"
	genTestsFramework = "go" // 非法

	// cmd 参数仅用于 context，传入裸 Command 即可。
	err := runGenTests(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("期望非法 --framework 触发 error，got nil")
	}
	var exitErr *ExitCodeError
	// errors.As 会沿 Unwrap 链查找，这里手动断言匹配。
	if e, ok := err.(*ExitCodeError); ok {
		exitErr = e
	}
	if exitErr == nil {
		t.Fatalf("err = %v, 期望类型 *ExitCodeError", err)
	}
	if exitErr.Code != 1 {
		t.Errorf("exit code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(exitErr.Error(), "framework") {
		t.Errorf("err message = %q, 期望包含 \"framework\"", exitErr.Error())
	}
}

// TestRunGenTests_ModuleInvalid_ReturnsExitCode1
// 验证 RunE 在参数校验阶段对非法 --module 返回 ExitCodeError{Code:1}。
func TestRunGenTests_ModuleInvalid_ReturnsExitCode1(t *testing.T) {
	origOwner, origRepo, origModule := genTestsOwner, genTestsRepo, genTestsModule
	defer func() {
		genTestsOwner, genTestsRepo, genTestsModule = origOwner, origRepo, origModule
	}()

	genTestsOwner = "acme"
	genTestsRepo = "widgets"
	genTestsModule = "../escape" // 非法

	err := runGenTests(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("期望非法 --module 触发 error，got nil")
	}
	exitErr, ok := err.(*ExitCodeError)
	if !ok {
		t.Fatalf("err = %v, 期望类型 *ExitCodeError", err)
	}
	if exitErr.Code != 1 {
		t.Errorf("exit code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(exitErr.Error(), "module") && !strings.Contains(exitErr.Error(), "..") {
		t.Errorf("err message = %q, 期望包含 module/\"..\" 提示", exitErr.Error())
	}
}

// TestRunGenTests_EmptyOwnerOrRepo_ReturnsExitCode1
// 在 cfgManager 等外部依赖初始化前的参数裁剪：owner/repo 为纯空白时提前返回。
// 这是对 MarkFlagRequired 的补充（后者只校验是否显式传入，不校验值是否非空）。
func TestRunGenTests_EmptyOwnerOrRepo_ReturnsExitCode1(t *testing.T) {
	origOwner, origRepo := genTestsOwner, genTestsRepo
	defer func() {
		genTestsOwner, genTestsRepo = origOwner, origRepo
	}()

	genTestsOwner = "   "
	genTestsRepo = "widgets"

	err := runGenTests(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("期望空白 owner 触发 error，got nil")
	}
	exitErr, ok := err.(*ExitCodeError)
	if !ok {
		t.Fatalf("err = %v, 期望类型 *ExitCodeError", err)
	}
	if exitErr.Code != 1 {
		t.Errorf("exit code = %d, want 1", exitErr.Code)
	}
}
