package dtw

import (
	"context"
	"fmt"
	"time"
)

// WaitOptions 控制轮询行为
type WaitOptions struct {
	Timeout       time.Duration
	CheckInterval func(elapsed time.Duration) time.Duration
}

// DefaultWaitOptions 返回默认轮询配置：30 分钟超时，自适应间隔
func DefaultWaitOptions() WaitOptions {
	return WaitOptions{
		Timeout: 30 * time.Minute,
		CheckInterval: func(elapsed time.Duration) time.Duration {
			if elapsed < 30*time.Second {
				return 2 * time.Second
			}
			return 5 * time.Second
		},
	}
}

// TaskStatus 表示服务端任务状态
type TaskStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error_message,omitempty"`
}

func isTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled":
		return true
	}
	return false
}

// WaitForTask 轮询等待任务完成，返回终态或超时/取消错误
func WaitForTask(ctx context.Context, client *Client, taskID string, opts WaitOptions) (*TaskStatus, error) {
	start := time.Now()
	deadline := start.Add(opts.Timeout)

	for {
		var status TaskStatus
		if err := client.Do(ctx, "GET", "/api/v1/tasks/"+taskID, nil, &status); err != nil {
			return nil, fmt.Errorf("查询任务状态失败: %w", err)
		}

		if isTerminal(status.Status) {
			return &status, nil
		}

		if time.Now().After(deadline) {
			return &status, fmt.Errorf("等待超时（%s），当前状态: %s", opts.Timeout, status.Status)
		}

		interval := opts.CheckInterval(time.Since(start))
		select {
		case <-ctx.Done():
			return &status, ctx.Err()
		case <-time.After(interval):
		}
	}
}
