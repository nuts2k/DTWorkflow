package report

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockCardSender 模拟 CardSender
type mockCardSender struct {
	sent []map[string]any
	err  error
}

func (m *mockCardSender) SendCard(_ context.Context, card map[string]any) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, card)
	return nil
}

func TestReportGenerator_Generate_WithData(t *testing.T) {
	store := &mockReviewStore{
		results: testReviewRecords(),
	}
	sender := &mockCardSender{}

	gen := NewReportGenerator(
		NewReviewStatCollector(store),
		sender,
		"Asia/Shanghai",
		false, // skipEmpty
	)

	if err := gen.Generate(context.Background()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 card sent, got %d", len(sender.sent))
	}
	card := sender.sent[0]
	if card["msg_type"] != "interactive" {
		t.Errorf("msg_type = %v, want interactive", card["msg_type"])
	}
}

func TestReportGenerator_Generate_Empty_SkipFalse(t *testing.T) {
	store := &mockReviewStore{results: nil}
	sender := &mockCardSender{}

	gen := NewReportGenerator(NewReviewStatCollector(store), sender, "Asia/Shanghai", false)
	if err := gen.Generate(context.Background()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 card (empty report), got %d", len(sender.sent))
	}
}

func TestReportGenerator_Generate_Empty_SkipTrue(t *testing.T) {
	store := &mockReviewStore{results: nil}
	sender := &mockCardSender{}

	gen := NewReportGenerator(NewReviewStatCollector(store), sender, "Asia/Shanghai", true)
	if err := gen.Generate(context.Background()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("expected 0 cards (skip empty), got %d", len(sender.sent))
	}
}

func TestReportGenerator_Generate_SenderError(t *testing.T) {
	store := &mockReviewStore{results: testReviewRecords()}
	sender := &mockCardSender{err: fmt.Errorf("webhook timeout")}

	gen := NewReportGenerator(NewReviewStatCollector(store), sender, "Asia/Shanghai", false)
	err := gen.Generate(context.Background())
	if err == nil {
		t.Fatal("expected error from sender")
	}
}

func TestReportRanges_AsiaShanghaiCalendarBoundaries(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	yesterday, dayBefore := reportRanges(time.Date(2026, 4, 6, 9, 30, 0, 0, loc), loc)

	if got, want := yesterday.Start, time.Date(2026, 4, 5, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("yesterday.Start = %v, want %v", got, want)
	}
	if got, want := yesterday.End, time.Date(2026, 4, 6, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("yesterday.End = %v, want %v", got, want)
	}
	if got, want := dayBefore.Start, time.Date(2026, 4, 4, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("dayBefore.Start = %v, want %v", got, want)
	}
	if got, want := dayBefore.End, time.Date(2026, 4, 5, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("dayBefore.End = %v, want %v", got, want)
	}
}

func TestReportRanges_DSTUsesCalendarMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	yesterday, dayBefore := reportRanges(time.Date(2026, 3, 9, 12, 0, 0, 0, loc), loc)

	if got, want := yesterday.Start, time.Date(2026, 3, 8, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("yesterday.Start = %v, want %v", got, want)
	}
	if got, want := yesterday.End, time.Date(2026, 3, 9, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("yesterday.End = %v, want %v", got, want)
	}
	if got, want := dayBefore.Start, time.Date(2026, 3, 7, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("dayBefore.Start = %v, want %v", got, want)
	}
	if got, want := dayBefore.End, time.Date(2026, 3, 8, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("dayBefore.End = %v, want %v", got, want)
	}
}
