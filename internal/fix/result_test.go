package fix

import (
	"encoding/json"
	"testing"
)

func TestFixOutput_JSONUnmarshal(t *testing.T) {
	raw := `{
		"success": true,
		"info_sufficient": true,
		"branch_name": "auto-fix/issue-15",
		"commit_sha": "abc123",
		"modified_files": ["src/a.java", "src/b.java"],
		"test_results": {"passed": 10, "failed": 0, "skipped": 1, "all_passed": true},
		"analysis": "根因是 NPE",
		"fix_approach": "添加空值检查"
	}`
	var out FixOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal 失败: %v", err)
	}
	if !out.Success {
		t.Error("Success 应为 true")
	}
	if out.BranchName != "auto-fix/issue-15" {
		t.Errorf("BranchName = %q, 期望 auto-fix/issue-15", out.BranchName)
	}
	if out.TestResults == nil || !out.TestResults.AllPassed || out.TestResults.Passed != 10 {
		t.Errorf("TestResults 字段不正确: %+v", out.TestResults)
	}
	if len(out.ModifiedFiles) != 2 {
		t.Errorf("ModifiedFiles 长度 = %d, 期望 2", len(out.ModifiedFiles))
	}
}

func TestFixOutput_InfoInsufficient(t *testing.T) {
	raw := `{
		"success": false,
		"info_sufficient": false,
		"missing_info": ["缺少错误堆栈", "缺少复现步骤"],
		"analysis": "信息不足无法修复"
	}`
	var out FixOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal 失败: %v", err)
	}
	if out.Success || out.InfoSufficient {
		t.Error("Success 和 InfoSufficient 均应为 false")
	}
	if len(out.MissingInfo) != 2 {
		t.Errorf("MissingInfo 长度 = %d, 期望 2", len(out.MissingInfo))
	}
}
