package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
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

	if !strings.HasPrefix(comment, "## ‼️") {
		t.Errorf("critical comment should start with ‼️, got: %q", comment)
	}
}

func TestFormatGiteaComment_StripsNonBMPChars(t *testing.T) {
	msg := Message{
		EventType: EventSystemError,
		Severity:  SeverityCritical,
		Title:     "严重错误 🤖",
		Body:      "系统出现严重故障 🚀",
	}
	comment := formatGiteaComment(msg)

	for _, bad := range []string{"🤖", "🚀"} {
		if strings.Contains(comment, bad) {
			t.Errorf("comment 应剔除非 BMP 字符 %q，实际: %q", bad, comment)
		}
	}
	if !strings.Contains(comment, "严重错误 ") || !strings.Contains(comment, "系统出现严重故障 ") {
		t.Errorf("正常文本应保留，实际: %q", comment)
	}
}

// TestGiteaNotifier_Send_NegativeNumber 验证负数 Number 返回 ErrInvalidTarget（MEDIUM-1）
func TestGiteaNotifier_Send_NegativeNumber(t *testing.T) {
	stub := &stubCommentCreator{}
	n, err := NewGiteaNotifier(stub)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{
		EventType: EventPRReviewDone,
		Severity:  SeverityInfo,
		Body:      "test",
		Target:    Target{Owner: "org", Repo: "repo", Number: -1},
	}
	err = n.Send(context.Background(), msg)
	if !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("负数 Number 应返回 ErrInvalidTarget, got: %v", err)
	}
}

// TestGiteaNotifier_Send_EmptyTitle 验证 Title 为空时使用 EventType 作为标题（MEDIUM-4）
func TestGiteaNotifier_Send_EmptyTitle(t *testing.T) {
	stub := &stubCommentCreator{}
	n, err := NewGiteaNotifier(stub)
	if err != nil {
		t.Fatalf("NewGiteaNotifier() error: %v", err)
	}
	msg := Message{
		EventType: EventSystemError,
		Severity:  SeverityInfo,
		Body:      "some body",
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
		// Title 留空
	}
	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if len(stub.calls) == 0 {
		t.Fatal("CreateIssueComment 未被调用")
	}
	body := stub.calls[0].body
	if body == "" {
		t.Error("评论内容不应为空")
	}
	// Title 为空时应使用 EventType 作为标题
	if !strings.Contains(body, string(EventSystemError)) {
		t.Errorf("Title 为空时评论应包含事件类型名称, got: %q", body)
	}
}

// --- M4.2 CommentOnGenTestsPR 测试 ---

// stubCommentManager 实现 GiteaPRCommentManager，用于 upsert 路径测试。
type stubCommentManager struct {
	listReturn []GiteaCommentInfo
	listErr    error
	editErr    error
	createErr  error

	editCalls   []editCall
	createCalls []createIssueCommentCall
}

type editCall struct {
	owner     string
	repo      string
	commentID int64
	body      string
}

func (s *stubCommentManager) CreateIssueComment(_ context.Context, owner, repo string, index int64, body string) error {
	s.createCalls = append(s.createCalls, createIssueCommentCall{owner, repo, index, body})
	return s.createErr
}

func (s *stubCommentManager) ListIssueComments(_ context.Context, _, _ string, _ int64) ([]GiteaCommentInfo, error) {
	return s.listReturn, s.listErr
}

func (s *stubCommentManager) EditIssueComment(_ context.Context, owner, repo string, commentID int64, body string) error {
	s.editCalls = append(s.editCalls, editCall{owner, repo, commentID, body})
	return s.editErr
}

