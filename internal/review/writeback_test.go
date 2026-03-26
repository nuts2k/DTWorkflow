package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// --- Mock 实现 ---

type mockWritebackClient struct {
	createPullReview   func(ctx context.Context, owner, repo string, index int64, opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error)
	getPullRequestDiff func(ctx context.Context, owner, repo string, index int64) (string, *gitea.Response, error)
}

func (m *mockWritebackClient) CreatePullReview(ctx context.Context, owner, repo string, index int64, opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
	if m.createPullReview != nil {
		return m.createPullReview(ctx, owner, repo, index, opts)
	}
	return &gitea.PullReview{ID: 42}, nil, nil
}

func (m *mockWritebackClient) GetPullRequestDiff(ctx context.Context, owner, repo string, index int64) (string, *gitea.Response, error) {
	if m.getPullRequestDiff != nil {
		return m.getPullRequestDiff(ctx, owner, repo, index)
	}
	return "", nil, nil
}

type mockReviewStore struct {
	saveReviewResult func(ctx context.Context, result *model.ReviewRecord) error
	saved            *model.ReviewRecord
}

func (m *mockReviewStore) SaveReviewResult(ctx context.Context, result *model.ReviewRecord) error {
	m.saved = result
	if m.saveReviewResult != nil {
		return m.saveReviewResult(ctx, result)
	}
	return nil
}

// --- 辅助函数 ---

// simpleDiff 返回一个仅包含 foo.go 行 1..3 的 diff 文本
const simpleDiff = `diff --git a/foo.go b/foo.go
index abc..def 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package main
+
+import "fmt"
 func main() {}
`

func approveResult() *ReviewResult {
	return &ReviewResult{
		RawOutput: `{"type":"result","subtype":"success","is_error":false,"result":"{}"}`,
		CLIMeta:   &CLIMeta{CostUSD: 0.01, DurationMs: 1000},
		Review: &ReviewOutput{
			Summary: "looks good",
			Verdict: VerdictApprove,
			Issues:  []ReviewIssue{},
		},
	}
}

func requestChangesResult(issues []ReviewIssue) *ReviewResult {
	return &ReviewResult{
		RawOutput: `{"type":"result","subtype":"success","is_error":false,"result":"{}"}`,
		CLIMeta:   &CLIMeta{CostUSD: 0.02, DurationMs: 2000},
		Review: &ReviewOutput{
			Summary: "有问题",
			Verdict: VerdictRequestChanges,
			Issues:  issues,
		},
	}
}

func parseFailedResult() *ReviewResult {
	return &ReviewResult{
		RawOutput:  "raw claude output",
		CLIMeta:    &CLIMeta{CostUSD: 0.005, DurationMs: 500},
		Review:     nil,
		ParseError: errors.New("JSON 解析失败"),
	}
}

func defaultInput(result *ReviewResult) WritebackInput {
	return WritebackInput{
		TaskID:   "task-1",
		Owner:    "owner",
		Repo:     "repo",
		PRNumber: 1,
		HeadSHA:  "abc123",
		Result:   result,
	}
}

// --- 测试用例 ---

// TestWrite_NormalPath 正常路径：approve，无 issues，返回 giteaReviewID
func TestWrite_NormalPath(t *testing.T) {
	store := &mockReviewStore{}
	client := &mockWritebackClient{
		getPullRequestDiff: func(_ context.Context, _, _ string, _ int64) (string, *gitea.Response, error) {
			return simpleDiff, nil, nil
		},
		createPullReview: func(_ context.Context, _, _ string, _ int64, opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			if opts.State != gitea.ReviewStateApproved {
				t.Errorf("期望 state=APPROVED，实际: %s", opts.State)
			}
			return &gitea.PullReview{ID: 99}, nil, nil
		},
	}

	w := NewWriter(client, store, nil, nil)
	id, err := w.Write(context.Background(), defaultInput(approveResult()))
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}
	if id != 99 {
		t.Errorf("期望 giteaReviewID=99，实际: %d", id)
	}
	if store.saved == nil {
		t.Error("store.SaveReviewResult 应被调用")
	}
	if store.saved.ID == "" {
		t.Error("store 记录 ID 不应为空")
	}
	if store.saved.CreatedAt.IsZero() {
		t.Error("store 记录 CreatedAt 不应为零值")
	}
	if store.saved.GiteaReviewID != 99 {
		t.Errorf("store 记录 GiteaReviewID 期望 99，实际: %d", store.saved.GiteaReviewID)
	}
}

