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
	if payload.Action != "opened" && payload.Action != "synchronized" {
		return nil, ErrUnsupportedAction
	}
	if payload.PullRequest.Number == 0 || payload.Repository.FullName == "" {
		return nil, ErrInvalidPayload
	}
	return PullRequestEvent{
		DeliveryID: deliveryID,
		EventType:  "pull_request",
		Action:     payload.Action,
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
	if payload.Action != "labeled" && payload.Action != "unlabeled" {
		return nil, ErrUnsupportedAction
	}
	if payload.Issue.Number == 0 || payload.Repository.FullName == "" || payload.Label.Name == "" {
		return nil, ErrInvalidPayload
	}
	isAutoFix := isAutoFixLabel(payload.Label.Name)
	return IssueLabelEvent{
		DeliveryID: deliveryID,
		EventType:  "issues",
		Action:     payload.Action,
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
		},
		Label: LabelRef{Name: payload.Label.Name, Color: payload.Label.Color},
		Sender: UserRef{
			Login:    payload.Sender.Login,
			FullName: payload.Sender.FullName,
		},
		AutoFixChanged: isAutoFix,
		AutoFixAdded:   isAutoFix && payload.Action == "labeled",
		AutoFixRemoved: isAutoFix && payload.Action == "unlabeled",
	}, nil
}

func isAutoFixLabel(name string) bool {
	return strings.EqualFold(name, "auto-fix")
}
