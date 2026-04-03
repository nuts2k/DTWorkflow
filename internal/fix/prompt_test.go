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
			Body:   strings.Repeat("x", 2000), // 15 * 2000 = 30000 > 20000
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
