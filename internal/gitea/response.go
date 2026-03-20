package gitea

import (
	"net/http"
	"regexp"
	"strconv"
)

// Response 封装 HTTP 响应，附加分页元信息
type Response struct {
	*http.Response
	TotalCount int // X-Total-Count 响应头
	NextPage   int // 下一页页码（0 表示无下一页）
	LastPage   int // 最后一页页码
}

// newResponse 从 http.Response 创建 Response，并解析分页信息
func newResponse(r *http.Response) *Response {
	resp := &Response{Response: r}
	resp.parsePagination()
	return resp
}

// linkRelPattern 匹配 Link header 中的 rel 关系
var linkRelPattern = regexp.MustCompile(`<([^>]+)>;\s*rel="([^"]+)"`)

// parsePagination 从 Link 响应头和 X-Total-Count 解析分页信息
func (r *Response) parsePagination() {
	if tc := r.Header.Get("X-Total-Count"); tc != "" {
		r.TotalCount, _ = strconv.Atoi(tc)
	}

	// 解析 Link header: <url>; rel="next", <url>; rel="last"
	link := r.Header.Get("Link")
	if link == "" {
		return
	}

	matches := linkRelPattern.FindAllStringSubmatch(link, -1)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		rawURL := match[1]
		rel := match[2]

		// 从 URL 中提取 page 参数
		page := extractPageParam(rawURL)
		if page == 0 {
			continue
		}

		switch rel {
		case "next":
			r.NextPage = page
		case "last":
			r.LastPage = page
		}
	}
}

// pageParamPattern 匹配 URL 查询参数中的 page 值
var pageParamPattern = regexp.MustCompile(`[?&]page=(\d+)`)

// extractPageParam 从 URL 字符串中提取 page 查询参数值
func extractPageParam(rawURL string) int {
	match := pageParamPattern.FindStringSubmatch(rawURL)
	if len(match) < 2 {
		return 0
	}
	page, _ := strconv.Atoi(match[1])
	return page
}