// TestCommentOnGenTestsPR_FirstTimeCreate 首次调用：PR 上无含锚点评论 → 走 Create。
func TestCommentOnGenTestsPR_FirstTimeCreate(t *testing.T) {
	mgr := &stubCommentManager{listReturn: nil}
	n, err := NewGiteaNotifier(mgr)
	if err != nil {
		t.Fatalf("NewGiteaNotifier error: %v", err)
	}

	err = n.CommentOnGenTestsPR(context.Background(), "org", "repo", 42, "body-content")
	if err != nil {
		t.Fatalf("CommentOnGenTestsPR error: %v", err)
	}
	if len(mgr.createCalls) != 1 {
		t.Fatalf("期望 Create 调用 1 次，实际 %d", len(mgr.createCalls))
	}
	if len(mgr.editCalls) != 0 {
		t.Errorf("首次发送不应调用 Edit，实际 %d", len(mgr.editCalls))
	}
	got := mgr.createCalls[0]
	if got.owner != "org" || got.repo != "repo" || got.index != 42 {
		t.Errorf("Create 目标错误：%+v", got)
	}
	if !strings.HasPrefix(got.body, genTestsDoneAnchor) {
		t.Errorf("Create body 未以锚点开头：%q", got.body)
	}
	if !strings.Contains(got.body, "body-content") {
		t.Errorf("Create body 未包含原始内容：%q", got.body)
	}
}

// TestCommentOnGenTestsPR_CreateStripsNonBMPChars 验证首次 Create 路径会清洗
// Gitea MySQL utf8 不支持的非 BMP 字符，避免写评论时触发 500。
func TestCommentOnGenTestsPR_CreateStripsNonBMPChars(t *testing.T) {
	mgr := &stubCommentManager{listReturn: nil}
	n, err := NewGiteaNotifier(mgr)
	if err != nil {
		t.Fatalf("NewGiteaNotifier error: %v", err)
	}

	err = n.CommentOnGenTestsPR(context.Background(), "org", "repo", 42, "body 🤖🚀")
	if err != nil {
		t.Fatalf("CommentOnGenTestsPR error: %v", err)
	}
	if len(mgr.createCalls) != 1 {
		t.Fatalf("期望 Create 调用 1 次，实际 %d", len(mgr.createCalls))
	}
	got := mgr.createCalls[0].body
	if strings.Contains(got, "🤖") || strings.Contains(got, "🚀") {
		t.Fatalf("Create body 不应包含非 BMP 字符，实际：%q", got)
	}
	if !strings.Contains(got, "body ") {
		t.Fatalf("Create body 应保留非 BMP 以外内容，实际：%q", got)
	}
}

// TestCommentOnGenTestsPR_SecondTimeEdit 同 PR 已存在含锚点的旧评论 → 走 Edit 覆盖。
func TestCommentOnGenTestsPR_SecondTimeEdit(t *testing.T) {
	oldBody := genTestsDoneAnchor + "\n\n旧的评论内容（由上一次 gen_tests 任务写入）"
	mgr := &stubCommentManager{
		listReturn: []GiteaCommentInfo{
			{ID: 99, Body: "人类评论，与本服务无关"},
			{ID: 100, Body: oldBody},
		},
	}
	n, err := NewGiteaNotifier(mgr)
	if err != nil {
		t.Fatalf("NewGiteaNotifier error: %v", err)
	}

	err = n.CommentOnGenTestsPR(context.Background(), "org", "repo", 42, "新的评论内容")
	if err != nil {
		t.Fatalf("CommentOnGenTestsPR error: %v", err)
	}
	if len(mgr.createCalls) != 0 {
		t.Errorf("命中锚点不应调用 Create，实际 %d", len(mgr.createCalls))
	}
	if len(mgr.editCalls) != 1 {
		t.Fatalf("期望 Edit 调用 1 次，实际 %d", len(mgr.editCalls))
	}
	got := mgr.editCalls[0]
	if got.commentID != 100 {
		t.Errorf("Edit 应命中 commentID=100（带锚点的那条），实际 %d", got.commentID)
	}
	if !strings.HasPrefix(got.body, genTestsDoneAnchor) {
		t.Errorf("Edit body 未以锚点开头：%q", got.body)
	}
	if !strings.Contains(got.body, "新的评论内容") {
		t.Errorf("Edit body 未包含新内容：%q", got.body)
	}
}

