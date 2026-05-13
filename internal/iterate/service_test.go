package iterate

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

type mockPool struct {
	output string
	err    error
}

func (m *mockPool) RunWithCommandAndStdin(_ context.Context, _ model.TaskPayload,
	_ []string, _ []byte) (*worker.ExecutionResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &worker.ExecutionResult{
		ExitCode: 0,
		Output:   m.output,
	}, nil
}

func TestService_Execute_Success(t *testing.T) {
	fixOutput := FixReviewOutput{
		Fixes: []FixItem{
			{File: "main.go", Line: 10, Action: "modified", What: "fixed"},
		},
		Summary: "Fixed 1 issue",
	}
	fixJSON, _ := json.Marshal(fixOutput)
	cliResult := fmt.Sprintf(`{"type":"result","subtype":"success","result":"%s"}`,
		jsonEscape(string(fixJSON)))

	issues := []review.ReviewIssue{{File: "main.go", Line: 10, Severity: "ERROR", Message: "bug"}}
	issuesJSON, _ := json.Marshal(issues)

	pool := &mockPool{output: cliResult}
	svc := NewService(pool, nil)

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixReview,
		RepoFullName: "owner/repo",
		PRNumber:     42,
		HeadRef:      "feature",
		BaseRef:      "main",
		RoundNumber:  1,
		ReviewIssues: string(issuesJSON),
	}

	result, err := svc.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output == nil {
		t.Fatal("expected non-nil output")
	}
	if len(result.Output.Fixes) != 1 {
		t.Errorf("fixes count = %d, want 1", len(result.Output.Fixes))
	}
}

func TestService_Execute_NoIssues(t *testing.T) {
	pool := &mockPool{}
	svc := NewService(pool, nil)

	payload := model.TaskPayload{
		TaskType:     model.TaskTypeFixReview,
		ReviewIssues: "",
	}

	_, err := svc.Execute(context.Background(), payload)
	if err == nil {
		t.Fatal("expected error for empty issues")
	}
}

func TestCountFixedIssues(t *testing.T) {
	output := &FixReviewOutput{
		Fixes: []FixItem{
			{Action: "modified"},
			{Action: "skipped"},
			{Action: "alternative_chosen"},
		},
	}
	if got := CountFixedIssues(output); got != 2 {
		t.Errorf("CountFixedIssues = %d, want 2", got)
	}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
