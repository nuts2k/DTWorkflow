package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture 读取 testdata 目录下的固件文件。
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读取固件 %s 失败: %v", name, err)
	}
	return string(data)
}

// parseFixture 解析固件：外层 CLIResponse + 内层 TestGenOutput。
func parseFixture(t *testing.T, raw string) (CLIResponse, TestGenOutput) {
	t.Helper()
	var cli CLIResponse
	if err := json.Unmarshal([]byte(raw), &cli); err != nil {
		t.Fatalf("外层 CLI 解析失败: %v", err)
	}
	var out TestGenOutput
	if err := json.Unmarshal([]byte(extractJSON(cli.Result)), &out); err != nil {
		t.Fatalf("内层 TestGenOutput 解析失败: %v", err)
	}
	return cli, out
}

// =============================================================================
// 固件驱动的 TestGenOutput 解析测试
// =============================================================================

func TestTestGenOutput_SuccessFixture(t *testing.T) {
	cli, out := parseFixture(t, loadFixture(t, "testgen_output_success.json"))

	if cli.Type != "result" {
		t.Errorf("Type = %q, 期望 result", cli.Type)
	}
	if cli.IsExecutionError() {
		t.Error("IsExecutionError 应为 false")
	}
	if cli.EffectiveCostUSD() != 0.75 {
		t.Errorf("EffectiveCostUSD = %v, 期望 0.75", cli.EffectiveCostUSD())
	}

	if !out.Success {
		t.Error("Success 应为 true")
	}
	if !out.InfoSufficient {
		t.Error("InfoSufficient 应为 true")
	}
	if len(out.GeneratedFiles) != 3 {
		t.Errorf("GeneratedFiles 长度 = %d, 期望 3", len(out.GeneratedFiles))
	}
	if len(out.CommittedFiles) != 3 {
		t.Errorf("CommittedFiles 长度 = %d, 期望 3", len(out.CommittedFiles))
	}
	if out.TestResults == nil || !out.TestResults.AllPassed || out.TestResults.Failed != 0 {
		t.Errorf("TestResults 不符合成功预期: %+v", out.TestResults)
	}
	if out.BranchName == "" || out.CommitSHA == "" {
		t.Errorf("BranchName=%q CommitSHA=%q 不应为空", out.BranchName, out.CommitSHA)
	}
	if out.Analysis == nil || len(out.Analysis.UntestedModules) == 0 {
		t.Error("Analysis.UntestedModules 应非空")
	}
	if out.Analysis != nil && len(out.Analysis.ExistingTests) == 0 {
		t.Error("Analysis.ExistingTests 固件应有 1 条，实际为空")
	}

	// 验证 append + create 混合
	foundAppend := false
	foundCreate := false
	for _, gf := range out.GeneratedFiles {
		if gf.Operation == "append" {
			foundAppend = true
		}
		if gf.Operation == "create" {
			foundCreate = true
		}
	}
	if !foundAppend || !foundCreate {
		t.Errorf("期望同时包含 append 与 create，foundAppend=%v foundCreate=%v",
			foundAppend, foundCreate)
	}

	if err := validateSuccessfulTestGenOutput(&out); err != nil {
		t.Errorf("不变量校验应通过，实际: %v", err)
	}
}

func TestTestGenOutput_PartialDeliveryFixture(t *testing.T) {
	_, out := parseFixture(t, loadFixture(t, "testgen_output_partial_delivery.json"))

	if out.Success {
		t.Error("Success 应为 false（部分交付）")
	}
	if !out.InfoSufficient {
		t.Error("InfoSufficient 应为 true")
	}
	if len(out.CommittedFiles) != 2 {
		t.Errorf("CommittedFiles 长度 = %d, 期望 2", len(out.CommittedFiles))
	}
	if len(out.SkippedTargets) != 3 {
		t.Errorf("SkippedTargets 长度 = %d, 期望 3", len(out.SkippedTargets))
	}
	for _, st := range out.SkippedTargets {
		if st.Reason != "time_budget_exhausted" {
			t.Errorf("SkippedTarget reason = %q, 期望 time_budget_exhausted", st.Reason)
		}
	}
	if out.FailureReason == "" {
		t.Error("FailureReason 应非空（部分交付需要说明原因）")
	}
	// Success=false 的固件仅校验 operation / append target_files，应通过
	if err := validateSuccessfulTestGenOutput(&out); err != nil {
		t.Errorf("Success=false 固件应通过不变量校验，实际: %v", err)
	}
}

func TestTestGenOutput_InfoInsufficientFixture(t *testing.T) {
	_, out := parseFixture(t, loadFixture(t, "testgen_output_info_insufficient.json"))

	if out.Success {
		t.Error("Success 应为 false")
	}
	if out.InfoSufficient {
		t.Error("InfoSufficient 应为 false")
	}
	if len(out.MissingInfo) < 2 {
		t.Errorf("MissingInfo 应有至少 2 条，实际 %d", len(out.MissingInfo))
	}
	if out.Analysis != nil {
		t.Error("InfoInsufficient 固件 Analysis 应为 nil")
	}
}

