package cmd

import (
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
)

func TestDailyReportTaskOptions(t *testing.T) {
	opts := dailyReportTaskOptions()
	if len(opts) != 2 {
		t.Fatalf("dailyReportTaskOptions() returned %d opts, want 2", len(opts))
	}

	var (
		gotMaxRetry int
		gotTimeout  time.Duration
	)
	for _, opt := range opts {
		switch opt.Type() {
		case asynq.MaxRetryOpt:
			gotMaxRetry = opt.Value().(int)
		case asynq.TimeoutOpt:
			gotTimeout = opt.Value().(time.Duration)
		}
	}

	if gotMaxRetry != queue.TaskMaxRetry() {
		t.Fatalf("max retry = %d, want %d", gotMaxRetry, queue.TaskMaxRetry())
	}
	if gotTimeout != 10*time.Minute {
		t.Fatalf("timeout = %v, want 10m", gotTimeout)
	}
}

func TestNewDailyReportTask(t *testing.T) {
	task := newDailyReportTask()
	if task.Type() != queue.AsynqTypeGenDailyReport {
		t.Fatalf("task type = %q, want %q", task.Type(), queue.AsynqTypeGenDailyReport)
	}
	if len(task.Payload()) != 0 {
		t.Fatalf("payload should be empty, got %q", string(task.Payload()))
	}
}