// TestCommentOnGenTestsPR_EditStripsNonBMPChars 验证覆盖旧评论的 Edit 路径也会清洗
// 非 BMP 字符，避免 upsert 命中旧评论后绕过 Create 路径兜底。
func TestCommentOnGenTestsPR_EditStripsNonBMPChars(t *testing.T) {
	mgr := &stubCommentManager{
		listReturn: []GiteaCommentInfo{
			{ID: 100, Body: genTestsDoneAnchor + "\n\n旧评论"},
		},
	}
	n, err := NewGiteaNotifier(mgr)
	if err != nil {
		t.Fatalf("NewGiteaNotifier error: %v", err)
	}

	err = n.CommentOnGenTestsPR(context.Background(), "org", "repo", 42, "新的评论内容 🤖🚀")
	if err != nil {
		t.Fatalf("CommentOnGenTestsPR error: %v", err)
	}
	if len(mgr.editCalls) != 1 {
		t.Fatalf("期望 Edit 调用 1 次，实际 %d", len(mgr.editCalls))
	}
	got := mgr.editCalls[0].body
	if strings.Contains(got, "🤖") || strings.Contains(got, "🚀") {
		t.Fatalf("Edit body 不应包含非 BMP 字符，实际：%q", got)
	}
	if !strings.Contains(got, "新的评论内容 ") {
		t.Fatalf("Edit body 应保留非 BMP 以外内容，实际：%q", got)
	}
}

// TestCommentOnGenTestsPR_NoAnchorMatch 其它 bot/人类评论不含锚点 → 不误命中，仍走 Create。
func TestCommentOnGenTestsPR_NoAnchorMatch(t *testing.T) {
	mgr := &stubCommentManager{
		listReturn: []GiteaCommentInfo{
			{ID: 1, Body: "这是一条人类评论，里面可能提到 gen_tests 关键词但无 HTML 锚点"},
			{ID: 2, Body: "<!-- 其它工具锚点 --> 某个 CI 插件发的"},
			{ID: 3, Body: "<!-- dtworkflow:review:done -->\n\nPR 评审完成"}, // 另一种锚点，不应被误命中
		},
	}
	n, err := NewGiteaNotifier(mgr)
	if err != nil {
		t.Fatalf("NewGiteaNotifier error: %v", err)
	}

	err = n.CommentOnGenTestsPR(context.Background(), "org", "repo", 42, "body")
	if err != nil {
		t.Fatalf("CommentOnGenTestsPR error: %v", err)
	}
	if len(mgr.editCalls) != 0 {
		t.Errorf("其它锚点不应误命中，实际调用 Edit %d 次", len(mgr.editCalls))
	}
	if len(mgr.createCalls) != 1 {
		t.Errorf("应走 Create 路径，实际 %d", len(mgr.createCalls))
	}
}

// TestCommentOnGenTestsPR_StrictAnchorSubstring 验证锚点匹配是严格子串（出现即命中，位置无关）。
func TestCommentOnGenTestsPR_StrictAnchorSubstring(t *testing.T) {
	// 锚点出现在评论中段也应命中（子串匹配）
	embedded := "标题\n\n" + genTestsDoneAnchor + "\n\n正文"
	mgr := &stubCommentManager{
		listReturn: []GiteaCommentInfo{
			{ID: 5, Body: embedded},
		},
	}
	n, err := NewGiteaNotifier(mgr)
	if err != nil {
		t.Fatalf("NewGiteaNotifier error: %v", err)
	}

	err = n.CommentOnGenTestsPR(context.Background(), "org", "repo", 42, "body")
	if err != nil {
		t.Fatalf("CommentOnGenTestsPR error: %v", err)
	}
	if len(mgr.editCalls) != 1 || mgr.editCalls[0].commentID != 5 {
		t.Errorf("子串锚点应命中 commentID=5，实际 edit=%+v", mgr.editCalls)
	}
}

// TestCommentOnGenTestsPR_NarrowClientFallback 当 client 只实现 GiteaCommentCreator 时退化为仅 Create。
func TestCommentOnGenTestsPR_NarrowClientFallback(t *testing.T) {
	stub := &stubCommentCreator{}
	n, err := NewGiteaNotifier(stub)
	if err != nil {
		t.Fatalf("NewGiteaNotifier error: %v", err)
	}

	err = n.CommentOnGenTestsPR(context.Background(), "org", "repo", 42, "body")
	if err != nil {
		t.Fatalf("CommentOnGenTestsPR error: %v", err)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("期望 Create 调用 1 次，实际 %d", len(stub.calls))
	}
	if !strings.HasPrefix(stub.calls[0].body, genTestsDoneAnchor) {
		t.Errorf("fallback body 未以锚点开头：%q", stub.calls[0].body)
	}
}

