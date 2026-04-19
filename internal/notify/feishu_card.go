package notify

import (
	"fmt"
	"strings"
)

// FormatFeishuCard 将 Message 格式化为飞书交互卡片结构。
// 返回的 map 包含顶层 msg_type + card 结构，由调用方统一序列化为 JSON。
func FormatFeishuCard(msg Message) (map[string]any, error) {
	title, color := resolveHeaderStyle(msg)

	var mdParts []string
	mdParts = append(mdParts, fmt.Sprintf("**仓库**: %s/%s", msg.Target.Owner, msg.Target.Repo))
	if msg.Target.IsPR && msg.Target.Number > 0 {
		prTitle := msg.Metadata[MetaKeyPRTitle]
		if prTitle != "" {
			mdParts = append(mdParts, fmt.Sprintf("**PR**: #%d - %s", msg.Target.Number, prTitle))
		} else {
			mdParts = append(mdParts, fmt.Sprintf("**PR**: #%d", msg.Target.Number))
		}
	}

	if verdict := msg.Metadata[MetaKeyVerdict]; verdict != "" {
		mdParts = append(mdParts, fmt.Sprintf("**结论**: %s", strings.ToUpper(verdict)))
	}
	if issueSummary := msg.Metadata[MetaKeyIssueSummary]; issueSummary != "" {
		mdParts = append(mdParts, fmt.Sprintf("**发现问题**: %s", issueSummary))
	}
	if retryCount := msg.Metadata[MetaKeyRetryCount]; retryCount != "" {
		maxRetry := msg.Metadata[MetaKeyMaxRetry]
		if maxRetry != "" {
			mdParts = append(mdParts, fmt.Sprintf("**重试**: 第 %s 次 / 共 %s 次", retryCount, maxRetry))
		} else {
			mdParts = append(mdParts, fmt.Sprintf("**重试**: 第 %s 次", retryCount))
		}
	}
	if notifyTime := msg.Metadata[MetaKeyNotifyTime]; notifyTime != "" {
		mdParts = append(mdParts, fmt.Sprintf("**通知时间**: %s", notifyTime))
	}
	if duration := msg.Metadata[MetaKeyDuration]; duration != "" {
		mdParts = append(mdParts, fmt.Sprintf("**耗时**: %s", duration))
	}

	// M4.2：gen_tests 事件专用字段。仅对三个 gen_tests 事件渲染，避免污染其它事件卡片。
	switch msg.EventType {
	case EventGenTestsStarted, EventGenTestsDone, EventGenTestsFailed:
		mdParts = append(mdParts, renderGenTestsFields(msg)...)
	}

	if msg.Body != "" {
		mdParts = append(mdParts, msg.Body)
	}

	// M4.2：Failed 事件根据 failure_category 追加一行提示文案。
	if hint := genTestsFailureHint(msg); hint != "" {
		mdParts = append(mdParts, hint)
	}

	mdContent := strings.Join(mdParts, "\n")

	elements := []any{
		map[string]any{
			"tag":     "markdown",
			"content": mdContent,
		},
	}

	if btnURL := resolveButtonURL(msg); btnURL != "" {
		btnText, btnType := resolveButtonStyle(msg)
		elements = append(elements, map[string]any{
			"tag": "action",
			"actions": []any{
				map[string]any{
					"tag":  "button",
					"text": map[string]string{"tag": "plain_text", "content": btnText},
					"type": btnType,
					"url":  btnURL,
				},
			},
		})
	}

	card := map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"config": map[string]bool{"wide_screen_mode": true},
			"header": map[string]any{
				"title":    map[string]string{"tag": "plain_text", "content": title},
				"template": color,
			},
			"elements": elements,
		},
	}

	return card, nil
}

// resolveHeaderStyle 根据消息事件类型和元数据推断卡片标题与主题色。
func resolveHeaderStyle(msg Message) (title, color string) {
	if isRetryingMessage(msg) {
		return msg.Title, "orange"
	}

	switch msg.EventType {
	case EventPRReviewStarted:
		return "PR 评审开始", "blue"
	case EventPRReviewDone:
		verdict := strings.ToLower(msg.Metadata[MetaKeyVerdict])
		switch verdict {
		case "request_changes":
			return "PR 评审完成", "orange"
		default:
			return "PR 评审完成", "green"
		}
	case EventIssueAnalyzeStarted, EventIssueFixStarted:
		return msg.Title, "blue"
	case EventIssueAnalyzeDone:
		return "Issue 分析完成", "blue"
	case EventFixIssueDone:
		return "Issue 修复 PR 已创建", "green"
	case EventSystemError:
		if msg.Title != "" {
			return msg.Title, "red"
		}
		return "任务失败", "red"
	case EventGenTestsStarted:
		return "测试生成开始", "blue"
	case EventGenTestsDone:
		return "测试生成完成", "green"
	case EventGenTestsFailed:
		return genTestsFailedHeader(msg)
	default:
		return msg.Title, "blue"
	}
}

