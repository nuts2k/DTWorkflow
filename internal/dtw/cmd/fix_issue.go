package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
)

var (
	fixRepo    string
	fixIssue   int
	fixNoWait  bool
	fixTimeout time.Duration
)

var fixIssueCmd = &cobra.Command{
	Use:   "fix-issue",
	Short: "触发 Issue 自动修复",
	RunE: func(cmd *cobra.Command, args []string) error {
		parts := strings.SplitN(fixRepo, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("--repo 格式应为 owner/repo")
		}
		owner, repo := parts[0], parts[1]

		body := map[string]int{"issue_number": fixIssue}
		var result struct {
			TaskID string `json:"task_id"`
		}

		path := fmt.Sprintf("/api/v1/repos/%s/%s/fix-issue", owner, repo)
		if err := client.Do(cmd.Context(), "POST", path, body, &result); err != nil {
			return fmt.Errorf("提交修复任务失败: %w", err)
		}

		if fixNoWait {
			return printer.Print(fmt.Sprintf("修复任务已创建: %s", result.TaskID), result)
		}

		if !flagJSON {
			printer.PrintHuman("修复任务已创建: %s", result.TaskID)
		}

		// 等待任务完成
		opts := dtw.DefaultWaitOptions()
		if fixTimeout > 0 {
			opts.Timeout = fixTimeout
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
			return fmt.Errorf("修复任务失败")
		}
		return nil
	},
}

func init() {
	fixIssueCmd.Flags().StringVar(&fixRepo, "repo", "", "目标仓库 (owner/repo)")
	fixIssueCmd.Flags().IntVar(&fixIssue, "issue", 0, "Issue 编号")
	fixIssueCmd.Flags().BoolVar(&fixNoWait, "no-wait", false, "提交后不等待结果")
	fixIssueCmd.Flags().DurationVar(&fixTimeout, "timeout", 0, "等待超时时间（默认 30m）")

	_ = fixIssueCmd.MarkFlagRequired("repo")
	_ = fixIssueCmd.MarkFlagRequired("issue")

	rootCmd.AddCommand(fixIssueCmd)
}