// TestWrite_PartialMapping 部分 issues 无法映射到 diff 行，归入 body
func TestWrite_PartialMapping(t *testing.T) {
	var capturedOpts gitea.CreatePullReviewOptions
	client := &mockWritebackClient{
		getPullRequestDiff: func(_ context.Context, _, _ string, _ int64) (string, *gitea.Response, error) {
			return simpleDiff, nil, nil
		},
		createPullReview: func(_ context.Context, _, _ string, _ int64, opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			capturedOpts = opts
			return &gitea.PullReview{ID: 1}, nil, nil
		},
	}

	issues := []ReviewIssue{
		// 行 2 在 simpleDiff hunk 范围内（1..4），可映射
		{File: "foo.go", Line: 2, Severity: "WARNING", Category: "style", Message: "mapped issue"},
		// 行 999 不在范围内，归入 body
		{File: "foo.go", Line: 999, Severity: "INFO", Category: "style", Message: "unmapped issue"},
	}

	result := requestChangesResult(issues)
	w := NewWriter(client, nil, nil, nil)
	_, err := w.Write(context.Background(), defaultInput(result))
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}

	// 应有 1 个行级评论
	if len(capturedOpts.Comments) != 1 {
		t.Errorf("期望 1 个行级评论，实际: %d", len(capturedOpts.Comments))
	}
	// body 中应包含未映射的 issue
	if capturedOpts.Body == "" {
		t.Error("body 不应为空")
	}
}

// TestWrite_AllUnmapped diff 为空时所有 issues 归入 body
func TestWrite_AllUnmapped(t *testing.T) {
	var capturedOpts gitea.CreatePullReviewOptions
	client := &mockWritebackClient{
		getPullRequestDiff: func(_ context.Context, _, _ string, _ int64) (string, *gitea.Response, error) {
			return "", nil, nil // 空 diff
		},
		createPullReview: func(_ context.Context, _, _ string, _ int64, opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			capturedOpts = opts
			return &gitea.PullReview{ID: 1}, nil, nil
		},
	}

	issues := []ReviewIssue{
		{File: "foo.go", Line: 1, Severity: "ERROR", Category: "logic", Message: "issue1"},
		{File: "bar.go", Line: 5, Severity: "WARNING", Category: "style", Message: "issue2"},
	}

	result := requestChangesResult(issues)
	w := NewWriter(client, nil, nil, nil)
	_, err := w.Write(context.Background(), defaultInput(result))
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}

	// 空 diff → 无行级评论
	if len(capturedOpts.Comments) != 0 {
		t.Errorf("空 diff 时期望 0 个行级评论，实际: %d", len(capturedOpts.Comments))
	}
}

// TestWrite_ParseErrorFallback parse 失败时降级为 COMMENT + 原始输出附加到 body
func TestWrite_ParseErrorFallback(t *testing.T) {
	var capturedOpts gitea.CreatePullReviewOptions
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			capturedOpts = opts
			return &gitea.PullReview{ID: 1}, nil, nil
		},
	}

	w := NewWriter(client, nil, nil, nil)
	_, err := w.Write(context.Background(), defaultInput(parseFailedResult()))
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}

	// 解析失败时 state 应为 COMMENT
	if capturedOpts.State != gitea.ReviewStateComment {
		t.Errorf("解析失败时期望 state=COMMENT，实际: %s", capturedOpts.State)
	}
	// body 中应包含原始输出提示
	if capturedOpts.Body == "" {
		t.Error("body 不应为空")
	}
	// 无行级评论
	if len(capturedOpts.Comments) != 0 {
		t.Errorf("解析失败时期望 0 个行级评论，实际: %d", len(capturedOpts.Comments))
	}
}

// TestMapVerdict_Basic 基本 verdict 映射
func TestMapVerdict_Basic(t *testing.T) {
	tests := []struct {
		name          string
		verdict       VerdictType
		issues        []ReviewIssue
		hasParseError bool
		want          gitea.ReviewStateType
	}{
		{
			name:    "approve -> APPROVED",
			verdict: VerdictApprove,
			want:    gitea.ReviewStateApproved,
		},
		{
			name:    "request_changes -> REQUEST_CHANGES",
			verdict: VerdictRequestChanges,
			want:    gitea.ReviewStateRequestChanges,
		},
		{
			name:    "comment -> COMMENT",
			verdict: VerdictComment,
			want:    gitea.ReviewStateComment,
		},
		{
			name:    "空 verdict -> COMMENT",
			verdict: "",
			want:    gitea.ReviewStateComment,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapVerdict(tc.verdict, tc.issues, tc.hasParseError)
			if got != tc.want {
				t.Errorf("mapVerdict(%s) = %s，期望 %s", tc.verdict, got, tc.want)
			}
		})
	}
}

