package report

import (
	"context"
	"fmt"
	"testing"
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