// genTestsFailedHeader 根据 failure_category 返回 Failed 事件的标题与卡片颜色。
// 映射规则（与 §4.9.2 一致）：
//   - infrastructure    → Warning（orange）— 基础设施故障
//   - test_quality      → Info（blue）   — 质量未达标
//   - info_insufficient → Info（blue）   — 信息不足
//   - 其他/空           → orange 兜底
func genTestsFailedHeader(msg Message) (title, color string) {
	category := ""
	if msg.Metadata != nil {
		category = msg.Metadata[MetaKeyFailureCategory]
	}
	switch category {
	case "infrastructure":
		return "测试生成失败（基础设施故障）", "orange"
	case "test_quality":
		return "测试生成未达标", "blue"
	case "info_insufficient":
		return "测试生成信息不足", "blue"
	default:
		return "测试生成失败", "orange"
	}
}

// renderGenTestsFields 组装 gen_tests 卡片特有的 markdown 字段。
// 顺序固定：module / framework / generated_count / committed_count /
// skipped_count / failure_category（失败事件才渲染）。
func renderGenTestsFields(msg Message) []string {
	if msg.Metadata == nil {
		return nil
	}
	var parts []string
	if v := msg.Metadata[MetaKeyModule]; v != "" {
		parts = append(parts, fmt.Sprintf("**模块**: %s", v))
	}
	if v := msg.Metadata[MetaKeyFramework]; v != "" {
		parts = append(parts, fmt.Sprintf("**框架**: %s", v))
	}
	if v := msg.Metadata[MetaKeyGeneratedCount]; v != "" {
		parts = append(parts, fmt.Sprintf("**生成文件数**: %s", v))
	}
	if v := msg.Metadata[MetaKeyCommittedCount]; v != "" {
		parts = append(parts, fmt.Sprintf("**提交文件数**: %s", v))
	}
	if v := msg.Metadata[MetaKeySkippedCount]; v != "" {
		parts = append(parts, fmt.Sprintf("**跳过数**: %s", v))
	}
	if msg.EventType == EventGenTestsFailed {
		if v := msg.Metadata[MetaKeyFailureCategory]; v != "" {
			parts = append(parts, fmt.Sprintf("**失败分类**: %s", v))
		}
	}
	return parts
}

// genTestsFailureHint 返回 Failed 事件的提示文案。
// 仅在 EventGenTestsFailed 且 failure_category 有值时返回，用于让运维快速定位方向。
func genTestsFailureHint(msg Message) string {
	if msg.EventType != EventGenTestsFailed || msg.Metadata == nil {
		return ""
	}
	switch msg.Metadata[MetaKeyFailureCategory] {
	case "infrastructure":
		return "**提示**: 基础设施故障，建议重试或检查环境"
	case "test_quality":
		generated := msg.Metadata[MetaKeyGeneratedCount]
		if generated == "" {
			generated = "0"
		}
		return fmt.Sprintf("**提示**: 测试质量未达标，已生成的 %s 个测试可参考", generated)
	case "info_insufficient":
		return "**提示**: 信息不足，请补充相关上下文后重试"
	default:
		return ""
	}
}

// resolveButtonURL 从元数据中提取按钮跳转链接。
func resolveButtonURL(msg Message) string {
	if msg.Metadata == nil {
		return ""
	}
	if u := msg.Metadata[MetaKeyPRURL]; u != "" {
		return u
	}
	return msg.Metadata[MetaKeyIssueURL]
}

// resolveButtonStyle 根据事件类型返回按钮文案和样式。
func resolveButtonStyle(msg Message) (text, btnType string) {
	if isRetryingMessage(msg) {
		return "查看详情", "default"
	}

	switch msg.EventType {
	case EventPRReviewStarted:
		return "查看 PR", "default"
	case EventPRReviewDone:
		return "查看评审详情", "primary"
	case EventFixIssueDone:
		return "查看修复 PR", "primary"
	case EventIssueAnalyzeDone, EventIssueAnalyzeStarted, EventIssueFixStarted:
		return "查看 Issue", "default"
	case EventSystemError:
		return "查看详情", "danger"
	case EventGenTestsStarted:
		return "查看 PR", "default"
	case EventGenTestsDone:
		return "查看测试 PR", "primary"
	case EventGenTestsFailed:
		return "查看详情", "default"
	default:
		return "查看详情", "default"
	}
}

func isRetryingMessage(msg Message) bool {
	return msg.Metadata != nil && msg.Metadata[MetaKeyTaskStatus] == "retrying"
}
