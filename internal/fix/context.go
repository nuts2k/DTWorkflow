package fix

import "otws19.zicp.vip/kelin/dtworkflow/internal/gitea"

// IssueContext 通过 Gitea API 采集的 Issue 富上下文（纯原始数据）
type IssueContext struct {
	Issue    *gitea.Issue     // Issue 详情（标题、描述、状态、标签）
	Comments []*gitea.Comment // Issue 评论列表（单页，最多 50 条）
}
