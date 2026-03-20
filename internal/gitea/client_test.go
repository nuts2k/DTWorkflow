package gitea

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Client 构造测试 ---

func TestNewClient_Success(t *testing.T) {
	client, err := NewClient("https://gitea.example.com", WithToken("mytoken"))
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}
	if client.baseURL.String() != "https://gitea.example.com" {
		t.Errorf("baseURL = %q, 期望 %q", client.baseURL.String(), "https://gitea.example.com")
	}
	if client.token != "mytoken" {
		t.Errorf("token = %q, 期望 %q", client.token, "mytoken")
	}
}

func TestNewClient_MissingToken(t *testing.T) {
	_, err := NewClient("https://gitea.example.com")
	if err == nil {
		t.Fatal("期望错误，但未报错")
	}
	if !strings.Contains(err.Error(), "必须提供 API Token") {
		t.Errorf("错误信息 = %q, 期望包含 %q", err.Error(), "必须提供 API Token")
	}
}

func TestNewClient_InvalidURL(t *testing.T) {
	_, err := NewClient("://invalid", WithToken("mytoken"))
	if err == nil {
		t.Fatal("期望 URL 解析错误，但未报错")
	}
}

func TestNewClient_EmptyToken(t *testing.T) {
	_, err := NewClient("https://gitea.example.com", WithToken(""))
	if err == nil {
		t.Fatal("期望错误，但未报错")
	}
	if !strings.Contains(err.Error(), "token 不能为空") {
		t.Errorf("错误信息 = %q, 期望包含 %q", err.Error(), "token 不能为空")
	}
}

func TestNewClient_WithHTTPClient(t *testing.T) {
	customClient := &http.Client{}
	client, err := NewClient("https://gitea.example.com", WithToken("mytoken"), WithHTTPClient(customClient))
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}
	if client.httpClient != customClient {
		t.Error("自定义 HTTP 客户端未生效")
	}
}

func TestNewClient_WithHTTPClient_Nil(t *testing.T) {
	_, err := NewClient("https://gitea.example.com", WithToken("mytoken"), WithHTTPClient(nil))
	if err == nil {
		t.Fatal("期望错误，但未报错")
	}
}

func TestNewClient_WithUserAgent(t *testing.T) {
	client, err := NewClient("https://gitea.example.com", WithToken("mytoken"), WithUserAgent("my-agent/1.0"))
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}
	if client.userAgent != "my-agent/1.0" {
		t.Errorf("userAgent = %q, 期望 %q", client.userAgent, "my-agent/1.0")
	}
}

// --- 请求构造测试 ---

func TestNewRequest_Authorization(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/test", func(w http.ResponseWriter, r *http.Request) {
		testHeader(t, r, "Authorization", "token test-token")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}")) //nolint:errcheck
	})

	req, err := client.newRequest(context.Background(), http.MethodGet, "/api/v1/test", nil)
	if err != nil {
		t.Fatalf("newRequest 失败: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "token test-token" {
		t.Errorf("Authorization = %q, 期望 %q", got, "token test-token")
	}
}

func TestNewRequest_ContentType(t *testing.T) {
	client, err := NewClient("https://gitea.example.com", WithToken("mytoken"))
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}

	// POST 带 body 时设置 Content-Type
	body := map[string]string{"key": "value"}
	req, err := client.newRequest(context.Background(), http.MethodPost, "/api/v1/test", body)
	if err != nil {
		t.Fatalf("newRequest 失败: %v", err)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, 期望 %q", got, "application/json")
	}
}

func TestNewRequest_AcceptJSON(t *testing.T) {
	client, err := NewClient("https://gitea.example.com", WithToken("mytoken"))
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}

	req, err := client.newRequest(context.Background(), http.MethodGet, "/api/v1/test", nil)
	if err != nil {
		t.Fatalf("newRequest 失败: %v", err)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, 期望 %q", got, "application/json")
	}
}

// --- 响应处理测试 ---

func TestDoRequest_JSONDecode(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, loadFixture(t, "repo.json"))
	})

	req, err := client.newRequest(context.Background(), http.MethodGet, "/api/v1/repos/owner/repo", nil)
	if err != nil {
		t.Fatalf("newRequest 失败: %v", err)
	}

	var repo Repository
	resp, err := client.doRequest(req, &repo)
	if err != nil {
		t.Fatalf("doRequest 失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, 期望 200", resp.StatusCode)
	}
	if repo.Name == "" {
		t.Error("期望 repo.Name 非空")
	}
}

func TestDoRequest_ErrorResponse(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/error", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, []byte(`{"message":"internal server error"}`))
	})

	req, err := client.newRequest(context.Background(), http.MethodGet, "/api/v1/error", nil)
	if err != nil {
		t.Fatalf("newRequest 失败: %v", err)
	}

	_, err = client.doRequest(req, nil)
	if err == nil {
		t.Fatal("期望错误，但未报错")
	}

	var errResp *ErrorResponse
	if !isErrorResponse(err, &errResp) {
		t.Fatalf("期望 *ErrorResponse, 得到: %T", err)
	}
	if errResp.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, 期望 500", errResp.StatusCode)
	}
}

// isErrorResponse 辅助函数，检查 err 是否为 *ErrorResponse
func isErrorResponse(err error, target **ErrorResponse) bool {
	return errors.As(err, target)
}

// --- 错误判断测试 ---

