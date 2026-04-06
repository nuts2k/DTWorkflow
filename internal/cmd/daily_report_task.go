package cmd

import (
	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
)

func newDailyReportTask() *asynq.Task {
	return asynq.NewTask(queue.AsynqTypeGenDailyReport, nil, dailyReportTaskOptions()...)
}

func dailyReportTaskOptions() []asynq.Option {
	return []asynq.Option{
		asynq.MaxRetry(queue.TaskMaxRetry()),
		asynq.Timeout(queue.TaskTimeout(model.TaskTypeGenDailyReport, queue.TaskTimeoutsConfig{})),
	}
}
