package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateIssueAttachment(t *testing.T) {
	var gotFilename string
	var gotContent []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("期望 POST，实际 %s", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/owner/repo/issues/42/assets" {
			t.Fatalf("路径错误: %s", r.URL.Path)
		}

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("期望 multipart/form-data，实际=%s err=%v", mediaType, err)
		}

		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			t.Fatalf("读取 part: %v", err)
		}
		gotFilename = part.FileName()
		gotContent, _ = io.ReadAll(part)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Attachment{
			ID:          1,
			Name:        gotFilename,
			DownloadURL: "https://gitea.example.com/attachments/1",
		})
	}))
	defer ts.Close()

	c, _ := NewClient(ts.URL, WithToken("test-token"))
	att, _, err := c.CreateIssueAttachment(context.Background(), "owner", "repo",
		42, "screenshot.png", bytes.NewReader([]byte("fake-png-data")))
	if err != nil {
		t.Fatalf("CreateIssueAttachment 失败: %v", err)
	}
	if att.ID != 1 {
		t.Errorf("期望 attachment id=1，实际=%d", att.ID)
	}
	if gotFilename != "screenshot.png" {
		t.Errorf("期望文件名 screenshot.png，实际=%s", gotFilename)
	}
	if !bytes.Equal(gotContent, []byte("fake-png-data")) {
		t.Errorf("文件内容不匹配")
	}
}
