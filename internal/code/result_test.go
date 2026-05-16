package code

import (
	"errors"
	"testing"
)

func TestParseCodeFromDocOutput_Success(t *testing.T) {
	raw := `{"success":true,"info_sufficient":true,"branch_name":"auto-code/test","commit_sha":"abc123","modified_files":[{"path":"main.go","action":"created","description":"impl"}],"test_results":{"passed":5,"failed":0,"skipped":0,"all_passed":true},"analysis":"done","implementation":"done","failure_category":"none"}`
	output, err := ParseCodeFromDocOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !output.Success {
		t.Error("expected success=true")
	}
	if output.BranchName != "auto-code/test" {
		t.Errorf("expected branch_name=auto-code/test, got %s", output.BranchName)
	}
	if len(output.ModifiedFiles) != 1 {
		t.Errorf("expected 1 modified file, got %d", len(output.ModifiedFiles))
	}
}

func TestParseCodeFromDocOutput_Empty(t *testing.T) {
	_, err := ParseCodeFromDocOutput("")
	if !errors.Is(err, ErrCodeFromDocParseFailure) {
		t.Errorf("expected ErrCodeFromDocParseFailure, got %v", err)
	}
}

func TestParseCodeFromDocOutput_SuccessButMissingBranch(t *testing.T) {
	raw := `{"success":true,"info_sufficient":true,"branch_name":"","commit_sha":"abc","modified_files":[],"test_results":{},"analysis":"","implementation":"","failure_category":"none"}`
	_, err := ParseCodeFromDocOutput(raw)
	if !errors.Is(err, ErrCodeFromDocParseFailure) {
		t.Errorf("expected ErrCodeFromDocParseFailure for missing branch, got %v", err)
	}
}

func TestParseCodeFromDocOutput_SuccessButTestsFailed(t *testing.T) {
	raw := `{"success":true,"info_sufficient":true,"branch_name":"auto-code/test","commit_sha":"abc123","modified_files":[],"test_results":{"passed":3,"failed":1,"skipped":0,"all_passed":false},"analysis":"","implementation":"","failure_category":"none"}`
	_, err := ParseCodeFromDocOutput(raw)
	if !errors.Is(err, ErrCodeFromDocParseFailure) {
		t.Fatalf("expected ErrCodeFromDocParseFailure for inconsistent test result, got %v", err)
	}
}

func TestParseCodeFromDocOutput_InfoInsufficient(t *testing.T) {
	raw := `{"success":false,"info_sufficient":false,"missing_info":["缺少数据库 schema"],"branch_name":"","commit_sha":"","modified_files":[],"test_results":{},"analysis":"","implementation":"","failure_category":"info_insufficient","failure_reason":"设计文档未包含必要信息"}`
	output, err := ParseCodeFromDocOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Success {
		t.Error("expected success=false")
	}
	if output.FailureCategory != FailureCategoryInfoInsufficient {
		t.Errorf("expected info_insufficient, got %s", output.FailureCategory)
	}
}

func TestParseCodeFromDocOutput_TestFailureRequiresPRFields(t *testing.T) {
	raw := `{"success":false,"info_sufficient":true,"branch_name":"","commit_sha":"","modified_files":[],"test_results":{"passed":1,"failed":1,"skipped":0,"all_passed":false},"analysis":"","implementation":"","failure_category":"test_failure","failure_reason":"测试失败"}`
	_, err := ParseCodeFromDocOutput(raw)
	if !errors.Is(err, ErrCodeFromDocParseFailure) {
		t.Fatalf("expected ErrCodeFromDocParseFailure, got %v", err)
	}
}

func TestParseCodeFromDocOutput_TestFailureWithPRFields(t *testing.T) {
	raw := `{"success":false,"info_sufficient":true,"branch_name":"auto-code/spec","commit_sha":"abc123","modified_files":[{"path":"main.go","action":"modified","description":"impl"}],"test_results":{"passed":1,"failed":1,"skipped":0,"all_passed":false},"analysis":"","implementation":"已实现但测试失败","failure_category":"test_failure","failure_reason":"测试失败"}`
	output, err := ParseCodeFromDocOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.FailureCategory != FailureCategoryTestFailure {
		t.Fatalf("FailureCategory = %q, want %q", output.FailureCategory, FailureCategoryTestFailure)
	}
}

func TestParseCodeFromDocOutput_DoubleJSON(t *testing.T) {
	raw := `{"result":"{\"success\":true,\"info_sufficient\":true,\"branch_name\":\"auto-code/x\",\"commit_sha\":\"def456\",\"modified_files\":[],\"test_results\":{\"passed\":1,\"failed\":0,\"skipped\":0,\"all_passed\":true},\"analysis\":\"\",\"implementation\":\"\",\"failure_category\":\"none\"}"}`
	output, err := ParseCodeFromDocOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.BranchName != "auto-code/x" {
		t.Errorf("expected branch auto-code/x, got %s", output.BranchName)
	}
}

func TestParseCodeFromDocOutput_DoubleJSONWithFence(t *testing.T) {
	raw := "{\"result\":\"```json\\n{\\\"success\\\":true,\\\"info_sufficient\\\":true,\\\"branch_name\\\":\\\"auto-code/x\\\",\\\"commit_sha\\\":\\\"def456\\\",\\\"modified_files\\\":[],\\\"test_results\\\":{\\\"passed\\\":1,\\\"failed\\\":0,\\\"skipped\\\":0,\\\"all_passed\\\":true},\\\"analysis\\\":\\\"\\\",\\\"implementation\\\":\\\"\\\",\\\"failure_category\\\":\\\"none\\\"}\\n```\"}"
	output, err := ParseCodeFromDocOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.BranchName != "auto-code/x" {
		t.Errorf("expected branch auto-code/x, got %s", output.BranchName)
	}
}

func TestParseCodeFromDocOutput_FalseWithoutFailureCategory(t *testing.T) {
	raw := `{"success":false,"info_sufficient":true,"branch_name":"","commit_sha":"","modified_files":[],"test_results":{"passed":0,"failed":0,"skipped":0,"all_passed":false},"analysis":"","implementation":"","failure_category":""}`
	_, err := ParseCodeFromDocOutput(raw)
	if !errors.Is(err, ErrCodeFromDocParseFailure) {
		t.Fatalf("expected ErrCodeFromDocParseFailure, got %v", err)
	}
}

func TestSanitizeCodeFromDocOutput(t *testing.T) {
	o := &CodeFromDocOutput{
		Analysis:       string(make([]rune, 600)),
		Implementation: "normal text",
		FailureReason:  "has \x00 null",
		MissingInfo:    []string{"item with \x00"},
		ModifiedFiles:  []ModifiedFile{{Description: "ok"}},
	}
	SanitizeCodeFromDocOutput(o)
	if len([]rune(o.Analysis)) > 503 { // 500 + "..."
		t.Error("analysis not truncated")
	}
	if o.FailureReason != "has  null" {
		t.Errorf("null not removed: %q", o.FailureReason)
	}
}
