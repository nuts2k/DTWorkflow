package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/e2e"
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

// ListIssueComments 实现 notify.GiteaPRCommentManager 接口：
// 拉取指定 Issue/PR 下的评论并裁剪为 notify.GiteaCommentInfo 窄视图。
//
// 分页策略：最多拉 20 页 × 50 条/页 = 1000 条评论；锚点评论可能出现在任意页
// （Gitea 默认按创建时间升序返回），必须遍历完再决策 upsert。
func (a *giteaCommentAdapter) ListIssueComments(ctx context.Context, owner, repo string, index int64) ([]notify.GiteaCommentInfo, error) {
	comments, truncated, err := gitea.PaginateAll(ctx, 50, 20,
		func(ctx context.Context, page, pageSize int) ([]*gitea.Comment, *gitea.Response, error) {
			return a.client.ListIssueComments(ctx, owner, repo, index, gitea.ListOptions{
				Page: page, PageSize: pageSize,
			})
		})
	if err != nil {
		return nil, err
	}
	if truncated {
		slog.WarnContext(ctx, "评论列表被截断，锚点评论幂等 upsert 可能退化为 create",
			"owner", owner, "repo", repo, "index", index, "fetched", len(comments))
	}
	result := make([]notify.GiteaCommentInfo, 0, len(comments))
	for _, c := range comments {
		if c == nil {
			continue
		}
		result = append(result, notify.GiteaCommentInfo{
			ID:   c.ID,
			Body: c.Body,
		})
	}
	return result, nil
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
var _ e2e.E2EConfigProvider = (*configAdapter)(nil)
var _ e2e.E2EModuleScanner = (*giteaRepoFileChecker)(nil)

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

// ResolveE2EConfig 实现 e2e.E2EConfigProvider 接口。
func (a *configAdapter) ResolveE2EConfig(repoFullName string) config.E2EOverride {
	cfg := a.mgr.Get()
	if cfg == nil {
		return config.E2EOverride{}
	}
	return cfg.ResolveE2EConfig(repoFullName)
}

// GetE2EEnvironments 实现 e2e.E2EConfigProvider 接口。
func (a *configAdapter) GetE2EEnvironments() map[string]config.E2EEnvironment {
	cfg := a.mgr.Get()
	if cfg == nil {
		return nil
	}
	return cfg.E2E.Environments
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
		if gitea.IsNotFound(err) {
			return nil, fmt.Errorf("ListDir(%s): %w", dir, e2e.ErrDirNotFound)
		}
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
