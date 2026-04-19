package test

import (
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// defaultPRPayload 构造测试用 TaskPayload（gen_tests 语义）。
func defaultPRPayload() model.TaskPayload {
	return model.TaskPayload{
		TaskType:     model.TaskTypeGenTests,
		RepoOwner:    "acme",
		RepoName:     "backend",
		RepoFullName: "acme/backend",
		Module:       "services/api",
		BaseRef:      "main",
	}
}

// successOutputForPR 构造成功路径 TestGenOutput。
func successOutputForPR() *TestGenOutput {
	return &TestGenOutput{
		Success:            true,
		InfoSufficient:     true,
		VerificationPassed: true,
		BranchName:         "auto-test/services-api",
		CommitSHA:          "deadbeef",
		FailureCategory:    FailureCategoryNone,
		Analysis: &GapAnalysis{
			UntestedModules: []UntestedModule{
				{Path: "src/service/Foo.java", Kind: "service", Priority: "high", Reason: "核心 API 缺覆盖"},
				{Path: "src/service/Bar.java", Kind: "service", Priority: "medium", Reason: "二级逻辑"},
			},
			ExistingStyle: "JUnit 5 + Mockito",
		},
		GeneratedFiles: []GeneratedFile{
			{Path: "src/test/java/FooTest.java", Operation: "create",
				Framework: "junit5", TargetFiles: []string{"src/service/Foo.java"}, TestCount: 5},
			{Path: "src/test/java/BarTest.java", Operation: "append",
				Framework: "junit5", TargetFiles: []string{"src/service/Bar.java"}, TestCount: 2},
		},
		CommittedFiles: []string{"src/test/java/FooTest.java", "src/test/java/BarTest.java"},
		TestResults: &TestRunResults{
			Framework: "junit5", Passed: 7, Failed: 0, Skipped: 0, AllPassed: true, DurationMs: 8500,
		},
	}
}

// =============================================================================
// 成功路径
// =============================================================================

func TestFormatTestGenPRBody_Success_OmitsSkipped(t *testing.T) {
	out := successOutputForPR()
	body := FormatTestGenPRBody(out, defaultPRPayload(), "junit5")

	// 概览基础字段
	mustContainAll(t, "成功 body", body, []string{
		"gen_tests 测试补全",
		"`acme/backend`",
		"`services/api`",
		"`junit5`",
		"`auto-test/services-api`",
		"`deadbeef`",
		"| 生成文件 | **2** |",
		"| 已提交 | **2** |",
		"| 跳过目标 | **0** |",
		"缺口分析",
		"已生成文件",
		"测试结果",
		"通过 **7** / 失败 **0** / 跳过 **0**",
	})

	// 成功路径不应出现"已跳过目标"或"失败分类"节
	if strings.Contains(body, "### 已跳过目标") {
		t.Error("无 SkippedTargets 时不应出现 '已跳过目标' 节")
	}
	if strings.Contains(body, "### 失败分类") {
		t.Error("Success=true + FailureCategoryNone 不应渲染 '失败分类' 节")
	}
	if strings.Contains(body, "### 告警") {
		t.Error("无 Warnings 时不应出现 '告警' 节")
	}
}

func TestFormatTestGenPRBody_Success_EmptyModuleShowsWholeRepo(t *testing.T) {
	payload := defaultPRPayload()
	payload.Module = ""
	out := successOutputForPR()
	body := FormatTestGenPRBody(out, payload, "junit5")
	// `<整仓>` 被 escapePRMarkdown 转义为 `\<整仓\>`
	if !strings.Contains(body, `\<整仓\>`) {
		t.Errorf("空 module 应渲染转义后的 '\\<整仓\\>' 占位，实际 body: %s", body)
	}
}

// =============================================================================
// 半成品交付路径（Success=false && CommittedFiles > 0）
// =============================================================================

func TestFormatTestGenPRBody_PartialDelivery_ShowsFailureAndGenerated(t *testing.T) {
	out := successOutputForPR()
	out.Success = false
	out.FailureCategory = FailureCategoryTestQuality
	out.FailureReason = "retry 耗尽"
	out.SkippedTargets = []SkippedTarget{
		{Path: "src/service/Baz.java", Reason: "verification_failed_after_retries"},
	}

	body := FormatTestGenPRBody(out, defaultPRPayload(), "junit5")
	mustContainAll(t, "半成品 body", body, []string{
		"### 失败分类",
		"`test_quality`",
		"retry 耗尽",
		"### 已生成文件",
		"`src/test/java/FooTest.java`",
		"### 已跳过目标",
		"`src/service/Baz.java`",
		"verification_failed_after_retries",
	})
}

// =============================================================================
// 4 个 FailureCategory 取值各自渲染
// =============================================================================

func TestFormatTestGenPRBody_AllFailureCategories(t *testing.T) {
	cases := []struct {
		name     string
		category FailureCategory
		wantText string
	}{
		{"none 不渲染节", FailureCategoryNone, ""},
		{"infrastructure", FailureCategoryInfrastructure, "`infrastructure`"},
		{"test_quality", FailureCategoryTestQuality, "`test_quality`"},
		{"info_insufficient", FailureCategoryInfoInsufficient, "`info_insufficient`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := successOutputForPR()
			out.FailureCategory = c.category
			// 只有当非 none 时才把 Success 置 false（none + Success=true 模拟成功路径）
			if c.category != FailureCategoryNone {
				out.Success = false
			}
			body := FormatTestGenPRBody(out, defaultPRPayload(), "junit5")
			if c.wantText == "" {
				if strings.Contains(body, "### 失败分类") {
					t.Errorf("category=%q Success=true 不应渲染失败分类节", c.category)
				}
			} else {
				if !strings.Contains(body, "### 失败分类") {
					t.Errorf("category=%q 应渲染失败分类节", c.category)
				}
				if !strings.Contains(body, c.wantText) {
					t.Errorf("category=%q 应含 %q", c.category, c.wantText)
				}
			}
		})
	}
}

