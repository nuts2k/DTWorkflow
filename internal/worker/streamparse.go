package worker

import (
	"encoding/json"
	"fmt"
	"strings"
)

// streamEvent 流式事件的类型标识（仅用于快速筛选）
type streamEvent struct {
	Type string `json:"type"`
}

// resultEvent stream-json 的 result 事件完整结构
type resultEvent struct {
	Type       string  `json:"type"`
	Subtype    string  `json:"subtype"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMs int64   `json:"duration_ms"`
	IsError    bool    `json:"is_error"`
	NumTurns   int     `json:"num_turns"`
	Result     string  `json:"result"`
	SessionID  string  `json:"session_id"`
}

// isResultEvent 快速判断一行是否为 result 事件（仅解析 type 字段）
func isResultEvent(line string) bool {
	if len(line) == 0 {
		return false
	}
	var e streamEvent
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return false
	}
	return e.Type == "result"
}

// parseResultEvent 完整解析 result 事件
func parseResultEvent(line string) (*resultEvent, error) {
	var e resultEvent
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return nil, fmt.Errorf("解析 result 事件失败: %w", err)
	}
	if e.Type != "result" {
		return nil, fmt.Errorf("非 result 事件: type=%s", e.Type)
	}
	return &e, nil
}

// resultEventToCLIJSON 将 stream-json result 事件的原始 JSON 转换为与 --output-format json 兼容的 JSON 字符串。
// 上层 review.Service.parseResult() 期望的是 CLI JSON 信封格式，此函数做格式对齐。
// 使用 map[string]any 保留未知字段，避免 Claude CLI 新增字段时被静默丢弃。
func resultEventToCLIJSON(rawJSON string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &m); err != nil {
		return "", fmt.Errorf("解析 result 事件失败: %w", err)
	}
	// CLI JSON 信封中 type 字段使用 subtype 的值（如 "success"），去掉 subtype
	if subtype, ok := m["subtype"]; ok {
		m["type"] = subtype
		delete(m, "subtype")
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("序列化 CLI JSON 信封失败: %w", err)
	}
	return string(data), nil
}

// injectStreamJsonFlags 将命令中的 --output-format 替换为 stream-json 模式。
// 无 --output-format 参数时直接追加。
func injectStreamJsonFlags(cmd []string) []string {
	result := make([]string, 0, len(cmd)+4)
	skip := false
	for _, arg := range cmd {
		if skip {
			skip = false
			continue
		}
		if arg == "--output-format" {
			skip = true
			continue
		}
		if strings.HasPrefix(arg, "--output-format=") {
			continue
		}
		result = append(result, arg)
	}
	result = append(result, "--output-format", "stream-json", "--verbose", "--include-partial-messages")
	return result
}
