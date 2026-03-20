package webhook

type giteaRepositoryPayload struct {
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
	Name string `json:"name"`
}

type giteaPullRequestPayload struct {
	Number  int64  `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
}

type giteaPullRequestEventPayload struct {
	Action      string                  `json:"action"`
	Repository  giteaRepositoryPayload  `json:"repository"`
	PullRequest giteaPullRequestPayload `json:"pull_request"`
	Sender      struct {
		Login    string `json:"login"`
		FullName string `json:"full_name"`
	} `json:"sender"`
}

type giteaIssuePayload struct {
	Number  int64  `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
}

type giteaLabelPayload struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type giteaIssueEventPayload struct {
	Action     string                 `json:"action"`
	Repository giteaRepositoryPayload `json:"repository"`
	Issue      giteaIssuePayload      `json:"issue"`
	Label      giteaLabelPayload      `json:"label"`
	Sender     struct {
		Login    string `json:"login"`
		FullName string `json:"full_name"`
	} `json:"sender"`
}