// TestMapVerdict_SafetyNet_ParseError 解析失败时安全网强制 COMMENT
func TestMapVerdict_SafetyNet_ParseError(t *testing.T) {
	// 即使 verdict=approve，解析失败时应返回 COMMENT
	got := mapVerdict(VerdictApprove, nil, true)
	if got != gitea.ReviewStateComment {
		t.Errorf("解析失败安全网：期望 COMMENT，实际: %s", got)
	}
}

// TestMapVerdict_SafetyNet_CriticalError CRITICAL/ERROR 时强制 REQUEST_CHANGES
func TestMapVerdict_SafetyNet_CriticalError(t *testing.T) {
	tests := []struct {
		name    string
		verdict VerdictType
		issues  []ReviewIssue
	}{
		{
			name:    "approve + CRITICAL -> REQUEST_CHANGES",
			verdict: VerdictApprove,
			issues:  []ReviewIssue{{Severity: "CRITICAL", Message: "critical issue"}},
		},
		{
			name:    "approve + ERROR -> REQUEST_CHANGES",
			verdict: VerdictApprove,
			issues:  []ReviewIssue{{Severity: "ERROR", Message: "error issue"}},
		},
		{
			name:    "comment + CRITICAL -> REQUEST_CHANGES",
			verdict: VerdictComment,
			issues:  []ReviewIssue{{Severity: "CRITICAL", Message: "critical issue"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapVerdict(tc.verdict, tc.issues, false)
			if got != gitea.ReviewStateRequestChanges {
				t.Errorf("安全网：期望 REQUEST_CHANGES，实际: %s", got)
			}
		})
	}
}

// TestMapVerdict_SafetyNet_WarningNotForced WARNING 不触发安全网
func TestMapVerdict_SafetyNet_WarningNotForced(t *testing.T) {
	issues := []ReviewIssue{{Severity: "WARNING", Message: "just a warning"}}
	got := mapVerdict(VerdictApprove, issues, false)
	if got != gitea.ReviewStateApproved {
		t.Errorf("WARNING 不应触发安全网，期望 APPROVED，实际: %s", got)
	}
}

// TestWrite_DiffFetchFailed diff 获取失败时降级，但需把降级错误返回给调用方
func TestWrite_DiffFetchFailed(t *testing.T) {
	var capturedOpts gitea.CreatePullReviewOptions
	store := &mockReviewStore{}
	client := &mockWritebackClient{
		getPullRequestDiff: func(_ context.Context, _, _ string, _ int64) (string, *gitea.Response, error) {
			return "", nil, errors.New("network error")
		},
		createPullReview: func(_ context.Context, _, _ string, _ int64, opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			capturedOpts = opts
			return &gitea.PullReview{ID: 1}, nil, nil
		},
	}

	issues := []ReviewIssue{
		{File: "foo.go", Line: 1, Severity: "WARNING", Category: "style", Message: "some warning"},
	}
	result := requestChangesResult(issues)
	w := NewWriter(client, store, nil, nil)
	id, err := w.Write(context.Background(), defaultInput(result))
	if err == nil {
		t.Fatal("diff 获取失败时应返回降级错误")
	}
	if id != 1 {
		t.Fatalf("降级回写仍应返回 review ID，实际: %d", id)
	}
	if !strings.Contains(err.Error(), "获取 PR diff 失败") {
		t.Fatalf("错误信息应包含 diff 获取失败，实际: %v", err)
	}

	// diff 失败 → 无行级评论
	if len(capturedOpts.Comments) != 0 {
		t.Errorf("diff 获取失败时期望 0 个行级评论，实际: %d", len(capturedOpts.Comments))
	}
	if store.saved == nil {
		t.Fatal("降级场景也应持久化 review 结果")
	}
	if store.saved.WritebackError == "" {
		t.Fatal("降级场景应持久化 writeback_error")
	}
}

// TestWrite_APIFailed CreatePullReview API 失败时返回错误
func TestWrite_APIFailed(t *testing.T) {
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, _ gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			return nil, nil, errors.New("API 请求失败")
		},
	}

	w := NewWriter(client, nil, nil, nil)
	_, err := w.Write(context.Background(), defaultInput(approveResult()))
	if err == nil {
		t.Fatal("API 失败时应返回错误")
	}
}

