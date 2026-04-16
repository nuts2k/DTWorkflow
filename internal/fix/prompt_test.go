package fix

import (
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
)

func TestBuildPrompt_ReadOnlyConstraint(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	ctx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "测试 Bug",
			Body:   "页面出错了",
		},
	}

	prompt := svc.buildPrompt(ctx)

	if !strings.Contains(prompt, "READ-ONLY") {
		t.Error("prompt 应包含 READ-ONLY 约束")
	}
	if !strings.Contains(prompt, "Do NOT call any external APIs") {
		t.Error("prompt 应包含禁止外部 API 约束")
	}
}

func TestBuildPrompt_Normal(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	ctx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 42,
			Title:  "登录页面崩溃",
			Body:   "点击登录按钮后页面白屏",
		},
	}

	prompt := svc.buildPrompt(ctx)

	if !strings.Contains(prompt, "#42") {
		t.Error("prompt 应包含 Issue 编号")
	}
	if !strings.Contains(prompt, "登录页面崩溃") {
		t.Error("prompt 应包含 Issue 标题")
	}
	if !strings.Contains(prompt, "点击登录按钮后页面白屏") {
		t.Error("prompt 应包含 Issue 描述")
	}
}

func TestBuildPrompt_WithComments(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	ctx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "Bug",
			Body:   "问题描述",
		},
		Comments: []*gitea.Comment{
			{Body: "我也遇到了", User: &gitea.User{Login: "user1"}},
			{Body: "堆栈信息: NPE at line 42", User: &gitea.User{Login: "user2"}},
		},
	}

	prompt := svc.buildPrompt(ctx)

	if !strings.Contains(prompt, "共 2 条") {
		t.Error("prompt 应包含评论数量")
	}
	if !strings.Contains(prompt, "我也遇到了") {
		t.Error("prompt 应包含评论内容")
	}
	if !strings.Contains(prompt, "user1") {
		t.Error("prompt 应包含评论者")
	}
}

func TestBuildPrompt_WithMissingCommentAuthor(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	ctx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "Bug",
			Body:   "问题描述",
		},
		Comments: []*gitea.Comment{
			{Body: "作者字段缺失", User: nil},
			{Body: "作者登录名为空", User: &gitea.User{Login: ""}},
		},
	}

	prompt := svc.buildPrompt(ctx)

	if strings.Count(prompt, "未知作者") != 2 {
		t.Fatalf("prompt 应为缺失作者信息使用兜底名，实际: %q", prompt)
	}
	if !strings.Contains(prompt, "作者字段缺失") || !strings.Contains(prompt, "作者登录名为空") {
		t.Error("prompt 应保留评论内容")
	}
}

func TestBuildPrompt_LongBody(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	longBody := strings.Repeat("a", 6000)
	ctx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "Bug",
			Body:   longBody,
		},
	}

	prompt := svc.buildPrompt(ctx)

	// Body 被截断到 5000 runes + "..."
	if strings.Contains(prompt, longBody) {
		t.Error("超长 Body 应被截断")
	}
	if !strings.Contains(prompt, "...") {
		t.Error("截断后应有省略标记")
	}
}

func TestBuildPrompt_CommentsTotalTruncated(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})

	// 构造大量评论，总字符数超过 maxCommentTotalRunes (20000)
	comments := make([]*gitea.Comment, 15)
	for i := range comments {
		comments[i] = &gitea.Comment{
			Body: strings.Repeat("x", 2000), // 15 * 2000 = 30000 > 20000
			User: &gitea.User{Login: "user"},
		}
	}
	ctx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "Bug",
			Body:   "问题",
		},
		Comments: comments,
	}

	prompt := svc.buildPrompt(ctx)

	if !strings.Contains(prompt, "因长度限制被省略") {
		t.Error("超过总字符限制的评论应被省略")
	}
}

func TestBuildPrompt_AnalysisInstructions(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	ctx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "Bug",
			Body:   "问题描述",
		},
	}

	prompt := svc.buildPrompt(ctx)

	// 检查四段式结构
	if !strings.Contains(prompt, "信息充分性判断") {
		t.Error("prompt 应包含信息充分性判断指令")
	}
	if !strings.Contains(prompt, "根因分析") {
		t.Error("prompt 应包含根因分析指令")
	}
	if !strings.Contains(prompt, "修复建议") {
		t.Error("prompt 应包含修复建议指令")
	}
	if !strings.Contains(prompt, "info_sufficient") {
		t.Error("prompt 应包含输出 JSON schema")
	}
}

