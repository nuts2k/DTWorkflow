package webhook

import (
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrUnsupportedEvent  = errors.New("unsupported event")
	ErrUnsupportedAction = errors.New("unsupported action")
	ErrInvalidPayload    = errors.New("invalid payload")
)

type Parser struct{}

func NewParser() *Parser { return &Parser{} }

func (p *Parser) Parse(eventType, deliveryID string, body []byte) (Event, error) {
	switch eventType {
	case "pull_request":
		return p.parsePullRequest(deliveryID, body)
	case "issues":
		return p.parseIssue(deliveryID, body)
	default:
		return nil, ErrUnsupportedEvent
	}
}

func (p *Parser) parsePullRequest(deliveryID string, body []byte) (Event, error) {
	var payload giteaPullRequestEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrInvalidPayload
	}
	if payload.Action != "opened" && payload.Action != "synchronized" &&
		payload.Action != "reopened" && payload.Action != "closed" {
		return nil, ErrUnsupportedAction
	}

	action := payload.Action
	if payload.Action == "closed" && payload.PullRequest.Merged {
		action = "merged"
	} else if payload.Action == "closed" {
		return nil, ErrUnsupportedAction
	}

	if payload.PullRequest.Number == 0 || payload.Repository.FullName == "" {
		return nil, ErrInvalidPayload
	}
	return PullRequestEvent{
		DeliveryID: deliveryID,
		EventType:  "pull_request",
		Action:     action,
		Repository: RepositoryRef{
			Owner:         payload.Repository.Owner.Login,
			Name:          payload.Repository.Name,
			FullName:      payload.Repository.FullName,
			CloneURL:      payload.Repository.CloneURL,
			DefaultBranch: payload.Repository.DefaultBranch,
		},
		PullRequest: PullRequestRef{
			Number:  payload.PullRequest.Number,
			Title:   payload.PullRequest.Title,
			Body:    payload.PullRequest.Body,
			HTMLURL: payload.PullRequest.HTMLURL,
			Merged:  payload.PullRequest.Merged,
			BaseRef: payload.PullRequest.Base.Ref,
			HeadRef: payload.PullRequest.Head.Ref,
			BaseSHA: payload.PullRequest.Base.SHA,
			HeadSHA: payload.PullRequest.Head.SHA,
		},
		Sender: UserRef{
			Login:    payload.Sender.Login,
			FullName: payload.Sender.FullName,
		},
	}, nil
}

func (p *Parser) parseIssue(deliveryID string, body []byte) (Event, error) {
	var payload giteaIssueEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrInvalidPayload
	}

	// 标准化 action：Gitea 1.21+ 使用 label_updated/label_cleared，
	// 早期版本和 GitHub 使用 labeled/unlabeled。
	action := normalizeIssueLabelAction(payload.Action)
	if action == "" {
		return nil, ErrUnsupportedAction
	}

	// 提取标签名：优先使用顶层 label 字段（早期版本），
	// 回退到 issue.labels 中查找 auto-fix（Gitea 1.21+ label_updated）。
	labelRef, _ := extractLabel(payload)
	changedLabel, changedLabelKnown := detectChangedLabel(payload)
	if changedLabelKnown {
		labelRef = changedLabel
	}
	isAutoFix := changedLabelKnown && isAutoFixLabel(changedLabel.Name)
	isFixToPR := changedLabelKnown && isFixToPRLabel(changedLabel.Name)

	if payload.Issue.Number == 0 || payload.Repository.FullName == "" {
		return nil, ErrInvalidPayload
	}
	// 早期版本要求 label.name 非空；Gitea 1.21+ label_cleared 可以没有 label
	// fix-to-pr 独立检测，不依赖 labelRef
	if action == "labeled" && labelRef.Name == "" && !isFixToPR {
		return nil, ErrInvalidPayload
	}

	return IssueLabelEvent{
		DeliveryID: deliveryID,
		EventType:  "issues",
		Action:     action,
		Repository: RepositoryRef{
			Owner:         payload.Repository.Owner.Login,
			Name:          payload.Repository.Name,
			FullName:      payload.Repository.FullName,
			CloneURL:      payload.Repository.CloneURL,
			DefaultBranch: payload.Repository.DefaultBranch,
		},
		Issue: IssueRef{
			Number:  payload.Issue.Number,
			Title:   payload.Issue.Title,
			Body:    payload.Issue.Body,
			HTMLURL: payload.Issue.HTMLURL,
			State:   payload.Issue.State,
			Ref:     payload.Issue.Ref,
		},
		Label: labelRef,
		Sender: UserRef{
			Login:    payload.Sender.Login,
			FullName: payload.Sender.FullName,
		},
		AutoFixChanged: isAutoFix,
		AutoFixAdded:   isAutoFix && action == "labeled",
		AutoFixRemoved: isAutoFix && action == "unlabeled",
		FixToPRChanged: isFixToPR,
		FixToPRAdded:   isFixToPR && action == "labeled",
		FixToPRRemoved: isFixToPR && action == "unlabeled",
	}, nil
}

// normalizeIssueLabelAction 将 Gitea 1.21+ 的 action 映射到标准名称。
// 返回空字符串表示不支持的 action。
func normalizeIssueLabelAction(action string) string {
	switch action {
	case "labeled", "label_updated":
		return "labeled"
	case "unlabeled", "label_cleared":
		return "unlabeled"
	default:
		return ""
	}
}

// extractLabel 从 payload 中提取标签信息。
// 优先使用顶层 label 字段（早期版本），回退到 issue.labels（Gitea 1.21+）。
func extractLabel(payload giteaIssueEventPayload) (LabelRef, bool) {
	// 早期版本：顶层 label 字段
	if payload.Label.Name != "" {
		isAutoFix := isAutoFixLabel(payload.Label.Name)
		return LabelRef{Name: payload.Label.Name, Color: payload.Label.Color}, isAutoFix
	}
	// Gitea 1.21+: label_updated 时标签在 issue.labels 中
	for _, l := range payload.Issue.Labels {
		if isAutoFixLabel(l.Name) {
			return LabelRef{Name: l.Name, Color: l.Color}, true
		}
	}
	// label_cleared 或无 auto-fix 标签
	if len(payload.Issue.Labels) > 0 {
		first := payload.Issue.Labels[0]
		return LabelRef{Name: first.Name, Color: first.Color}, false
	}
	return LabelRef{}, false
}

func isAutoFixLabel(name string) bool {
	return strings.EqualFold(name, "auto-fix")
}

func isFixToPRLabel(name string) bool {
	return strings.EqualFold(name, "fix-to-pr")
}

// detectChangedLabel 识别本次 webhook 变更的标签。
// 优先使用顶层 label 字段；Gitea 1.21+ 的 label_updated 若缺少顶层 label，
// 仅在 issue.labels 恰好只有 1 个标签时才将其视为本次变更标签。
// 当当前 payload 无法可靠判断本次变更的是哪个标签时，返回 false，调用方需保守处理。
func detectChangedLabel(payload giteaIssueEventPayload) (LabelRef, bool) {
	if payload.Label.Name != "" {
		return LabelRef{Name: payload.Label.Name, Color: payload.Label.Color}, true
	}
	if normalizeIssueLabelAction(payload.Action) == "labeled" && len(payload.Issue.Labels) == 1 {
		l := payload.Issue.Labels[0]
		return LabelRef{Name: l.Name, Color: l.Color}, true
	}
	return LabelRef{}, false
}