// TestWrite_StoreFailed store 持久化失败不影响回写结果（仅日志）
func TestWrite_StoreFailed(t *testing.T) {
	store := &mockReviewStore{
		saveReviewResult: func(_ context.Context, _ *model.ReviewRecord) error {
			return errors.New("数据库写入失败")
		},
	}
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, _ gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			return &gitea.PullReview{ID: 77}, nil, nil
		},
	}

	w := NewWriter(client, store, nil, nil)
	id, err := w.Write(context.Background(), defaultInput(approveResult()))
	// store 失败不应影响主流程
	if err != nil {
		t.Fatalf("store 失败时主流程不应返回错误，实际: %v", err)
	}
	if id != 77 {
		t.Errorf("期望 giteaReviewID=77，实际: %d", id)
	}
}

// TestWrite_NilResult result 为 nil 时返回错误
func TestWrite_NilResult(t *testing.T) {
	client := &mockWritebackClient{}
	w := NewWriter(client, nil, nil, nil)
	input := defaultInput(nil)
	_, err := w.Write(context.Background(), input)
	if err == nil {
		t.Fatal("result 为 nil 时应返回错误")
	}
}

// TestWrite_NilStore store 为 nil 时跳过持久化，正常完成
func TestWrite_NilStore(t *testing.T) {
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, _ gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			return &gitea.PullReview{ID: 1}, nil, nil
		},
	}

	w := NewWriter(client, nil, nil, nil)
	_, err := w.Write(context.Background(), defaultInput(approveResult()))
	if err != nil {
		t.Fatalf("store 为 nil 时应正常完成，实际: %v", err)
	}
}

// TestMapIssuesToComments_AllMapped 所有 issues 都能映射
func TestMapIssuesToComments_AllMapped(t *testing.T) {
	dm := ParseDiff(simpleDiff)
	issues := []ReviewIssue{
		{File: "foo.go", Line: 1, Severity: "WARNING", Message: "line 1"},
		{File: "foo.go", Line: 2, Severity: "INFO", Message: "line 2"},
	}

	results := mapIssuesToComments(dm, issues)
	for i, mr := range results {
		if !mr.Mapped {
			t.Errorf("issue[%d] 应能映射，file=%s line=%d", i, mr.Issue.File, mr.Issue.Line)
		}
	}
}

// TestMapIssuesToComments_NilDiffMap diffMap 为 nil 时所有 issues 标记为未映射
func TestMapIssuesToComments_NilDiffMap(t *testing.T) {
	issues := []ReviewIssue{
		{File: "foo.go", Line: 1, Severity: "WARNING", Message: "msg"},
	}

	results := mapIssuesToComments(nil, issues)
	for i, mr := range results {
		if mr.Mapped {
			t.Errorf("diffMap 为 nil 时 issue[%d] 应标记为未映射", i)
		}
	}
}

// TestMapIssuesToComments_EmptyIssues 空 issues 不 panic
func TestMapIssuesToComments_EmptyIssues(t *testing.T) {
	dm := ParseDiff(simpleDiff)
	results := mapIssuesToComments(dm, nil)
	if len(results) != 0 {
		t.Errorf("空 issues 期望 0 个结果，实际: %d", len(results))
	}
}

// TestWrite_HeadSHAPassedToGitea HeadSHA 传递到 CreatePullReview
func TestWrite_HeadSHAPassedToGitea(t *testing.T) {
	var capturedCommitID string
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			capturedCommitID = opts.CommitID
			return &gitea.PullReview{ID: 1}, nil, nil
		},
	}

	input := defaultInput(approveResult())
	input.HeadSHA = "sha-xyz-123"

	w := NewWriter(client, nil, nil, nil)
	_, err := w.Write(context.Background(), input)
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}
	if capturedCommitID != "sha-xyz-123" {
		t.Errorf("期望 commit_id=sha-xyz-123，实际: %s", capturedCommitID)
	}
}

// --- M2.4 staleness check mock ---

type mockStaleChecker struct {
	hasNewer    bool
	hasNewerErr error
}

func (m *mockStaleChecker) HasNewerReviewTask(_ context.Context, _ string, _ int64, _ time.Time) (bool, error) {
	return m.hasNewer, m.hasNewerErr
}

