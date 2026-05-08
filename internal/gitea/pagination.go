package gitea

import "context"

const (
	DefaultPageSize = 50
	DefaultMaxPages = 20
)

// PaginateAll 逐页拉取 Gitea 分页 API 的全量结果。
//
// 页码推进使用 resp.NextPage（HATEOAS 模式）；maxPages 按迭代次数计数。
// 截断于 maxPages 时返回已拉取的部分数据 + truncated=true + nil error。
// 中途 error 返回 (nil, false, err)，丢弃已拉取的部分数据。
func PaginateAll[T any](
	ctx context.Context,
	pageSize, maxPages int,
	fetch func(ctx context.Context, page, pageSize int) ([]T, *Response, error),
) ([]T, bool, error) {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	var all []T
	page := 1
	for i := 0; i < maxPages; i++ {
		items, resp, err := fetch(ctx, page, pageSize)
		if err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		if resp == nil || resp.NextPage == 0 {
			return all, false, nil
		}
		page = resp.NextPage
	}
	return all, true, nil
}
