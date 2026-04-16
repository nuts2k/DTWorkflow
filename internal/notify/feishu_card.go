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

	if msg.Body != "" {
		mdParts = append(mdParts, msg.Body)
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
	default:
		return msg.Title, "blue"
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
	default:
		return "查看详情", "default"
	}
}

func isRetryingMessage(msg Message) bool {
	return msg.Metadata != nil && msg.Metadata[MetaKeyTaskStatus] == "retrying"
}