// --- M2.4 staleness check 测试 ---

// TestWrite_StalenessCheck_NotStale StaleChecker 返回 false，正常回写
func TestWrite_StalenessCheck_NotStale(t *testing.T) {
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, _ gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			return &gitea.PullReview{ID: 10}, nil, nil
		},
	}
	checker := &mockStaleChecker{hasNewer: false}

	input := defaultInput(approveResult())
	input.TaskCreatedAt = time.Now().Add(-time.Minute)

	w := NewWriter(client, nil, checker, nil)
	id, err := w.Write(context.Background(), input)
	if err != nil {
		t.Fatalf("非过时任务不应返回错误，实际: %v", err)
	}
	if id != 10 {
		t.Errorf("期望 giteaReviewID=10，实际: %d", id)
	}
}

// TestWrite_StalenessCheck_Stale StaleChecker 返回 true，跳过回写，返回 ErrStaleReview
func TestWrite_StalenessCheck_Stale(t *testing.T) {
	// createPullReview 不应被调用
	called := false
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, _ gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			called = true
			return &gitea.PullReview{ID: 1}, nil, nil
		},
	}
	checker := &mockStaleChecker{hasNewer: true}

	input := defaultInput(approveResult())
	input.TaskCreatedAt = time.Now().Add(-time.Minute)

	w := NewWriter(client, nil, checker, nil)
	id, err := w.Write(context.Background(), input)
	if !errors.Is(err, ErrStaleReview) {
		t.Fatalf("过时任务应返回 ErrStaleReview，实际: %v", err)
	}
	if id != 0 {
		t.Errorf("过时任务应返回 id=0，实际: %d", id)
	}
	if called {
		t.Error("过时任务不应调用 CreatePullReview")
	}
}

// TestWrite_StalenessCheck_Error StaleChecker 返回 error，fail-open 继续回写
func TestWrite_StalenessCheck_Error(t *testing.T) {
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, _ gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			return &gitea.PullReview{ID: 20}, nil, nil
		},
	}
	checker := &mockStaleChecker{hasNewerErr: errors.New("数据库连接失败")}

	input := defaultInput(approveResult())
	input.TaskCreatedAt = time.Now().Add(-time.Minute)

	w := NewWriter(client, nil, checker, nil)
	id, err := w.Write(context.Background(), input)
	// fail-open：检查失败时继续回写，不返回 stale 错误
	if errors.Is(err, ErrStaleReview) {
		t.Fatal("staleness check 失败时不应返回 ErrStaleReview")
	}
	if id != 20 {
		t.Errorf("fail-open 后应正常回写，期望 id=20，实际: %d", id)
	}
}

// TestWrite_StalenessCheck_NilChecker StaleChecker 为 nil 时跳过检查，正常回写
func TestWrite_StalenessCheck_NilChecker(t *testing.T) {
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, _ gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			return &gitea.PullReview{ID: 30}, nil, nil
		},
	}

	input := defaultInput(approveResult())
	input.TaskCreatedAt = time.Now().Add(-time.Minute)

	// staleChecker 传 nil
	w := NewWriter(client, nil, nil, nil)
	id, err := w.Write(context.Background(), input)
	if err != nil {
		t.Fatalf("nil StaleChecker 时应正常完成，实际: %v", err)
	}
	if id != 30 {
		t.Errorf("期望 giteaReviewID=30，实际: %d", id)
	}
}

// TestWrite_SupersededAnnotation SupersededCount > 0 时 body 应包含替代标注
func TestWrite_SupersededAnnotation(t *testing.T) {
	var capturedBody string
	client := &mockWritebackClient{
		createPullReview: func(_ context.Context, _, _ string, _ int64, opts gitea.CreatePullReviewOptions) (*gitea.PullReview, *gitea.Response, error) {
			capturedBody = opts.Body
			return &gitea.PullReview{ID: 1}, nil, nil
		},
	}

	input := defaultInput(approveResult())
	input.SupersededCount = 1
	input.PreviousHeadSHA = "deadbeef9999"

	w := NewWriter(client, nil, nil, nil)
	_, err := w.Write(context.Background(), input)
	if err != nil {
		t.Fatalf("预期无错误，实际: %v", err)
	}
	// body 应包含短 SHA（前 7 位）的替代标注
	if !strings.Contains(capturedBody, "替代了之前基于 `deadbee` 的评审") {
		t.Errorf("body 应包含替代标注，实际 body=%s", capturedBody)
	}
}