func TestTestGenOutput_FailureFixture(t *testing.T) {
	_, out := parseFixture(t, loadFixture(t, "testgen_output_failure.json"))

	if out.Success {
		t.Error("Success 应为 false")
	}
	if out.VerificationPassed {
		t.Error("VerificationPassed 应为 false")
	}
	if out.TestResults == nil || out.TestResults.Failed != 3 || out.TestResults.AllPassed {
		t.Errorf("TestResults 不符合失败预期: %+v", out.TestResults)
	}
	if out.FailureReason == "" {
		t.Error("FailureReason 应非空")
	}
	if out.RetryRounds != 3 {
		t.Errorf("RetryRounds = %d, 期望 3", out.RetryRounds)
	}
}

func TestTestGenOutput_InvalidOperationFixture(t *testing.T) {
	_, out := parseFixture(t, loadFixture(t, "testgen_output_invalid_operation.json"))

	// Success=true 但 operation=replace 应被不变量拒绝
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil {
		t.Fatal("invalid operation 固件应返回不变量错误")
	}
	if !strings.Contains(err.Error(), "Operation 非法") {
		t.Errorf("错误消息应提及非法 Operation，实际: %v", err)
	}
}

// =============================================================================
// validateSuccessfulTestGenOutput 不变量分支测试
// =============================================================================

func successOutput() TestGenOutput {
	return TestGenOutput{
		Success:            true,
		InfoSufficient:     true,
		VerificationPassed: true,
		BranchName:         "auto-test/foo-20260418",
		CommitSHA:          "abc",
		GeneratedFiles: []GeneratedFile{
			{Path: "a.java", Operation: "create", TargetFiles: []string{"src/a.java"}, TestCount: 1},
		},
		CommittedFiles: []string{"a.java"},
		TestResults: &TestRunResults{
			Framework: "junit5", Passed: 3, Failed: 0, AllPassed: true,
		},
	}
}

func TestValidate_NilOutput(t *testing.T) {
	if err := validateSuccessfulTestGenOutput(nil); err == nil {
		t.Error("nil 应返回错误")
	}
}

func TestValidate_SuccessHappyPath(t *testing.T) {
	out := successOutput()
	if err := validateSuccessfulTestGenOutput(&out); err != nil {
		t.Errorf("happy path 应通过: %v", err)
	}
}

func TestValidate_InfoSufficientFalse(t *testing.T) {
	out := successOutput()
	out.InfoSufficient = false
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "InfoSufficient") {
		t.Errorf("应返回 InfoSufficient 相关错误，实际: %v", err)
	}
}

func TestValidate_VerificationPassedFalse(t *testing.T) {
	out := successOutput()
	out.VerificationPassed = false
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "VerificationPassed") {
		t.Errorf("应返回 VerificationPassed 相关错误，实际: %v", err)
	}
}

func TestValidate_TestResultsNil(t *testing.T) {
	out := successOutput()
	out.TestResults = nil
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "test_results 为空") {
		t.Errorf("应返回 test_results 相关错误，实际: %v", err)
	}
}

func TestValidate_TestResultsNotAllPassed(t *testing.T) {
	out := successOutput()
	out.TestResults.AllPassed = false
	out.TestResults.Failed = 2
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "测试未全部通过") {
		t.Errorf("应返回测试未全部通过错误，实际: %v", err)
	}
}

func TestValidate_BranchNameEmpty(t *testing.T) {
	out := successOutput()
	out.BranchName = ""
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "branch_name") {
		t.Errorf("应返回 branch_name 相关错误，实际: %v", err)
	}
}

func TestValidate_CommitSHAEmpty(t *testing.T) {
	out := successOutput()
	out.CommitSHA = ""
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "commit_sha") {
		t.Errorf("应返回 commit_sha 相关错误，实际: %v", err)
	}
}

func TestValidate_CommittedFilesEmpty(t *testing.T) {
	out := successOutput()
	out.CommittedFiles = nil
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "committed_files 为空") {
		t.Errorf("应返回 committed_files 为空错误，实际: %v", err)
	}
}

func TestValidate_CommittedFilesNotInGenerated(t *testing.T) {
	out := successOutput()
	out.CommittedFiles = []string{"unknown.java"}
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "未在 generated_files 声明") {
		t.Errorf("应返回子集违反错误，实际: %v", err)
	}
}

func TestValidate_InvalidOperation(t *testing.T) {
	out := successOutput()
	out.GeneratedFiles[0].Operation = "replace"
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "Operation 非法") {
		t.Errorf("应返回 Operation 非法错误，实际: %v", err)
	}
}

func TestValidate_AppendWithoutTargetFiles(t *testing.T) {
	out := successOutput()
	out.GeneratedFiles[0].Operation = "append"
	out.GeneratedFiles[0].TargetFiles = nil
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "append 操作必须声明 target_files") {
		t.Errorf("应返回 append 无 target_files 错误，实际: %v", err)
	}
}