func TestExtractJSON_CodeFence(t *testing.T) {
	input := "```json\n{\"info_sufficient\": true}\n```"
	got := extractJSON(input)
	if got != `{"info_sufficient": true}` {
		t.Errorf("extractJSON = %q, want %q", got, `{"info_sufficient": true}`)
	}
}

func TestExtractJSON_NakedJSON(t *testing.T) {
	input := `{"info_sufficient": false, "analysis": "test"}`
	got := extractJSON(input)
	if got != input {
		t.Errorf("extractJSON = %q, want %q", got, input)
	}
}

func TestExtractJSON_LeadingText(t *testing.T) {
	input := "Here is my analysis:\n```json\n{\"confidence\": \"high\"}\n```"
	got := extractJSON(input)
	if got != `{"confidence": "high"}` {
		t.Errorf("extractJSON = %q, want %q", got, `{"confidence": "high"}`)
	}
}

func TestExtractJSON_NakedWithLeadingText(t *testing.T) {
	input := "分析结果如下：\n{\"info_sufficient\": true, \"analysis\": \"测试\"}"
	got := extractJSON(input)
	want := `{"info_sufficient": true, "analysis": "测试"}`
	if got != want {
		t.Errorf("extractJSON = %q, want %q", got, want)
	}
}

func TestBuildCommand_Default(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	cmd := svc.buildCommand()

	if cmd[0] != "claude" {
		t.Errorf("cmd[0] = %q, want %q", cmd[0], "claude")
	}
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "--output-format json") {
		t.Error("命令应包含 --output-format json")
	}
	if !strings.Contains(joined, "--disallowedTools") {
		t.Error("命令应包含 --disallowedTools")
	}
	if strings.Contains(joined, "--model") {
		t.Error("cfgProv 为 nil 时不应追加 --model")
	}
	if strings.Contains(joined, "--effort") {
		t.Error("cfgProv 为 nil 时不应追加 --effort")
	}
}

type mockFixConfigProvider struct {
	model  string
	effort string
}

func (m *mockFixConfigProvider) GetClaudeModel() string  { return m.model }
func (m *mockFixConfigProvider) GetClaudeEffort() string { return m.effort }

func TestBuildCommand_WithModelEffort(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{},
		WithConfigProvider(&mockFixConfigProvider{model: "claude-opus-4-6", effort: "High"}))
	cmd := svc.buildCommand()

	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "--model claude-opus-4-6") {
		t.Errorf("命令应包含 --model claude-opus-4-6，实际: %s", joined)
	}
	if !strings.Contains(joined, "--effort high") {
		t.Errorf("命令应包含 --effort high（小写），实际: %s", joined)
	}
}

func TestBuildCommand_EmptyModelEffort(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{},
		WithConfigProvider(&mockFixConfigProvider{model: "", effort: ""}))
	cmd := svc.buildCommand()
	joined := strings.Join(cmd, " ")
	if strings.Contains(joined, "--model") || strings.Contains(joined, "--effort") {
		t.Errorf("空字符串时不应追加 model/effort，实际: %s", joined)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncated", "hello world", 5, "hello..."},
		{"chinese", "你好世界测试", 4, "你好世界..."},
		{"empty", "", 10, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestBuildPrompt_ContainsRef(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	issueCtx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "test bug",
			Body:   "something broken",
		},
		Ref: "feature/user-auth",
	}
	prompt := svc.buildPrompt(issueCtx)
	if !strings.Contains(prompt, "当前代码基于 ref：feature/user-auth") {
		t.Errorf("prompt 应包含 ref 信息，实际:\n%s", prompt)
	}
}

