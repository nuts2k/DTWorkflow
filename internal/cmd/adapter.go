package cmd

import (
	"context"
	"fmt"
	"path"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/fix"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	testgen "otws19.zicp.vip/kelin/dtworkflow/internal/test"
)

// 编译时断言 giteaCommentAdapter 实现 notify.GiteaCommentCreator 接口
var _ notify.GiteaCommentCreator = (*giteaCommentAdapter)(nil)

// 编译时断言 giteaCommentAdapter 同时实现 notify.GiteaPRCommentManager 扩展接口，
// 打开 M4.2 gen_tests PR 评论幂等 upsert 路径（跨任务覆盖语义）。
// 若此断言失败（未来 notify 接口变动），CommentOnGenTestsPR 会退化为仅 Create 并告警，
// 但不影响编译通过——保留断言是为了明确装配层对接口升级的承诺。
var _ notify.GiteaPRCommentManager = (*giteaCommentAdapter)(nil)

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
// 说明：serve 装配层通过此适配器将 gitea.Client 接入 notify.GiteaNotifier。
type giteaCommentAdapter struct {
	client *gitea.Client
}

func (a *giteaCommentAdapter) CreateIssueComment(ctx context.Context, owner, repo string, index int64, body string) error {
	_, resp, err := a.client.CreateIssueComment(ctx, owner, repo, index, gitea.CreateIssueCommentOption{
		Body: body,
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return err
}

// 分页遍历常量，保护 gen_tests 锚点搜索不被评论数量拖垮。
//
// maxCommentPages：最多拉 20 页 × 50 条/页 = 1000 条评论；超过时 CommentOnGenTestsPR
// 若仍未命中锚点会退化为 Create（接受极端长寿 PR 偶发重复评论）。
const (
	commentListPageSize = 50
	maxCommentPages     = 20
)

// ListIssueComments 实现 notify.GiteaPRCommentManager 接口：
// 拉取指定 Issue/PR 下的评论并裁剪为 notify.GiteaCommentInfo 窄视图。
//
// 分页策略（M4.2 修订）：
//   - 从第 1 页开始逐页拉取 PageSize=50，遇到空页 / 不足 50 条即停止
//   - 最多拉 maxCommentPages 页（1000 条）；超过上限也停止
//   - 锚点评论可能出现在任意页（Gitea 默认按创建时间升序返回），必须遍历完再决策 upsert
//
// 原实现只取首页 50 条，长寿 PR（review + fix 多轮迭代）的 gen_tests 锚点评论
// 常被挤到第 2 页之后，导致 CommentOnGenTestsPR 退化为 Create 产生重复评论。
func (a *giteaCommentAdapter) ListIssueComments(ctx context.Context, owner, repo string, index int64) ([]notify.GiteaCommentInfo, error) {
	var all []notify.GiteaCommentInfo
	for page := 1; page <= maxCommentPages; page++ {
		comments, resp, err := a.client.ListIssueComments(ctx, owner, repo, index, gitea.ListOptions{
			Page:     page,
			PageSize: commentListPageSize,
		})
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			return nil, err
		}
		if len(comments) == 0 {
			break
		}
		for _, c := range comments {
			if c == nil {
				continue
			}
			all = append(all, notify.GiteaCommentInfo{
				ID:   c.ID,
				Body: c.Body,
			})
		}
		// 最后一页（返回少于 PageSize）直接停止，避免无谓的下一页请求
		if len(comments) < commentListPageSize {
			break
		}
	}
	return all, nil
}

// EditIssueComment 实现 notify.GiteaPRCommentManager 接口：
// 按评论 ID 覆盖评论正文，用于 gen_tests Done 事件的幂等 upsert。
func (a *giteaCommentAdapter) EditIssueComment(ctx context.Context, owner, repo string, commentID int64, body string) error {
	_, resp, err := a.client.EditIssueComment(ctx, owner, repo, commentID, gitea.EditIssueCommentOption{
		Body: body,
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return err
}

// 编译时检查 *configAdapter 实现 fix.FixConfigProvider 接口
var _ fix.FixConfigProvider = (*configAdapter)(nil)
var _ testgen.TestConfigProvider = (*configAdapter)(nil)
var _ testgen.RepoFileChecker = (*giteaRepoFileChecker)(nil)

// configAdapter 将 config.Manager 适配为 review.ConfigProvider 接口
type configAdapter struct {
	mgr *config.Manager
}

func (a *configAdapter) ResolveReviewConfig(repoFullName string) config.ReviewOverride {
	cfg := a.mgr.Get()
	if cfg == nil {
		return config.ReviewOverride{}
	}
	return cfg.ResolveReviewConfig(repoFullName)
}

// IsReviewEnabled 实现 queue.ReviewEnabledChecker 接口
func (a *configAdapter) IsReviewEnabled(repoFullName string) bool {
	cfg := a.mgr.Get()
	if cfg == nil {
		return true // 配置未加载时默认启用
	}
	override := cfg.ResolveReviewConfig(repoFullName)
	return override.Enabled == nil || *override.Enabled
}

// GetClaudeModel 实现 fix.FixConfigProvider 接口
func (a *configAdapter) GetClaudeModel() string {
	cfg := a.mgr.Get()
	if cfg == nil {
		return ""
	}
	return cfg.Claude.Model
}

// GetClaudeEffort 实现 fix.FixConfigProvider 接口
func (a *configAdapter) GetClaudeEffort() string {
	cfg := a.mgr.Get()
	if cfg == nil {
		return ""
	}
	return cfg.Claude.Effort
}

// ResolveTestGenConfig 实现 test.TestConfigProvider 接口。
func (a *configAdapter) ResolveTestGenConfig(repoFullName string) config.TestGenOverride {
	cfg := a.mgr.Get()
	if cfg == nil {
		return config.TestGenOverride{}
	}
	return cfg.ResolveTestGenConfig(repoFullName)
}

// giteaRepoFileChecker 基于 Gitea contents API 检测 ref 下文件是否存在。
type giteaRepoFileChecker struct {
	client *gitea.Client
}

func (c *giteaRepoFileChecker) HasFile(ctx context.Context, owner, repo, ref, module, relPath string) (bool, error) {
	if c == nil || c.client == nil {
		return false, fmt.Errorf("giteaRepoFileChecker: client 不能为空")
	}
	if module == "" && relPath == "" {
		return true, nil
	}
	targetPath := relPath
	switch {
	case module != "" && relPath == "":
		targetPath = module
	case module != "":
		targetPath = path.Join(module, relPath)
	}
	contents, _, err := c.client.GetContents(ctx, owner, repo, targetPath, ref)
	if err != nil {
		if gitea.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return contents != nil, nil
}

func (c *giteaRepoFileChecker) ListDir(ctx context.Context, owner, repo, ref, dir string) ([]string, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("giteaRepoFileChecker: client 不能为空")
	}
	entries, _, err := c.client.ListDirContents(ctx, owner, repo, dir, ref)
	if err != nil {
		return nil, fmt.Errorf("ListDir(%s): %w", dir, err)
	}
	var dirs []string
	for _, e := range entries {
		if e.Type == "dir" {
			dirs = append(dirs, e.Name)
		}
	}
	return dirs, nil
}
