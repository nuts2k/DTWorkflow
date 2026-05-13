package store

import (
	"context"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func TestFindActiveIterationSession_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	session, err := s.FindActiveIterationSession(ctx, "owner/repo", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session != nil {
		t.Fatal("expected nil session")
	}
}

func TestFindOrCreateIterationSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 首次创建
	s1, err := s.FindOrCreateIterationSession(ctx, "owner/repo", 42, "feature-branch", 3)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if s1.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	if s1.Status != "idle" {
		t.Errorf("status = %q, want idle", s1.Status)
	}
	if s1.MaxRounds != 3 {
		t.Errorf("max_rounds = %d, want 3", s1.MaxRounds)
	}

	// 再次调用返回同一会话
	s2, err := s.FindOrCreateIterationSession(ctx, "owner/repo", 42, "feature-branch", 5)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if s2.ID != s1.ID {
		t.Errorf("expected same session ID %d, got %d", s1.ID, s2.ID)
	}
	// max_rounds 不被第二次调用覆盖
	if s2.MaxRounds != 3 {
		t.Errorf("max_rounds should stay 3, got %d", s2.MaxRounds)
	}
}

func TestFindActiveIterationSession_IgnoresTerminal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 创建并标记为 completed
	session, _ := s.FindOrCreateIterationSession(ctx, "owner/repo", 1, "branch", 3)
	session.Status = "completed"
	if err := s.UpdateIterationSession(ctx, session); err != nil {
		t.Fatalf("update: %v", err)
	}

	// 活跃查询应返回 nil
	found, err := s.FindActiveIterationSession(ctx, "owner/repo", 1)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found != nil {
		t.Fatal("expected nil for terminal session")
	}
}

func TestCountNonRecoveryRounds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	session, _ := s.FindOrCreateIterationSession(ctx, "owner/repo", 1, "branch", 5)

	// 创建 2 个正常轮次 + 1 个恢复轮次
	for i, recovery := range []bool{false, false, true} {
		if err := s.CreateIterationRound(ctx, &IterationRoundRecord{
			SessionID:   session.ID,
			RoundNumber: i + 1,
			IsRecovery:  recovery,
		}); err != nil {
			t.Fatalf("create round %d: %v", i+1, err)
		}
	}

	count, err := s.CountNonRecoveryRounds(ctx, session.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestFindActivePRTasksMulti(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tasks, err := s.FindActivePRTasksMulti(ctx, "owner/repo", 1, []model.TaskType{"review_pr", "fix_review"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetRecentRoundsIssuesFixed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	session, _ := s.FindOrCreateIterationSession(ctx, "owner/repo", 1, "branch", 5)
	for i := 1; i <= 3; i++ {
		if err := s.CreateIterationRound(ctx, &IterationRoundRecord{
			SessionID:   session.ID,
			RoundNumber: i,
			IssuesFixed: i * 2,
		}); err != nil {
			t.Fatalf("create round %d: %v", i, err)
		}
	}

	fixed, err := s.GetRecentRoundsIssuesFixed(ctx, session.ID, 2)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(fixed) != 2 {
		t.Fatalf("expected 2 results, got %d", len(fixed))
	}
	// 最近的在前：round 3 (6), round 2 (4)
	if fixed[0] != 6 || fixed[1] != 4 {
		t.Errorf("fixed = %v, want [6, 4]", fixed)
	}
}
