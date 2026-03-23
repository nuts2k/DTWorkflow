package cmd

import (
	"context"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
)

// 编译时断言 giteaCommentAdapter 实现 notify.GiteaCommentCreator 接口
var _ notify.GiteaCommentCreator = (*giteaCommentAdapter)(nil)

// giteaCommentAdapter 将 gitea.Client 适配为 notify.GiteaCommentCreator 窄接口。
//
// notify 包定义的窄接口签名为：
//
//	CreateIssueComment(ctx, owner, repo string, index int64, body string) error
//
// 而 gitea.Client 的实际签名为：
//
//	CreateIssueComment(ctx, owner, repo string, index int64, opts CreateIssueCommentOption) (*Comment, *Response, error)
//
// 适配器负责：
// (a) 将 body string 包装为 CreateIssueCommentOption{Body: body}
// (b) 丢弃 *Comment 和 *Response 返回值，只保留 error
//
// TODO: 在 M1.8 配置管理完成后，通过此适配器将 GiteaNotifier 接入通知框架。
type giteaCommentAdapter struct {
	client *gitea.Client
}

func (a *giteaCommentAdapter) CreateIssueComment(ctx context.Context, owner, repo string, index int64, body string) error {
	_, _, err := a.client.CreateIssueComment(ctx, owner, repo, index, gitea.CreateIssueCommentOption{
		Body: body,
	})
	return err
}
