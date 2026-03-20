package gitea

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// setup 创建 mock HTTP 服务器和指向它的 Client
func setup(t *testing.T) (*http.ServeMux, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL, WithToken("test-token"))
	if err != nil {
		t.Fatalf("创建测试客户端: %v", err)
	}
	return mux, client
}

// testMethod 验证 HTTP 方法
func testMethod(t *testing.T, r *http.Request, want string) {
	t.Helper()
	if r.Method != want {
		t.Errorf("HTTP 方法 = %s, 期望 %s", r.Method, want)
	}
}

// testHeader 验证请求头
func testHeader(t *testing.T, r *http.Request, name, want string) {
	t.Helper()
	if got := r.Header.Get(name); got != want {
		t.Errorf("Header %s = %q, 期望 %q", name, got, want)
	}
}

// loadFixture 读取 testdata 目录下的 JSON fixture
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // 测试夹具文件名由测试代码固定提供
	if err != nil {
		t.Fatalf("加载 fixture %s: %v", name, err)
	}
	return data
}

// writeJSON 向 ResponseWriter 写入 JSON
func writeJSON(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