// TestCommentOnGenTestsPR_NarrowClientFallbackStripsNonBMPChars 验证窄接口 fallback
// 的仅 Create 路径也复用同一套 Gitea 文本清洗。
func TestCommentOnGenTestsPR_NarrowClientFallbackStripsNonBMPChars(t *testing.T) {
	stub := &stubCommentCreator{}
	n, err := NewGiteaNotifier(stub)
	if err != nil {
		t.Fatalf("NewGiteaNotifier error: %v", err)
	}

	err = n.CommentOnGenTestsPR(context.Background(), "org", "repo", 42, "body 🤖🚀")
	if err != nil {
		t.Fatalf("CommentOnGenTestsPR error: %v", err)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("期望 Create 调用 1 次，实际 %d", len(stub.calls))
	}
	got := stub.calls[0].body
	if strings.Contains(got, "🤖") || strings.Contains(got, "🚀") {
		t.Fatalf("fallback body 不应包含非 BMP 字符，实际：%q", got)
	}
}

// TestCommentOnGenTestsPR_InvalidTarget 参数校验：owner/repo 空、prNumber <= 0。
func TestCommentOnGenTestsPR_InvalidTarget(t *testing.T) {
	mgr := &stubCommentManager{}
	n, err := NewGiteaNotifier(mgr)
	if err != nil {
		t.Fatalf("NewGiteaNotifier error: %v", err)
	}

	cases := []struct {
		name  string
		owner string
		repo  string
		prNum int64
	}{
		{"空 owner", "", "repo", 1},
		{"空 repo", "org", "", 1},
		{"零 pr", "org", "repo", 0},
		{"负 pr", "org", "repo", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := n.CommentOnGenTestsPR(context.Background(), c.owner, c.repo, c.prNum, "body")
			if !errors.Is(err, ErrInvalidTarget) {
				t.Errorf("期望 ErrInvalidTarget，得到 %v", err)
			}
		})
	}
}

// TestCommentOnGenTestsPR_ListError List 调用失败应传播错误且不发评论。
func TestCommentOnGenTestsPR_ListError(t *testing.T) {
	listErr := errors.New("gitea API 5xx")
	mgr := &stubCommentManager{listErr: listErr}
	n, _ := NewGiteaNotifier(mgr)

	err := n.CommentOnGenTestsPR(context.Background(), "org", "repo", 1, "body")
	if err == nil {
		t.Fatal("期望返回错误")
	}
	if !errors.Is(err, listErr) {
		t.Errorf("错误链未包裹原始 list 错误：%v", err)
	}
	if !errors.Is(err, ErrSendFailed) {
		t.Errorf("错误链未包裹 ErrSendFailed：%v", err)
	}
	if len(mgr.createCalls) != 0 || len(mgr.editCalls) != 0 {
		t.Errorf("list 失败不应发评论")
	}
}

// TestGiteaNotifier_Send_WithDiscardLogger 验证传入 discard logger 不影响正常发送（LOW-2）
func TestGiteaNotifier_Send_WithDiscardLogger(t *testing.T) {
	stub := &stubCommentCreator{}
	discardLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	n, err := NewGiteaNotifier(stub, WithLogger(discardLogger))
	if err != nil {
		t.Fatalf("NewGiteaNotifier() with discard logger error: %v", err)
	}
	msg := Message{
		EventType: EventPRReviewDone,
		Severity:  SeverityInfo,
		Title:     "测试",
		Body:      "discard logger 测试",
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
	}
	if err := n.Send(context.Background(), msg); err != nil {
		t.Errorf("Send() with discard logger failed: %v", err)
	}
	if len(stub.calls) != 1 {
		t.Errorf("CreateIssueComment 应被调用 1 次，实际 %d 次", len(stub.calls))
	}
}
