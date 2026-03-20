package gitea

import "time"

// --- 基础实体 ---

// User 表示 Gitea 用户
type User struct {
	ID       int64  `json:"id"`
	Login    string `json:"login"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
}

// Repository 表示 Gitea 仓库
type Repository struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Owner         *User  `json:"owner"`
	DefaultBranch string `json:"default_branch"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	HTMLURL       string `json:"html_url"`
}

// Branch 表示 Gitea 分支
type Branch struct {
	Name   string  `json:"name"`
	Commit *Commit `json:"commit"`
}

// Commit 表示提交信息
type Commit struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Label 表示 Issue/PR 标签
type Label struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// --- PR 相关 ---

// PullRequest 表示 Gitea PR
type PullRequest struct {
	ID        int64      `json:"id"`
	Number    int64      `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	State     string     `json:"state"`
	HTMLURL   string     `json:"html_url"`
	Base      *PRBranch  `json:"base"`
	Head      *PRBranch  `json:"head"`
	User      *User      `json:"user"`
	Mergeable bool       `json:"mergeable"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// PRBranch 表示 PR 的源/目标分支信息
type PRBranch struct {
	Ref  string      `json:"ref"`
	SHA  string      `json:"sha"`
	Repo *Repository `json:"repo"`
}

// ChangedFile 表示 PR 中变更的文件
type ChangedFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"` // added, modified, deleted, renamed
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
}

// PullReview 表示 PR 评审
type PullReview struct {
	ID        int64           `json:"id"`
	Body      string          `json:"body"`
	State     ReviewStateType `json:"state"`
	HTMLURL   string          `json:"html_url"`
	CommitID  string          `json:"commit_id"`
	User      *User           `json:"user"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ReviewStateType 评审状态类型
type ReviewStateType string

const (
	ReviewStateApproved       ReviewStateType = "APPROVED"
	ReviewStateRequestChanges ReviewStateType = "REQUEST_CHANGES"
	ReviewStateComment        ReviewStateType = "COMMENT"
)

// --- Issue 相关 ---

// Issue 表示 Gitea Issue
type Issue struct {
	ID        int64     `json:"id"`
	Number    int64     `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	User      *User     `json:"user"`
	Labels    []*Label  `json:"labels"`
	Comments  int       `json:"comments"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Comment 表示 Issue/PR 评论
type Comment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	User      *User     `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// --- 仓库文件 ---

// ContentsResponse 表示仓库文件元数据和内容
type ContentsResponse struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Type     string `json:"type"`     // file, dir, symlink
	Size     int64  `json:"size"`
	Encoding string `json:"encoding"` // base64
	Content  string `json:"content"`  // base64 编码的文件内容
}

// --- 通用选项 ---

// ListOptions 分页选项
type ListOptions struct {
	Page     int // 页码，从 1 开始
	PageSize int // 每页条数，Gitea 默认 50
}

// --- PR 选项 ---

// ListPullRequestsOptions 列出 PR 的选项
type ListPullRequestsOptions struct {
	ListOptions
	State string // open, closed, all
	Sort  string // oldest, recentupdate, leastupdate, mostcomment, leastcomment, priority
}

// CreatePullRequestOption 创建 PR 的选项
type CreatePullRequestOption struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"` // 源分支
	Base  string `json:"base"` // 目标分支
}

// CreatePullReviewOptions 创建 PR 评审的选项
type CreatePullReviewOptions struct {
	State    ReviewStateType `json:"event"`
	Body     string          `json:"body"`
	CommitID string          `json:"commit_id,omitempty"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// ReviewComment 评审行级评论
type ReviewComment struct {
	Path       string `json:"path"`
	Body       string `json:"body"`
	NewLineNum int64  `json:"new_position"`
	OldLineNum int64  `json:"old_position"`
}

// --- Issue 选项 ---

// ListIssueOptions 列出 Issue 的选项
type ListIssueOptions struct {
	ListOptions
	State  string // open, closed, all
	Labels string // 逗号分隔的标签名
	Type   string // issues, pulls
}

// CreateIssueCommentOption 创建 Issue 评论的选项
type CreateIssueCommentOption struct {
	Body string `json:"body"`
}

// --- 仓库选项 ---

// CreateBranchOption 创建分支的选项
type CreateBranchOption struct {
	BranchName    string `json:"new_branch_name"`
	OldBranchName string `json:"old_branch_name,omitempty"` // 默认使用仓库默认分支
}
