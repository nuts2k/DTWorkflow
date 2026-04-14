package dtw

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForTask_ImmediateSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": TaskStatus{ID: "t1", Status: "succeeded", Result: "done"},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	opts := DefaultWaitOptions()
	opts.Timeout = 5 * time.Second

	status, err := WaitForTask(context.Background(), client, "t1", opts)
	if err != nil {
		t.Fatalf("WaitForTask 失败: %v", err)
	}
	if status.Status != "succeeded" {
		t.Errorf("Status = %q，期望 succeeded", status.Status)
	}
	if status.Result != "done" {
		t.Errorf("Result = %q，期望 done", status.Result)
	}
}

func TestWaitForTask_EventualSuccess(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		status := "running"
		if n >= 3 {
			status = "succeeded"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": TaskStatus{ID: "t2", Status: status},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	opts := WaitOptions{
		Timeout: 10 * time.Second,
		CheckInterval: func(_ time.Duration) time.Duration {
			return 50 * time.Millisecond
		},
	}

	status, err := WaitForTask(context.Background(), client, "t2", opts)
	if err != nil {
		t.Fatalf("WaitForTask 失败: %v", err)
	}
	if status.Status != "succeeded" {
		t.Errorf("Status = %q，期望 succeeded", status.Status)
	}
	if callCount.Load() < 3 {
		t.Errorf("调用次数 = %d，期望 >= 3", callCount.Load())
	}
}

func TestWaitForTask_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": TaskStatus{ID: "t3", Status: "failed", Error: "build error"},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	opts := DefaultWaitOptions()
	opts.Timeout = 5 * time.Second

	status, err := WaitForTask(context.Background(), client, "t3", opts)
	if err != nil {
		t.Fatalf("WaitForTask 失败: %v", err)
	}
	if status.Status != "failed" {
		t.Errorf("Status = %q，期望 failed", status.Status)
	}
	if status.Error != "build error" {
		t.Errorf("Error = %q，期望 build error", status.Error)
	}
}

func TestWaitForTask_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": TaskStatus{ID: "t4", Status: "running"},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	opts := WaitOptions{
		Timeout: 200 * time.Millisecond,
		CheckInterval: func(_ time.Duration) time.Duration {
			return 50 * time.Millisecond
		},
	}

	_, err := WaitForTask(context.Background(), client, "t4", opts)
	if err == nil {
		t.Fatal("期望超时错误，但得到 nil")
	}
}

func TestWaitForTask_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": TaskStatus{ID: "t5", Status: "running"},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "token")
	ctx, cancel := context.WithCancel(context.Background())

	opts := WaitOptions{
		Timeout: 30 * time.Second,
		CheckInterval: func(_ time.Duration) time.Duration {
			return 50 * time.Millisecond
		},
	}

	// 100ms 后取消
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := WaitForTask(ctx, client, "t5", opts)
	if err == nil {
		t.Fatal("期望 context 取消错误，但得到 nil")
	}
}

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		status   string
		terminal bool
	}{
		{"succeeded", true},
		{"failed", true},
		{"cancelled", true},
		{"running", false},
		{"pending", false},
		{"queued", false},
	}
	for _, tt := range tests {
		if got := isTerminal(tt.status); got != tt.terminal {
			t.Errorf("isTerminal(%q) = %v，期望 %v", tt.status, got, tt.terminal)
		}
	}
}
