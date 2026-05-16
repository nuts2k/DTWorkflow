package notify

import (
	"encoding/json"
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
	case EventE2EStarted, EventE2EDone, EventE2EFailed:
		mdParts = append(mdParts, renderE2EFields(msg)...)
	case EventE2ETriageStarted, EventE2ETriageDone, EventE2ETriageFailed:
		mdParts = append(mdParts, renderE2ETriageFields(msg)...)
	case EventCodeFromDocStarted, EventCodeFromDocDone, EventCodeFromDocFailed:
		mdParts = append(mdParts, renderCodeFromDocFields(msg)...)
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
	case EventE2EStarted:
		return "E2E 测试开始", "blue"
	case EventE2EDone:
		return "E2E 测试通过", "green"
	case EventE2EFailed:
		return e2eFailedHeader(msg)
	case EventE2ETriageStarted:
		return "E2E 回归分析开始", "blue"
	case EventE2ETriageDone:
		return "E2E 回归分析完成", "green"
	case EventE2ETriageFailed:
		return "E2E 回归分析失败", "red"
	case EventCodeFromDocStarted:
		return "文档驱动编码开始", "blue"
	case EventCodeFromDocDone:
		return "文档驱动编码完成", "green"
	case EventCodeFromDocFailed:
		return codeFromDocFailedHeader(msg)
	default:
		return msg.Title, "blue"
	}
}

// failure_category 枚举字面量与 internal/test.FailureCategory* 保持同步。
// notify 包不直接 import internal/test 避免跨包循环；若未来 test 侧新增/重命名枚举，
// 需同步更新本常量块与下方 switch。
const (
	genTestsCategoryInfrastructure   = "infrastructure"
	genTestsCategoryTestQuality      = "test_quality"
	genTestsCategoryInfoInsufficient = "info_insufficient"
)

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
	case genTestsCategoryInfrastructure:
		return "测试生成失败（基础设施故障）", "orange"
	case genTestsCategoryTestQuality:
		return "测试生成未达标", "blue"
	case genTestsCategoryInfoInsufficient:
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
	case genTestsCategoryInfrastructure:
		return "**提示**: 基础设施故障，建议重试或检查环境"
	case genTestsCategoryTestQuality:
		generated := msg.Metadata[MetaKeyGeneratedCount]
		if generated == "" {
			generated = "0"
		}
		return fmt.Sprintf("**提示**: 测试质量未达标，已生成的 %s 个测试可参考", generated)
	case genTestsCategoryInfoInsufficient:
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
	case EventE2EStarted:
		return "查看详情", "default"
	case EventE2EDone:
		return "查看详情", "primary"
	case EventE2EFailed:
		return "查看详情", "default"
	case EventE2ETriageStarted:
		return "查看详情", "default"
	case EventE2ETriageDone:
		return "查看详情", "primary"
	case EventE2ETriageFailed:
		return "查看详情", "default"
	case EventCodeFromDocStarted:
		return "查看详情", "default"
	case EventCodeFromDocDone:
		return "查看 PR", "primary"
	case EventCodeFromDocFailed:
		return "查看详情", "default"
	default:
		return "查看详情", "default"
	}
}

func isRetryingMessage(msg Message) bool {
	return msg.Metadata != nil && msg.Metadata[MetaKeyTaskStatus] == "retrying"
}

// e2eFailedHeader 根据通过/总数比例决定 E2E 失败卡片的标题与颜色。
// passed == "0"（全部失败）→ 红色；其余部分失败 → 橙色。
func e2eFailedHeader(msg Message) (title, color string) {
	passed := msg.Metadata[MetaKeyE2EPassedCases]
	if passed == "0" {
		return "E2E 测试失败", "red"
	}
	return "E2E 测试部分失败", "orange"
}

// renderE2EFields 组装 E2E 卡片特有的 markdown 字段。
func renderE2EFields(msg Message) []string {
	if msg.Metadata == nil {
		return nil
	}
	var parts []string
	if v := msg.Metadata[MetaKeyE2EEnv]; v != "" {
		parts = append(parts, fmt.Sprintf("**环境**: %s", v))
	}
	if msg.EventType == EventE2EStarted {
		if v := msg.Metadata[MetaKeyE2EModule]; v != "" {
			parts = append(parts, fmt.Sprintf("**范围**: %s", v))
		}
		return parts
	}

	// Done / Failed：用例统计
	total := msg.Metadata[MetaKeyE2ETotalCases]
	passed := msg.Metadata[MetaKeyE2EPassedCases]
	failed := msg.Metadata[MetaKeyE2EFailedCases]
	errCases := msg.Metadata[MetaKeyE2EErrorCases]
	skipped := msg.Metadata[MetaKeyE2ESkippedCases]

	if msg.EventType == EventE2EDone {
		parts = append(parts, fmt.Sprintf("**用例统计**: %s 个全部通过", total))
	} else {
		parts = append(parts, fmt.Sprintf("**用例统计**: 通过 %s / 失败 %s / 错误 %s / 跳过 %s",
			passed, failed, errCases, skipped))
	}

	// 失败用例列表
	if failedJSON := msg.Metadata[MetaKeyE2EFailedList]; failedJSON != "" {
		parts = append(parts, renderE2EFailedList(failedJSON)...)
	}

	// 已创建 Issue
	if issues := msg.Metadata[MetaKeyE2ECreatedIssues]; issues != "" {
		parts = append(parts, fmt.Sprintf("**已创建 Issue**: %s",
			formatIssueNumbers(issues, msg.Metadata)))
	}

	return parts
}

type e2eFailedItem struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Analysis string `json:"analysis"`
}

