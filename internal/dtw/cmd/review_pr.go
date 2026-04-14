package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
)

var (
	reviewRepo    string
	reviewPR      int
	reviewNoWait  bool
	reviewTimeout time.Duration
)

var reviewPRCmd = &cobra.Command{
	Use:   "review-pr",
	Short: "触发 PR 评审",
	RunE: func(cmd *cobra.Command, args []string) error {
		parts := strings.SplitN(reviewRepo, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("--repo 格式应为 owner/repo")
		}
		owner, repo := parts[0], parts[1]

		body := map[string]int{"pr_number": reviewPR}
		var result struct {
			TaskID string `json:"task_id"`
		}

		path := fmt.Sprintf("/api/v1/repos/%s/%s/review-pr", owner, repo)
		if err := client.Do(cmd.Context(), "POST", path, body, &result); err != nil {
			return fmt.Errorf("提交评审失败: %w", err)
		}

		if reviewNoWait {
			return printer.Print(fmt.Sprintf("评审任务已创建: %s", result.TaskID), result)
		}

		if !flagJSON {
			printer.PrintHuman("评审任务已创建: %s", result.TaskID)
		}

		// 等待任务完成
		opts := dtw.DefaultWaitOptions()
		if reviewTimeout > 0 {
			opts.Timeout = reviewTimeout
		}

		if !flagJSON {
			printer.PrintHuman("等待任务完成...")
		}
		status, err := dtw.WaitForTask(cmd.Context(), client, result.TaskID, opts)
		if err != nil {
			return fmt.Errorf("等待任务失败: %w", err)
		}

		if flagJSON {
			return printer.PrintJSON(status)
		}

		printer.PrintHuman("任务状态: %s", status.Status)
		if status.Error != "" {
			printer.PrintHuman("错误: %s", status.Error)
		}

		if status.Status == "failed" {
			return fmt.Errorf("评审任务失败")
		}
		return nil
	},
}

func init() {
	reviewPRCmd.Flags().StringVar(&reviewRepo, "repo", "", "目标仓库 (owner/repo)")
	reviewPRCmd.Flags().IntVar(&reviewPR, "pr", 0, "PR 编号")
	reviewPRCmd.Flags().BoolVar(&reviewNoWait, "no-wait", false, "提交后不等待结果")
	reviewPRCmd.Flags().DurationVar(&reviewTimeout, "timeout", 0, "等待超时时间（默认 30m）")

	_ = reviewPRCmd.MarkFlagRequired("repo")
	_ = reviewPRCmd.MarkFlagRequired("pr")

	rootCmd.AddCommand(reviewPRCmd)
}