// info_insufficient 必须附带 MissingInfo 展示
func TestFormatTestGenPRBody_InfoInsufficient_ShowsMissingInfo(t *testing.T) {
	out := &TestGenOutput{
		Success:         false,
		InfoSufficient:  false,
		FailureCategory: FailureCategoryInfoInsufficient,
		FailureReason:   "缺少源文件上下文",
		MissingInfo:     []string{"源码目录不明确", "依赖配置文件缺失"},
	}
	body := FormatTestGenPRBody(out, defaultPRPayload(), "junit5")
	mustContainAll(t, "info_insufficient body", body, []string{
		"### 失败分类",
		"`info_insufficient`",
		"缺失信息",
		"源码目录不明确",
		"依赖配置文件缺失",
	})
}

// =============================================================================
// Warnings 渲染
// =============================================================================

func TestFormatTestGenPRBody_WarningsSection(t *testing.T) {
	out := successOutputForPR()
	out.Warnings = []string{"AUTO_TEST_BRANCH_RESET_PUSHED=1", "AUTO_TEST_BRANCH_RESET_REMOTE_FAILED=1"}
	body := FormatTestGenPRBody(out, defaultPRPayload(), "junit5")
	mustContainAll(t, "warnings body", body, []string{
		"### 告警",
		"AUTO_TEST_BRANCH_RESET_PUSHED=1",
		"AUTO_TEST_BRANCH_RESET_REMOTE_FAILED=1",
	})
}

// =============================================================================
// GapAnalysis 摘要截断（超过 5 个）
// =============================================================================

func TestFormatTestGenPRBody_GapAnalysisTruncatedAfter5(t *testing.T) {
	out := successOutputForPR()
	// 构造 7 个 untested 条目
	out.Analysis.UntestedModules = nil
	for i := 0; i < 7; i++ {
		out.Analysis.UntestedModules = append(out.Analysis.UntestedModules, UntestedModule{
			Path: "src/M", Kind: "service", Priority: "low", Reason: "x",
		})
	}
	body := FormatTestGenPRBody(out, defaultPRPayload(), "junit5")
	if !strings.Contains(body, "仅展示前 5 个") {
		t.Errorf("超过 5 个未测模块应出现截断提示，实际 body 缺失")
	}
}

// =============================================================================
// Markdown 注入防护（title / module / 字段含 </script>、```、# 等特殊字符）
// =============================================================================

func TestFormatTestGenPRBody_MarkdownEscapesSpecials(t *testing.T) {
	payload := defaultPRPayload()
	payload.RepoFullName = "acme/<script>alert(1)</script>"
	payload.Module = "a/[b](c)#d"
	out := successOutputForPR()
	out.FailureReason = "a`b`c # d [e](f)"
	out.BranchName = "br<anch>"
	out.Warnings = []string{"a<b>c"}
	body := FormatTestGenPRBody(out, payload, "junit5")

	// escapeMarkdown 将 <、>、[、]、(、)、#、` 转义，原始字符不应直接出现（除了正常内容）
	// 重点：不应出现原始 <script> 片段（未转义）
	if strings.Contains(body, "<script>") {
		t.Errorf("Markdown 注入：body 不应直接出现原始 <script>，应被转义")
	}
	// 反引号内的 repo/module 也要转义；<>、#、()、[] 均被 \ 前缀转义
	if !strings.Contains(body, `\<script\>`) {
		t.Errorf("body 应含转义后的 \\<script\\>，实际内容: %s", body)
	}
	if !strings.Contains(body, `\[b\]\(c\)\#d`) {
		t.Errorf("module 特殊字符应被转义，实际内容: %s", body)
	}
}

// =============================================================================
// 容错：out=nil 时仍能产出概览 + 任务记录提示
// =============================================================================

func TestFormatTestGenPRBody_NilOutput_StillRenders(t *testing.T) {
	body := FormatTestGenPRBody(nil, defaultPRPayload(), "junit5")
	mustContainAll(t, "nil body", body, []string{
		"gen_tests 测试补全",
		"`acme/backend`",
		"| 生成文件 | **0** |",
		"本次任务未产出内层 TestGenOutput",
	})
}

// =============================================================================
// 超长截断
// =============================================================================

func TestFormatTestGenPRBody_Truncation(t *testing.T) {
	out := successOutputForPR()
	// 用超长 Reason 逼近 prBodyMaxLen
	huge := strings.Repeat("X", prBodyMaxLen+1000)
	out.Analysis.UntestedModules = []UntestedModule{
		{Path: "p", Kind: "k", Priority: "high", Reason: huge},
	}
	body := FormatTestGenPRBody(out, defaultPRPayload(), "junit5")
	if len(body) > prBodyMaxLen+64 {
		t.Errorf("body 长度应被截断到 ~%d，实际 %d", prBodyMaxLen, len(body))
	}
	if !strings.Contains(body, "已截断") {
		t.Errorf("截断时应附加 '已截断' 说明")
	}
}

// =============================================================================
// helpers
// =============================================================================

func mustContainAll(t *testing.T, name, body string, kws []string) {
	t.Helper()
	for _, kw := range kws {
		if !strings.Contains(body, kw) {
			t.Errorf("%s 应包含 %q", name, kw)
		}
	}
}
