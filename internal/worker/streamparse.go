package worker

import (
	"encoding/json"
	"fmt"
	"strings"
)

// tryExtractResultCLIJSON 尝试从 stream-json 行中提取 result 事件并转换为 CLI JSON 信封格式。
// 合并了 isResultEvent + resultEventToCLIJSON，避免对同一行做两次 JSON 反序列化。
// 使用 strings.Contains 做快速前置过滤，大部分非 result 行无需 JSON 解析。
func tryExtractResultCLIJSON(line string) (string, bool) {
	if len(line) == 0 || !strings.Contains(line, `"type":"result"`) {
		return "", false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return "", false
	}
	if m["type"] != "result" {
		return "", false
	}
	// CLI JSON 信封中 type 字段使用 subtype 的值（如 "success"），去掉 subtype
	if subtype, ok := m["subtype"]; ok {
		m["type"] = subtype
		delete(m, "subtype")
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "", false
	}
	return string(data), true
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
