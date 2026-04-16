package webhook

import "context"

type Event interface {
	Name() string
}

type RepositoryRef struct {
	Owner         string
	Name          string
	FullName      string
	CloneURL      string
	DefaultBranch string
}

type UserRef struct {
	Login    string
	FullName string
}

type LabelRef struct {
	Name  string
	Color string
}

type PullRequestRef struct {
	Number  int64
	Title   string
	Body    string
	HTMLURL string
	BaseRef string
	HeadRef string
	BaseSHA string
	HeadSHA string
}

type IssueRef struct {
	Number  int64
	Title   string
	Body    string
	HTMLURL string
	State   string
	Ref     string
}

type PullRequestEvent struct {
	DeliveryID  string
	EventType   string
	Action      string
	Repository  RepositoryRef
	PullRequest PullRequestRef
	Sender      UserRef
}

func (e PullRequestEvent) Name() string {
	return e.EventType + "." + e.Action
}

type IssueLabelEvent struct {
	DeliveryID     string
	EventType      string
	Action         string
	Repository     RepositoryRef
	Issue          IssueRef
	Label          LabelRef
	Sender         UserRef
	AutoFixChanged bool
	AutoFixAdded   bool
	AutoFixRemoved bool
	FixToPRChanged bool  // M3.4: fix-to-pr 标签变化
	FixToPRAdded   bool  // M3.4: fix-to-pr 标签添加
	FixToPRRemoved bool  // M3.4: fix-to-pr 标签移除
}

func (e IssueLabelEvent) Name() string {
	return e.EventType + "." + e.Action
}

type Handler interface {
	HandlePullRequest(ctx context.Context, event PullRequestEvent) error
	HandleIssueLabel(ctx context.Context, event IssueLabelEvent) error
}