func TestBuildFixCommand_NoDisallowedBash(t *testing.T) {
	s := &Service{}
	cmd := s.buildFixCommand()

	// 必须是 claude -p - --output-format json 起始
	if len(cmd) < 5 {
		t.Fatalf("fix 命令长度 = %d，期望至少 5 个参数", len(cmd))
	}
	if cmd[0] != "claude" || cmd[1] != "-p" || cmd[2] != "-" {
		t.Errorf("命令前缀 = %v，期望 [claude -p -]", cmd[:3])
	}

	// 必须有 --output-format json
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "--output-format json") {
		t.Error("fix 命令必须含 --output-format json")
	}

	// 禁止带分析模式的 --disallowedTools（fix 需要 Bash）
	if strings.Contains(joined, "--disallowedTools") {
		t.Errorf("fix 命令不应含 --disallowedTools（需要 Bash 和 Write 权限运行测试）: %s", joined)
	}
}

func TestBuildFixCommand_WithModelAndEffort(t *testing.T) {
	s := &Service{cfgProv: &mockFixConfigProvider{model: "claude-sonnet-4-5", effort: "HIGH"}}
	cmd := s.buildFixCommand()
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "--model claude-sonnet-4-5") {
		t.Errorf("应透传 --model, got: %s", joined)
	}
	if !strings.Contains(joined, "--effort high") {
		t.Errorf("effort 应小写归一化, got: %s", joined)
	}
}

func TestBuildPrompt_NoRefOmitted(t *testing.T) {
	svc := NewService(&mockIssueClient{}, &mockFixPoolRunner{})
	issueCtx := &IssueContext{
		Issue: &gitea.Issue{
			Number: 10,
			Title:  "test bug",
		},
		Ref: "",
	}
	prompt := svc.buildPrompt(issueCtx)
	if strings.Contains(prompt, "当前代码基于 ref") {
		t.Errorf("ref 为空时不应出现 ref 信息，实际:\n%s", prompt)
	}
}

func TestBuildFixPrompt_FourSegments(t *testing.T) {
	// 用 99 作为 Issue 编号，与 schema 示例硬编码的 15 区分开，
	// 这样我们可以精确验证模板占位符 %[1]d 确实被渲染成真实编号，
	// 而不是误把 schema 示例段当作指令段命中。
	issue := &gitea.Issue{
		Number: 99,
		Title:  "登录页面崩溃",
		Body:   "用户输入空密码时报 NPE",
	}
	ctx := &IssueContext{
		Issue:    issue,
		Comments: nil,
		Ref:      "main",
	}
	s := &Service{}
	prompt := s.buildFixPrompt(ctx)

	// 关键段落必须出现
	checks := []struct{ needle, reason string }{
		{"#99", "Issue 编号"},
		{"登录页面崩溃", "Issue 标题"},
		{"main", "ref 信息"},
		{"auto-fix/issue-99", "分支命名规范（模板渲染为真实编号）"},
		{"fix: #99", "commit message 模板渲染为真实编号"},
		{"force-with-lease", "force push 指令（重试场景）"},
		{"mvn test", "Java 测试指令"},
		{"npm test", "前端测试指令"},
		{"missing_info", "JSON schema info_sufficient 字段"},
		{"branch_name", "JSON schema branch_name 字段"},
		{"commit_sha", "JSON schema commit_sha 字段"},
		{"test_results", "JSON schema test_results 字段"},
		{"auto-fix/issue-15", "JSON schema 示例段保留硬编码 issue-15"},
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c.needle) {
			t.Errorf("fix prompt 应包含 %q（%s）", c.needle, c.reason)
		}
	}

	// 占位符不能泄漏到最终 prompt
	if strings.Contains(prompt, "<id>") || strings.Contains(prompt, "<issue_id>") {
		t.Errorf("fix prompt 不应含未替换的占位符 <id>/<issue_id>，实际:\n%s", prompt)
	}

	// 修复流程段必须出现真实编号的分支名（不是模板字面）
	const branchInInstructions = "git checkout -b auto-fix/issue-99"
	if !strings.Contains(prompt, branchInInstructions) {
		t.Errorf("第二步指令应包含 %q（验证模板被实际渲染），实际:\n%s",
			branchInInstructions, prompt)
	}

	// 不应含只读模式独有文本
	if strings.Contains(prompt, "READ-ONLY code analysis mode") {
		t.Error("fix prompt 不应包含 READ-ONLY 只读约束（修复需要写权限）")
	}
}
