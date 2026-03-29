package cmd

import (
	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

func buildQueueTimeoutConfigFromAppConfig(cfg *config.Config) queue.TaskTimeoutsConfig {
	if cfg == nil {
		return queue.TaskTimeoutsConfig{}
	}
	return queue.TaskTimeoutsConfig{
		ReviewPR: cfg.Worker.Timeouts.ReviewPR,
		FixIssue: cfg.Worker.Timeouts.FixIssue,
		GenTests: cfg.Worker.Timeouts.GenTests,
	}
}

func buildWorkerTimeoutConfigFromAppConfig(cfg *config.Config) worker.TaskTimeoutsConfig {
	if cfg == nil {
		return worker.TaskTimeoutsConfig{}
	}
	return worker.TaskTimeoutsConfig{
		ReviewPR: cfg.Worker.Timeouts.ReviewPR,
		FixIssue: cfg.Worker.Timeouts.FixIssue,
		GenTests: cfg.Worker.Timeouts.GenTests,
	}
}

func buildWorkerStreamMonitorConfigFromAppConfig(cfg *config.Config) worker.StreamMonitorConfig {
	if cfg == nil {
		return worker.StreamMonitorConfig{}
	}
	return worker.StreamMonitorConfig{
		Enabled:         cfg.Worker.StreamMonitor.Enabled,
		ActivityTimeout: cfg.Worker.StreamMonitor.ActivityTimeout,
	}
}