func TestCheckResponse_StatusCodes(t *testing.T) {
	cases := []struct {
		code      int
		checkFunc func(error) bool
		name      string
	}{
		{http.StatusNotFound, IsNotFound, "404"},
		{http.StatusUnauthorized, IsUnauthorized, "401"},
		{http.StatusForbidden, IsForbidden, "403"},
		{http.StatusConflict, IsConflict, "409"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, client := setup(t)
			mux.HandleFunc("/api/v1/test-"+tc.name, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				json.NewEncoder(w).Encode(map[string]string{"message": http.StatusText(tc.code)}) //nolint:errcheck
			})

			req, err := client.newRequest(context.Background(), http.MethodGet, "/api/v1/test-"+tc.name, nil)
			if err != nil {
				t.Fatalf("newRequest 失败: %v", err)
			}
			_, err = client.doRequest(req, nil)
			if err == nil {
				t.Fatal("期望错误，但未报错")
			}
			if !tc.checkFunc(err) {
				t.Errorf("期望 %s 错误判断为 true", tc.name)
			}
		})
	}
}

func TestIsNotFound(t *testing.T) {
	mux, client := setup(t)
	mux.HandleFunc("/api/v1/notfound", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, []byte(`{"message":"not found"}`))
	})

	req, _ := client.newRequest(context.Background(), http.MethodGet, "/api/v1/notfound", nil)
	_, err := client.doRequest(req, nil)
	if !IsNotFound(err) {
		t.Errorf("IsNotFound = false, 期望 true")
	}
	if IsUnauthorized(err) || IsForbidden(err) || IsConflict(err) {
		t.Error("其他错误判断应为 false")
	}
}

func TestIsUnauthorized(t *testing.T) {
	mux, client := setup(t)
	mux.HandleFunc("/api/v1/unauthorized", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, []byte(`{"message":"unauthorized"}`))
	})

	req, _ := client.newRequest(context.Background(), http.MethodGet, "/api/v1/unauthorized", nil)
	_, err := client.doRequest(req, nil)
	if !IsUnauthorized(err) {
		t.Errorf("IsUnauthorized = false, 期望 true")
	}
}

func TestIsForbidden(t *testing.T) {
	mux, client := setup(t)
	mux.HandleFunc("/api/v1/forbidden", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		writeJSON(w, []byte(`{"message":"forbidden"}`))
	})

	req, _ := client.newRequest(context.Background(), http.MethodGet, "/api/v1/forbidden", nil)
	_, err := client.doRequest(req, nil)
	if !IsForbidden(err) {
		t.Errorf("IsForbidden = false, 期望 true")
	}
}

func TestIsConflict(t *testing.T) {
	mux, client := setup(t)
	mux.HandleFunc("/api/v1/conflict", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, []byte(`{"message":"conflict"}`))
	})

	req, _ := client.newRequest(context.Background(), http.MethodGet, "/api/v1/conflict", nil)
	_, err := client.doRequest(req, nil)
	if !IsConflict(err) {
		t.Errorf("IsConflict = false, 期望 true")
	}
}

// --- 分页测试 ---

func TestParsePagination(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/pagination", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Count", "42")
		w.Header().Set("Link", `<http://localhost/api/v1/pagination?page=3>; rel="next", <http://localhost/api/v1/pagination?page=5>; rel="last"`)
		writeJSON(w, []byte(`[]`))
	})

	req, err := client.newRequest(context.Background(), http.MethodGet, "/api/v1/pagination", nil)
	if err != nil {
		t.Fatalf("newRequest 失败: %v", err)
	}
	resp, err := client.doRequest(req, nil)
	if err != nil {
		t.Fatalf("doRequest 失败: %v", err)
	}
	if resp.TotalCount != 42 {
		t.Errorf("TotalCount = %d, 期望 42", resp.TotalCount)
	}
	if resp.NextPage != 3 {
		t.Errorf("NextPage = %d, 期望 3", resp.NextPage)
	}
	if resp.LastPage != 5 {
		t.Errorf("LastPage = %d, 期望 5", resp.LastPage)
	}
}

// --- Context 测试 ---

func TestContextCancellation(t *testing.T) {
	// 创建已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []byte(`{}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, WithToken("test-token"))
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}

	req, err := client.newRequest(ctx, http.MethodGet, "/api/v1/test", nil)
	if err != nil {
		t.Fatalf("newRequest 失败: %v", err)
	}

	_, err = client.doRequest(req, nil)
	if err == nil {
		t.Fatal("期望 context 取消错误，但未报错")
	}
}

// --- ErrorResponse 测试 ---

func TestErrorResponse_Error(t *testing.T) {
	errResp := &ErrorResponse{
		StatusCode: 404,
		Method:     "GET",
		Path:       "/api/v1/repos/owner/repo",
		Message:    "not found",
	}
	got := errResp.Error()
	want := "GET /api/v1/repos/owner/repo: 404 not found"
	if got != want {
		t.Errorf("Error() = %q, 期望 %q", got, want)
	}
}

// --- URL 转义测试 ---

func TestURLPathEscape_BranchWithSlash(t *testing.T) {
	mux, client := setup(t)

	mux.HandleFunc("/api/v1/repos/owner/repo/branches/feature%2Flogin", func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "GET")
		writeJSON(w, []byte(`{"name":"feature/login","commit":{"id":"abc123","url":""}}`))
	})

	branch, _, err := client.GetBranch(context.Background(), "owner", "repo", "feature/login")
	if err != nil {
		t.Fatalf("GetBranch 含斜杠分支名失败: %v", err)
	}
	if branch.Name != "feature/login" {
		t.Errorf("Name = %q, 期望 %q", branch.Name, "feature/login")
	}
}
