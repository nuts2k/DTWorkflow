package notify

import (
	"context"
	"errors"
	"testing"
)

func makeRouter(t *testing.T, opts ...RouterOption) *Router {
	t.Helper()
	r, err := NewRouter(opts...)
	if err != nil {
		t.Fatalf("NewRouter() error: %v", err)
	}
	return r
}

func TestRouter_ExactRepoMatch(t *testing.T) {
	n := &stubNotifier{name: "gitea"}
	r := makeRouter(t,
		WithNotifier(n),
		WithRules([]RoutingRule{
			{RepoPattern: "org/repo", EventTypes: nil, Channels: []string{"gitea"}},
		}),
	)

	msg := Message{
		EventType: EventPRReviewDone,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
		Title:     "完成",
		Body:      "内容",
	}

	if err := r.Send(context.Background(), msg); err != nil {
		t.Errorf("Send() unexpected error: %v", err)
	}
	if len(n.calls) != 1 {
		t.Errorf("notifier called %d times, want 1", len(n.calls))
	}
}

func TestRouter_ExactRepoNoMatch(t *testing.T) {
	n := &stubNotifier{name: "gitea"}
	r := makeRouter(t,
		WithNotifier(n),
		WithRules([]RoutingRule{
			{RepoPattern: "org/other-repo", Channels: []string{"gitea"}},
		}),
	)

	msg := Message{
		EventType: EventPRReviewDone,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
	}

	err := r.Send(context.Background(), msg)
	if !errors.Is(err, ErrNoChannelMatched) {
		t.Errorf("Send() error = %v, want ErrNoChannelMatched", err)
	}
	if len(n.calls) != 0 {
		t.Errorf("notifier should not be called, got %d calls", len(n.calls))
	}
}

func TestRouter_WildcardRepoMatch(t *testing.T) {
	n := &stubNotifier{name: "gitea"}
	r := makeRouter(t,
		WithNotifier(n),
		WithRules([]RoutingRule{
			{RepoPattern: "*", Channels: []string{"gitea"}},
		}),
	)

	for _, owner := range []string{"org1", "org2", "another"} {
		msg := Message{
			EventType: EventPRReviewDone,
			Target:    Target{Owner: owner, Repo: "any-repo", Number: 1},
			Title:     "测试",
			Body:      "内容",
		}
		if err := r.Send(context.Background(), msg); err != nil {
			t.Errorf("Send() for owner=%q unexpected error: %v", owner, err)
		}
	}

	if len(n.calls) != 3 {
		t.Errorf("notifier called %d times, want 3", len(n.calls))
	}
}

func TestRouter_EventTypeFilter_Match(t *testing.T) {
	n := &stubNotifier{name: "gitea"}
	r := makeRouter(t,
		WithNotifier(n),
		WithRules([]RoutingRule{
			{
				RepoPattern: "*",
				EventTypes:  []EventType{EventPRReviewDone, EventPRRejected},
				Channels:    []string{"gitea"},
			},
		}),
	)

	msg := Message{
		EventType: EventPRReviewDone,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
		Title:     "完成",
		Body:      "内容",
	}

	if err := r.Send(context.Background(), msg); err != nil {
		t.Errorf("Send() unexpected error: %v", err)
	}
	if len(n.calls) != 1 {
		t.Errorf("notifier called %d times, want 1", len(n.calls))
	}
}

func TestRouter_EventTypeFilter_NoMatch(t *testing.T) {
	n := &stubNotifier{name: "gitea"}
	r := makeRouter(t,
		WithNotifier(n),
		WithRules([]RoutingRule{
			{
				RepoPattern: "*",
				EventTypes:  []EventType{EventPRReviewDone},
				Channels:    []string{"gitea"},
			},
		}),
	)

	msg := Message{
		EventType: EventSystemError,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
	}

	err := r.Send(context.Background(), msg)
	if !errors.Is(err, ErrNoChannelMatched) {
		t.Errorf("Send() error = %v, want ErrNoChannelMatched", err)
	}
}

func TestRouter_Fallback(t *testing.T) {
	n := &stubNotifier{name: "fallback-channel"}
	r := makeRouter(t,
		WithNotifier(n),
		WithRules([]RoutingRule{
			{RepoPattern: "org/other", Channels: []string{"fallback-channel"}},
		}),
		WithFallback("fallback-channel"),
	)

	// 规则不匹配，应使用 fallback
	msg := Message{
		EventType: EventSystemError,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
		Title:     "错误",
		Body:      "内容",
	}

	if err := r.Send(context.Background(), msg); err != nil {
		t.Errorf("Send() unexpected error: %v", err)
	}
	if len(n.calls) != 1 {
		t.Errorf("fallback notifier called %d times, want 1", len(n.calls))
	}
}

