package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubCommentCreator 用于测试的桩 GiteaCommentCreator
type stubCommentCreator struct {
	err   error
	calls []createIssueCommentCall
}

type createIssueCommentCall struct {
	owner string
	repo  string
	index int64
	body  string
}

func (s *stubCommentCreator) CreateIssueComment(_ context.Context, owner, repo string, index int64, body string) error {
	s.calls = append(s.calls, createIssueCommentCall{owner, repo, index, body})
	return s.err
}

func TestNewGiteaNotifier_NilClient(t *testing.T) {
	_, err := NewGiteaNotifier(nil)
	if err == nil {
		t.Fatal("NewGiteaNotifier(nil) should return error")
	}
	if !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("error should wrap ErrInvalidTarget, got: %v", err)
	}
}

func TestGiteaNotifier_Name(t *testing.T) {
	n, err := NewGiteaNotifier(&stubCommentCreator{})
	if err != nil {
		t.Fatalf("NewGiteaNotifier() error: %v", err)
	}
	if got := n.Name(); got != "gitea" {
		t.Errorf("Name() = %q, want %q", got, "gitea")
	}
}

func TestGiteaNotifier_Send_Success(t *testing.T) {
	stub := &stubCommentCreator{}
	n, err := NewGiteaNotifier(stub)
	if err != nil {
		t.Fatalf("NewGiteaNotifier() error: %v", err)
	}

	msg := Message{
		EventType: EventPRReviewDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "myorg", Repo: "myrepo", Number: 42, IsPR: true},
		Title:     "评审完成",
		Body:      "PR 已通过自动评审",
	}

	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() unexpected error: %v", err)
	}

	if len(stub.calls) != 1 {
		t.Fatalf("CreateIssueComment called %d times, want 1", len(stub.calls))
	}

	call := stub.calls[0]
	if call.owner != "myorg" {
		t.Errorf("owner = %q, want %q", call.owner, "myorg")
	}
	if call.repo != "myrepo" {
		t.Errorf("repo = %q, want %q", call.repo, "myrepo")
	}
	if call.index != 42 {
		t.Errorf("index = %d, want 42", call.index)
	}
	// 验证格式化内容包含关键元素
	if !strings.Contains(call.body, "评审完成") {
		t.Errorf("body should contain title, got: %q", call.body)
	}
	if !strings.Contains(call.body, "PR 已通过自动评审") {
		t.Errorf("body should contain body text, got: %q", call.body)
	}
	if !strings.Contains(call.body, string(EventPRReviewDone)) {
		t.Errorf("body should contain event type, got: %q", call.body)
	}
}

func TestGiteaNotifier_Send_ZeroNumber(t *testing.T) {
	stub := &stubCommentCreator{}
	n, err := NewGiteaNotifier(stub)
	if err != nil {
		t.Fatalf("NewGiteaNotifier() error: %v", err)
	}

	msg := Message{
		EventType: EventSystemError,
		Target:    Target{Owner: "org", Repo: "repo", Number: 0},
	}

	err = n.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("Send() with Number=0 should return error")
	}
	if !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("error should wrap ErrInvalidTarget, got: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Error("CreateIssueComment should not be called when Number=0")
	}
}

func TestGiteaNotifier_Send_APIError(t *testing.T) {
	apiErr := errors.New("API 调用失败")
	stub := &stubCommentCreator{err: apiErr}
	n, err := NewGiteaNotifier(stub)
	if err != nil {
		t.Fatalf("NewGiteaNotifier() error: %v", err)
	}

	msg := Message{
		EventType: EventSystemError,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
		Title:     "系统错误",
		Body:      "发生了系统错误",
	}

	err = n.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("Send() should propagate API error")
	}
	if !errors.Is(err, apiErr) {
		t.Errorf("error should wrap API error, got: %v", err)
	}
	// 双 %w 包装：同时验证 ErrSendFailed 哨兵错误（Go 1.20+ 支持多 %w）
	if !errors.Is(err, ErrSendFailed) {
		t.Errorf("error should wrap ErrSendFailed, got: %v", err)
	}
}

func TestGiteaNotifier_Send_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	stub := &stubCommentCreator{}
	n, err := NewGiteaNotifier(stub)
	if err != nil {
		t.Fatalf("NewGiteaNotifier() error: %v", err)
	}

	msg := Message{
		EventType: EventSystemError,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "owner", Repo: "repo", Number: 1},
		Title:     "测试取消",
		Body:      "context 取消场景",
	}

	err = n.Send(ctx, msg)
	if err == nil {
		t.Fatal("期望返回错误")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("期望 context.Canceled 错误，实际: %v", err)
	}
	if len(stub.calls) != 0 {
		t.Errorf("取消后不应调用 API，实际调用了 %d 次", len(stub.calls))
	}
}

func TestGiteaNotifier_WithLogger(t *testing.T) {
	stub := &stubCommentCreator{}
	// 使用 nil logger 应使用默认值，不 panic
	n, err := NewGiteaNotifier(stub, WithLogger(nil))
	if err != nil {
		t.Fatalf("NewGiteaNotifier() with nil logger error: %v", err)
	}
	if n.logger == nil {
		t.Error("logger should not be nil after WithLogger(nil)")
	}
}

func TestFormatGiteaComment_Info(t *testing.T) {
	msg := Message{
		EventType: EventPRReviewDone,
		Severity:  SeverityInfo,
		Title:     "评审完成",
		Body:      "自动评审通过",
	}
	comment := formatGiteaComment(msg)

	if !strings.HasPrefix(comment, "## ℹ️") {
		t.Errorf("info comment should start with ℹ️, got: %q", comment)
	}
	if !strings.Contains(comment, "评审完成") {
		t.Errorf("comment should contain title, got: %q", comment)
	}
	if !strings.Contains(comment, "自动评审通过") {
		t.Errorf("comment should contain body, got: %q", comment)
	}
	if !strings.Contains(comment, "DTWorkflow") {
		t.Errorf("comment should contain DTWorkflow signature, got: %q", comment)
	}
	if !strings.Contains(comment, string(EventPRReviewDone)) {
		t.Errorf("comment should contain event type, got: %q", comment)
	}
}

func TestFormatGiteaComment_Warning(t *testing.T) {
	msg := Message{
		EventType: EventIssueNeedInfo,
		Severity:  SeverityWarning,
		Title:     "需要更多信息",
		Body:      "请补充描述",
	}
	comment := formatGiteaComment(msg)

	if !strings.HasPrefix(comment, "## ⚠️") {
		t.Errorf("warning comment should start with ⚠️, got: %q", comment)
	}
}

func TestFormatGiteaComment_Critical(t *testing.T) {
	msg := Message{
		EventType: EventSystemError,
		Severity:  SeverityCritical,
		Title:     "严重错误",
		Body:      "系统出现严重故障",
	}
	comment := formatGiteaComment(msg)

	if !strings.HasPrefix(comment, "## 🚨") {
		t.Errorf("critical comment should start with 🚨, got: %q", comment)
	}
}
