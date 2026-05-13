package iterate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

type mockPool struct {
	output    string
	exitCode  int
	err       error
	stdinData []byte
}

func (m *mockPool) RunWithCommandAndStdin(_ context.Context, _ model.TaskPayload,
	_ []string, stdinData []byte) (*worker.ExecutionResult, error) {
	m.stdinData = stdinData
	if m.err != nil {
		return nil, m.err
	}
	return &worker.ExecutionResult{
		ExitCode: m.exitCode,
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

func TestService_Execute_UsesPayloadPromptConfig(t *testing.T) {
	fixOutput := FixReviewOutput{
		Fixes:   []FixItem{{Action: "modified"}},
		Summary: "ok",
	}
	fixJSON, _ := json.Marshal(fixOutput)
	cliResult := fmt.Sprintf(`{"type":"success","is_error":false,"result":"%s"}`,
		jsonEscape(string(fixJSON)))
	issues := []review.ReviewIssue{{File: "main.go", Line: 10, Severity: "ERROR", Message: "bug"}}
	issuesJSON, _ := json.Marshal(issues)

	pool := &mockPool{output: cliResult}
	svc := NewService(pool, nil)
	payload := model.TaskPayload{
		TaskType:           model.TaskTypeFixReview,
		RepoFullName:       "owner/repo",
		PRNumber:           42,
		HeadRef:            "feature",
		BaseRef:            "main",
		RoundNumber:        2,
		ReviewIssues:       string(issuesJSON),
		FixReportPath:      "custom/reports/42-round2.md",
		IterationMaxRounds: 5,
	}

	if _, err := svc.Execute(context.Background(), payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prompt := string(pool.stdinData)
	if !strings.Contains(prompt, "iteration round 2 of 5") {
		t.Fatalf("prompt 未使用 payload max_rounds: %s", prompt)
	}
	if !strings.Contains(prompt, "custom/reports/42-round2.md") {
		t.Fatalf("prompt 未使用 payload report path: %s", prompt)
	}
}

func TestService_Execute_NoFixedIssuesReturnsErrNoChanges(t *testing.T) {
	fixOutput := FixReviewOutput{
		Fixes:   []FixItem{{Action: "skipped"}},
		Summary: "nothing changed",
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
	if !errors.Is(err, ErrNoChanges) {
		t.Fatalf("error = %v, want ErrNoChanges", err)
	}
	if result == nil || result.Output == nil {
		t.Fatal("零修复场景仍应保留结构化输出供上层落库")
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

func TestService_Execute_CLIIsErrorFails(t *testing.T) {
	fixOutput := FixReviewOutput{Summary: "should not be accepted"}
	fixJSON, _ := json.Marshal(fixOutput)
	cliResult := fmt.Sprintf(`{"type":"result","is_error":true,"subtype":"error","result":"%s"}`,
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
	if !errors.Is(err, ErrFixReviewParseFailure) {
		t.Fatalf("error = %v, want ErrFixReviewParseFailure", err)
	}
	if result == nil || result.CLIMeta == nil || !result.CLIMeta.IsError {
		t.Fatalf("CLIMeta.IsError 应记录 CLI 错误，result=%+v", result)
	}
	if result.Output != nil {
		t.Fatal("CLI is_error=true 时不应产生结构化修复结果")
	}
}

func TestService_Execute_ExitCode2IsDeterministicFailure(t *testing.T) {
	issues := []review.ReviewIssue{{File: "main.go", Line: 10, Severity: "ERROR", Message: "bug"}}
	issuesJSON, _ := json.Marshal(issues)
	pool := &mockPool{exitCode: 2, output: "missing HEAD_REF"}
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
	if !errors.Is(err, ErrFixReviewDeterministicFailure) {
		t.Fatalf("error = %v, want ErrFixReviewDeterministicFailure", err)
	}
	if result == nil || result.ExitCode != 2 {
		t.Fatalf("ExitCode = %v, want 2", result)
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