func TestRouter_NoFallback_NoMatch(t *testing.T) {
	n := &stubNotifier{name: "gitea"}
	r := makeRouter(t,
		WithNotifier(n),
		// 没有规则，没有 fallback
	)

	msg := Message{
		EventType: EventSystemError,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
	}

	err := r.Send(context.Background(), msg)
	if !errors.Is(err, ErrNoChannelMatched) {
		t.Errorf("Send() error = %v, want ErrNoChannelMatched", err)
	}
}

func TestRouter_MultiRuleDeduplication(t *testing.T) {
	n := &stubNotifier{name: "gitea"}
	r := makeRouter(t,
		WithNotifier(n),
		WithRules([]RoutingRule{
			{RepoPattern: "*", Channels: []string{"gitea"}},
			{RepoPattern: "org/repo", Channels: []string{"gitea"}},
		}),
	)

	msg := Message{
		EventType: EventPRReviewDone,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
		Title:     "完成",
		Body:      "内容",
	}

	if err := r.Send(context.Background(), msg); err != nil {
		t.Errorf("Send() unexpected error: %v", err)
	}
	// 两条规则都匹配同一渠道，去重后只发送一次
	if len(n.calls) != 1 {
		t.Errorf("notifier called %d times (dedup should result in 1)", len(n.calls))
	}
}

func TestRouter_MultiChannel_PartialFailure(t *testing.T) {
	failErr := errors.New("发送失败")
	nOK := &stubNotifier{name: "ok-channel"}
	nFail := &stubNotifier{name: "fail-channel", sendErr: failErr}

	r := makeRouter(t,
		WithNotifier(nOK),
		WithNotifier(nFail),
		WithRules([]RoutingRule{
			{RepoPattern: "*", Channels: []string{"ok-channel", "fail-channel"}},
		}),
	)

	msg := Message{
		EventType: EventPRReviewDone,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
		Title:     "完成",
		Body:      "内容",
	}

	err := r.Send(context.Background(), msg)
	// 应该有错误（fail-channel 失败）
	if err == nil {
		t.Fatal("Send() should return error when one channel fails")
	}

	// ok-channel 应该仍然被调用
	if len(nOK.calls) != 1 {
		t.Errorf("ok-channel called %d times, want 1", len(nOK.calls))
	}
	// fail-channel 也应该被调用
	if len(nFail.calls) != 1 {
		t.Errorf("fail-channel called %d times, want 1", len(nFail.calls))
	}
	// 错误应包含 failErr
	if !errors.Is(err, failErr) {
		t.Errorf("error should wrap failErr, got: %v", err)
	}
}

func TestRouter_UnregisteredChannel(t *testing.T) {
	r := makeRouter(t,
		WithRules([]RoutingRule{
			{RepoPattern: "*", Channels: []string{"nonexistent-channel"}},
		}),
	)

	msg := Message{
		EventType: EventPRReviewDone,
		Target:    Target{Owner: "org", Repo: "repo", Number: 1},
	}

	err := r.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("Send() should return error for unregistered channel")
	}
	if !errors.Is(err, ErrNotifierNotFound) {
		t.Errorf("error should wrap ErrNotifierNotFound, got: %v", err)
	}
}

func TestRouter_WildcardEventType(t *testing.T) {
	n := &stubNotifier{name: "gitea"}
	r := makeRouter(t,
		WithNotifier(n),
		WithRules([]RoutingRule{
			{
				RepoPattern: "*",
				EventTypes:  []EventType{"*"},
				Channels:    []string{"gitea"},
			},
		}),
	)

	for _, et := range []EventType{EventPRReviewDone, EventSystemError, EventE2ETestFailed} {
		msg := Message{
			EventType: et,
			Target:    Target{Owner: "org", Repo: "repo", Number: 1},
			Title:     "测试",
			Body:      "内容",
		}
		if err := r.Send(context.Background(), msg); err != nil {
			t.Errorf("Send() for event=%q unexpected error: %v", et, err)
		}
	}

	if len(n.calls) != 3 {
		t.Errorf("notifier called %d times, want 3", len(n.calls))
	}
}

func TestNewRouter_NilNotifier(t *testing.T) {
	_, err := NewRouter(WithNotifier(nil))
	if err == nil {
		t.Fatal("NewRouter with nil notifier should return error")
	}
}
