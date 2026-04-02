package notify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatFeishuCard 将 Message 格式化为飞书交互卡片 JSON。
// 返回的 JSON 包含顶层 msg_type + card 结构，可直接嵌入 Webhook 请求体。
func FormatFeishuCard(msg Message) ([]byte, error) {
	title, color := resolveHeaderStyle(msg)

	var mdParts []string
	mdParts = append(mdParts, fmt.Sprintf("**仓库**: %s/%s", msg.Target.Owner, msg.Target.Repo))
	if msg.Target.IsPR && msg.Target.Number > 0 {
		prTitle := msg.Metadata["pr_title"]
		if prTitle != "" {
			mdParts = append(mdParts, fmt.Sprintf("**PR**: #%d - %s", msg.Target.Number, prTitle))
		} else {
			mdParts = append(mdParts, fmt.Sprintf("**PR**: #%d", msg.Target.Number))
		}
	}

	if verdict := msg.Metadata["verdict"]; verdict != "" {
		mdParts = append(mdParts, fmt.Sprintf("**结论**: %s", strings.ToUpper(verdict)))
	}
	if issueSummary := msg.Metadata["issue_summary"]; issueSummary != "" {
		mdParts = append(mdParts, fmt.Sprintf("**发现问题**: %s", issueSummary))
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

	return json.Marshal(card)
}

// resolveHeaderStyle 根据消息事件类型和元数据推断卡片标题与主题色。
func resolveHeaderStyle(msg Message) (title, color string) {
	switch msg.EventType {
	case EventPRReviewStarted:
		return "PR 评审开始", "blue"
	case EventPRReviewDone:
		verdict := strings.ToLower(msg.Metadata["verdict"])
		switch verdict {
		case "request_changes":
			return "PR 评审完成", "orange"
		default:
			return "PR 评审完成", "green"
		}
	case EventSystemError:
		return "PR 评审失败", "red"
	default:
		return msg.Title, "blue"
	}
}

// resolveButtonURL 从元数据中提取按钮跳转链接。
func resolveButtonURL(msg Message) string {
	if msg.Metadata == nil {
		return ""
	}
	if u := msg.Metadata["pr_url"]; u != "" {
		return u
	}
	return msg.Metadata["issue_url"]
}

// resolveButtonStyle 根据事件类型返回按钮文案和样式。
func resolveButtonStyle(msg Message) (text, btnType string) {
	switch msg.EventType {
	case EventPRReviewStarted:
		return "查看 PR", "default"
	case EventPRReviewDone:
		return "查看评审详情", "primary"
	case EventSystemError:
		return "查看 PR", "danger"
	default:
		return "查看详情", "default"
	}
}
