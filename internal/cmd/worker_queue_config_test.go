package cmd

import (
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
)

func TestBuildQueueTimeoutConfigFromAppConfig(t *testing.T) {
	cfg := &config.Config{
		Worker: config.WorkerConfig{
			Timeouts: config.TaskTimeouts{
				ReviewPR: 15 * time.Minute,
				FixIssue: 45 * time.Minute,
				GenTests: 30 * time.Minute,
			},
		},
	}

	got := buildQueueTimeoutConfigFromAppConfig(cfg)
	if got.ReviewPR != 15*time.Minute {
		t.Fatalf("ReviewPR = %s, want %s", got.ReviewPR, 15*time.Minute)
	}
	if got.FixIssue != 45*time.Minute {
		t.Fatalf("FixIssue = %s, want %s", got.FixIssue, 45*time.Minute)
	}
	if got.GenTests != 30*time.Minute {
		t.Fatalf("GenTests = %s, want %s", got.GenTests, 30*time.Minute)
	}
}

func TestBuildWorkerStreamMonitorConfigFromAppConfig(t *testing.T) {
	cfg := &config.Config{
		Worker: config.WorkerConfig{
			StreamMonitor: config.StreamMonitorConf{
				Enabled:         true,
				ActivityTimeout: 2 * time.Minute,
			},
		},
	}

	got := buildWorkerStreamMonitorConfigFromAppConfig(cfg)
	if !got.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if got.ActivityTimeout != 2*time.Minute {
		t.Fatalf("ActivityTimeout = %s, want %s", got.ActivityTimeout, 2*time.Minute)
	}
}

// TestBuildQueueTimeoutConfigFromAppConfig_NilConfig 覆盖 cfg == nil 时返回零值
func TestBuildQueueTimeoutConfigFromAppConfig_NilConfig(t *testing.T) {
	got := buildQueueTimeoutConfigFromAppConfig(nil)
	if got.ReviewPR != 0 {
		t.Fatalf("ReviewPR = %s, want 0", got.ReviewPR)
	}
	if got.FixIssue != 0 {
		t.Fatalf("FixIssue = %s, want 0", got.FixIssue)
	}
	if got.GenTests != 0 {
		t.Fatalf("GenTests = %s, want 0", got.GenTests)
	}
}

// TestBuildWorkerTimeoutConfigFromAppConfig_NilConfig 覆盖 cfg == nil 时返回零值
func TestBuildWorkerTimeoutConfigFromAppConfig_NilConfig(t *testing.T) {
	got := buildWorkerTimeoutConfigFromAppConfig(nil)
	if got.ReviewPR != 0 {
		t.Fatalf("ReviewPR = %s, want 0", got.ReviewPR)
	}
	if got.FixIssue != 0 {
		t.Fatalf("FixIssue = %s, want 0", got.FixIssue)
	}
	if got.GenTests != 0 {
		t.Fatalf("GenTests = %s, want 0", got.GenTests)
	}
}
