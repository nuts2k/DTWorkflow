package notify

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildE2ECard_AllPassed(t *testing.T) {
	msg := Message{
		EventType: EventE2EDone,
		Target:    Target{Owner: "owner", Repo: "repo"},
		Metadata: map[string]string{
			MetaKeyE2EEnv:         "staging",
			MetaKeyE2ETotalCases:  "5",
			MetaKeyE2EPassedCases: "5",
			MetaKeyE2EFailedCases: "0",
			MetaKeyE2EErrorCases:  "0",
			MetaKeyDuration:       "2m30s",
		},
	}
	card, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard: %v", err)
	}
	raw, _ := json.Marshal(card)
	s := string(raw)
	if !strings.Contains(s, "E2E 测试通过") {
		t.Error("期望包含'E2E 测试通过'标题")
	}
	if !strings.Contains(s, "green") {
		t.Error("全通过应为绿色")
	}
}

func TestBuildE2ECard_PartialFailure(t *testing.T) {
	failedList := `[{"name":"order/create-order","category":"bug","analysis":"取消按钮失效"}]`
	msg := Message{
		EventType: EventE2EFailed,
		Severity:  SeverityWarning,
		Target:    Target{Owner: "owner", Repo: "repo"},
		Metadata: map[string]string{
			MetaKeyE2EEnv:           "staging",
			MetaKeyE2ETotalCases:    "5",
			MetaKeyE2EPassedCases:   "3",
			MetaKeyE2EFailedCases:   "2",
			MetaKeyE2EErrorCases:    "0",
			MetaKeyE2EFailedList:    failedList,
			MetaKeyE2ECreatedIssues: "42,43",
			MetaKeyDuration:         "3m",
		},
	}
	card, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard: %v", err)
	}
	raw, _ := json.Marshal(card)
	s := string(raw)
	if !strings.Contains(s, "orange") && !strings.Contains(s, "red") {
		t.Error("失败应为橙色或红色")
	}
	if !strings.Contains(s, "order/create-order") {
		t.Error("期望包含失败用例名")
	}
	if !strings.Contains(s, "#42") || !strings.Contains(s, "#43") {
		t.Error("期望包含已创建 Issue 号")
	}
}

func TestBuildE2ECard_Started(t *testing.T) {
	msg := Message{
		EventType: EventE2EStarted,
		Target:    Target{Owner: "owner", Repo: "repo"},
		Metadata: map[string]string{
			MetaKeyE2EEnv: "staging",
		},
	}
	card, err := FormatFeishuCard(msg)
	if err != nil {
		t.Fatalf("FormatFeishuCard: %v", err)
	}
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), "blue") {
		t.Error("开始应为蓝色")
	}
}
