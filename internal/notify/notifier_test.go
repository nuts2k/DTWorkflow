package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// stubNotifier 用于测试的桩通知渠道，并发安全。
type stubNotifier struct {
	mu      sync.Mutex
	name    string
	sendErr error
	calls   []Message
}

func (s *stubNotifier) Name() string { return s.name }

func (s *stubNotifier) Send(_ context.Context, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, msg)
	return s.sendErr
}

// 确保 stubNotifier 实现了 Notifier 接口
var _ Notifier = (*stubNotifier)(nil)

func TestStubNotifier_Name(t *testing.T) {
	n := &stubNotifier{name: "test-channel"}
	if got := n.Name(); got != "test-channel" {
		t.Errorf("Name() = %q, want %q", got, "test-channel")
	}
}

func TestStubNotifier_Send_Success(t *testing.T) {
	n := &stubNotifier{name: "test-channel"}
	msg := Message{
		EventType: EventPRReviewDone,
		Severity:  SeverityInfo,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1, IsPR: true},
		Title:     "评审完成",
		Body:      "PR 已通过评审",
	}

	if err := n.Send(context.Background(), msg); err != nil {
		t.Errorf("Send() unexpected error: %v", err)
	}
	if len(n.calls) != 1 {
		t.Fatalf("Send() called %d times, want 1", len(n.calls))
	}
	if n.calls[0].EventType != EventPRReviewDone {
		t.Errorf("Send() got EventType %q, want %q", n.calls[0].EventType, EventPRReviewDone)
	}
}

func TestStubNotifier_Send_Error(t *testing.T) {
	sendErr := errors.New("发送失败")
	n := &stubNotifier{name: "failing-channel", sendErr: sendErr}
	msg := Message{EventType: EventSystemError}

	if err := n.Send(context.Background(), msg); !errors.Is(err, sendErr) {
		t.Errorf("Send() error = %v, want %v", err, sendErr)
	}
}

func TestEventTypeConstants(t *testing.T) {
	types := []EventType{
		EventPRReviewDone,
		EventPRRejected,
		EventIssueAnalysisDone,
		EventIssueNeedInfo,
		EventFixPRCreated,
		EventE2ETestFailed,
		EventSystemError,
	}
	for _, et := range types {
		if et == "" {
			t.Error("EventType constant should not be empty")
		}
	}
}

func TestSeverityConstants(t *testing.T) {
	severities := []Severity{SeverityInfo, SeverityWarning, SeverityCritical}
	for _, s := range severities {
		if s == "" {
			t.Error("Severity constant should not be empty")
		}
	}
}