// renderE2EFailedList 解析失败用例 JSON 列表，最多渲染 5 条。
func renderE2EFailedList(jsonStr string) []string {
	var items []e2eFailedItem
	if err := json.Unmarshal([]byte(jsonStr), &items); err != nil {
		return nil
	}

	var parts []string
	parts = append(parts, "**失败用例**:")
	maxShow := 5
	for i, item := range items {
		if i >= maxShow {
			parts = append(parts, fmt.Sprintf("  ...及其他 %d 个失败用例", len(items)-maxShow))
			break
		}
		prefix := "  ✗"
		if item.Category == "environment" {
			prefix = "  ⚠"
		}
		analysis := item.Analysis
		analysisRunes := []rune(analysis)
		if len(analysisRunes) > 80 {
			analysis = string(analysisRunes[:80]) + "..."
		}
		parts = append(parts, fmt.Sprintf("%s %s — %s: %s", prefix, item.Name, item.Category, analysis))
	}
	return parts
}

// triageModuleItem 表示 triage_modules / triage_skipped_modules JSON 中的单个模块条目。
type triageModuleItem struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// renderE2ETriageFields 组装 E2E 回归分析卡片特有的 markdown 字段。
func renderE2ETriageFields(msg Message) []string {
	if msg.Metadata == nil {
		return nil
	}
	var parts []string

	switch msg.EventType {
	case EventE2ETriageDone:
		// 解析选中模块
		var modules []triageModuleItem
		if raw := msg.Metadata[MetaKeyTriageModules]; raw != "" {
			_ = json.Unmarshal([]byte(raw), &modules)
		}
		if len(modules) > 0 {
			parts = append(parts, "**选中模块**:")
			for _, m := range modules {
				line := fmt.Sprintf("  - %s", m.Name)
				if m.Reason != "" {
					line += fmt.Sprintf(" -- %s", m.Reason)
				}
				parts = append(parts, line)
			}
		} else {
			parts = append(parts, "**结果**: 本次变更无需 E2E 回归")
		}

		// 解析跳过模块
		var skipped []triageModuleItem
		if raw := msg.Metadata[MetaKeyTriageSkippedModules]; raw != "" {
			_ = json.Unmarshal([]byte(raw), &skipped)
		}
		if len(skipped) > 0 {
			parts = append(parts, "**跳过模块**:")
			for _, m := range skipped {
				line := fmt.Sprintf("  - %s", m.Name)
				if m.Reason != "" {
					line += fmt.Sprintf(" -- %s", m.Reason)
				}
				parts = append(parts, line)
			}
		}

		// 分析摘要（截断 200 字符）
		if analysis := msg.Metadata[MetaKeyTriageAnalysis]; analysis != "" {
			r := []rune(analysis)
			if len(r) > 200 {
				analysis = string(r[:200]) + "..."
			}
			parts = append(parts, fmt.Sprintf("**分析摘要**: %s", analysis))
		}

	case EventE2ETriageFailed:
		parts = append(parts, "**提示**: 回归分析失败，可手动触发 E2E 测试")
	}

	return parts
}

const (
	codeFromDocCategoryInfrastructure   = "infrastructure"
	codeFromDocCategoryTestFailure      = "test_failure"
	codeFromDocCategoryInfoInsufficient = "info_insufficient"
)

func codeFromDocFailedHeader(msg Message) (title, color string) {
	category := ""
	if msg.Metadata != nil {
		category = msg.Metadata[MetaKeyFailureCategory]
	}
	switch category {
	case codeFromDocCategoryInfrastructure:
		return "文档驱动编码失败（基础设施故障）", "orange"
	case codeFromDocCategoryTestFailure:
		return "文档驱动编码测试未通过", "orange"
	case codeFromDocCategoryInfoInsufficient:
		return "文档驱动编码信息不足", "blue"
	default:
		return "文档驱动编码失败", "red"
	}
}

func renderCodeFromDocFields(msg Message) []string {
	if msg.Metadata == nil {
		return nil
	}
	var parts []string
	if v := msg.Metadata[MetaKeyDocPath]; v != "" {
		parts = append(parts, fmt.Sprintf("**文档**: %s", v))
	}
	if v := msg.Metadata[MetaKeyBranchName]; v != "" {
		parts = append(parts, fmt.Sprintf("**分支**: %s", v))
	}
	if msg.EventType == EventCodeFromDocStarted {
		return parts
	}
	if v := msg.Metadata[MetaKeyFilesCreated]; v != "" {
		parts = append(parts, fmt.Sprintf("**新建文件**: %s", v))
	}
	if v := msg.Metadata[MetaKeyFilesModified]; v != "" {
		parts = append(parts, fmt.Sprintf("**修改文件**: %s", v))
	}
	if v := msg.Metadata[MetaKeyTestPassed]; v != "" {
		failed := msg.Metadata[MetaKeyTestFailed]
		if failed == "" {
			failed = "0"
		}
		parts = append(parts, fmt.Sprintf("**测试**: 通过 %s / 失败 %s", v, failed))
	}
	if msg.EventType == EventCodeFromDocFailed {
		if v := msg.Metadata[MetaKeyFailureCategory]; v != "" {
			parts = append(parts, fmt.Sprintf("**失败分类**: %s", v))
		}
	}
	return parts
}

// formatIssueNumbers 将逗号分隔的 Issue 编号字符串格式化为 "#42, #43" 形式。
func formatIssueNumbers(csv string, _ map[string]string) string {
	nums := strings.Split(csv, ",")
	var formatted []string
	for _, n := range nums {
		n = strings.TrimSpace(n)
		if n != "" {
			formatted = append(formatted, "#"+n)
		}
	}
	return strings.Join(formatted, ", ")
}
