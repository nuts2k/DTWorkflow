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