// Success=false 仍应校验 operation + append target_files（跨状态约束）
func TestValidate_SuccessFalse_InvalidOperationStillRejected(t *testing.T) {
	out := TestGenOutput{
		Success: false,
		GeneratedFiles: []GeneratedFile{
			{Path: "x.java", Operation: "replace"},
		},
	}
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "Operation 非法") {
		t.Errorf("Success=false 也应拒绝非法 operation，实际: %v", err)
	}
}

// Success=false + operation=append 无 target_files → 仍然拒绝（跨状态约束）
func TestValidate_SuccessFalse_AppendWithoutTargetStillRejected(t *testing.T) {
	out := TestGenOutput{
		Success: false,
		GeneratedFiles: []GeneratedFile{
			{Path: "x.java", Operation: "append"},
		},
	}
	err := validateSuccessfulTestGenOutput(&out)
	if err == nil || !strings.Contains(err.Error(), "append 操作必须声明") {
		t.Errorf("Success=false 也应拒绝 append 无 target_files，实际: %v", err)
	}
}

// Success=false + 合法 operation + 其它字段缺失 → 放行
func TestValidate_SuccessFalse_OtherFieldsRelaxed(t *testing.T) {
	out := TestGenOutput{
		Success: false,
		// 无 BranchName / CommitSHA / TestResults，operation 合法
		GeneratedFiles: []GeneratedFile{
			{Path: "x.java", Operation: "create"},
		},
	}
	if err := validateSuccessfulTestGenOutput(&out); err != nil {
		t.Errorf("Success=false 合法 operation 应通过，实际: %v", err)
	}
}

// =============================================================================
// extractJSON 行为测试
// =============================================================================

func TestExtractJSON_PlainJSON(t *testing.T) {
	got := extractJSON(`{"a":1}`)
	if got != `{"a":1}` {
		t.Errorf("got = %q", got)
	}
}

func TestExtractJSON_WithFence(t *testing.T) {
	input := "```json\n{\"a\":1}\n```"
	got := extractJSON(input)
	if got != `{"a":1}` {
		t.Errorf("got = %q", got)
	}
}

func TestExtractJSON_WithFenceNoLang(t *testing.T) {
	input := "```\n{\"a\":1}\n```"
	got := extractJSON(input)
	if got != `{"a":1}` {
		t.Errorf("got = %q", got)
	}
}

func TestExtractJSON_WithSurroundingText(t *testing.T) {
	input := `解释如下：{"a":1} 结束`
	got := extractJSON(input)
	if got != `{"a":1}` {
		t.Errorf("got = %q", got)
	}
}

func TestExtractJSON_NoBraces(t *testing.T) {
	input := "no json at all"
	got := extractJSON(input)
	if got != input {
		t.Errorf("应原样返回，得 %q", got)
	}
}

// =============================================================================
// CLIResponse 行为测试
// =============================================================================

func TestCLIResponse_EffectiveCostUSD_PrefersTotal(t *testing.T) {
	r := CLIResponse{CostUSD: 0.1, TotalCostUSD: 0.5}
	if got := r.EffectiveCostUSD(); got != 0.5 {
		t.Errorf("应优先返回 TotalCostUSD, 得 %v", got)
	}
}

func TestCLIResponse_EffectiveCostUSD_FallbackToCost(t *testing.T) {
	r := CLIResponse{CostUSD: 0.2, TotalCostUSD: 0}
	if got := r.EffectiveCostUSD(); got != 0.2 {
		t.Errorf("应回退到 CostUSD, 得 %v", got)
	}
}

func TestCLIResponse_IsExecutionError(t *testing.T) {
	cases := []struct {
		name   string
		r      CLIResponse
		expect bool
	}{
		{"正常 result", CLIResponse{Type: "result"}, false},
		{"正常 success", CLIResponse{Type: "success"}, false},
		{"空 Type", CLIResponse{Type: ""}, false},
		{"is_error=true", CLIResponse{IsError: true}, true},
		{"未知 type", CLIResponse{Type: "error"}, true},
		{"another unknown", CLIResponse{Type: "whatever"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.IsExecutionError(); got != tc.expect {
				t.Errorf("got=%v want=%v", got, tc.expect)
			}
		})
	}
}

// =============================================================================
// truncate / safeOutput 边界
// =============================================================================

func TestTruncate_Short(t *testing.T) {
	if truncate("abc", 10) != "abc" {
		t.Error("短字符串不应截断")
	}
}

func TestTruncate_LongASCII(t *testing.T) {
	got := truncate("abcdefghij", 3)
	if got != "abc..." {
		t.Errorf("got=%q", got)
	}
}

func TestTruncate_LongUTF8(t *testing.T) {
	// 长度 5 个中文字符（15 bytes）截成 3 字符
	got := truncate("一二三四五", 3)
	if got != "一二三..." {
		t.Errorf("got=%q", got)
	}
}

func TestSafeOutput_Nil(t *testing.T) {
	if got := safeOutput(nil); got != "" {
		t.Errorf("nil 应返回空串，得 %q", got)
	}
}
