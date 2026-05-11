package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
)

// e2e 命令标志
var (
	e2eRepo    string
	e2eModule  string
	e2eCase    string
	e2eEnv     string
	e2eBaseURL string
	e2eRef     string
	e2eNoWait  bool
	e2eTimeout time.Duration
)

var e2eCmd = &cobra.Command{
	Use:   "e2e",
	Short: "E2E 测试相关命令",
}

var e2eRunCmd = &cobra.Command{
	Use:   "run",
	Short: "触发 E2E 测试任务",
	Long:  "通过 dtworkflow serve 的 REST API 触发仓库的 E2E 测试任务。默认等待任务完成，可通过 --no-wait 仅提交不等待。",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 解析 owner/repo
		parts := strings.SplitN(e2eRepo, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("--repo 格式应为 owner/repo")
		}
		owner, repo := parts[0], parts[1]

		// case 非空时 module 必须非空
		if e2eCase != "" && e2eModule == "" {
			return fmt.Errorf("指定 --case 时必须同时指定 --module")
		}

		body := map[string]string{}
		if e2eModule != "" {
			body["module"] = e2eModule
		}
		if e2eCase != "" {
			body["case"] = e2eCase
		}
		if e2eEnv != "" {
			body["env"] = e2eEnv
		}
		if e2eBaseURL != "" {
			body["base_url"] = e2eBaseURL
		}
		if e2eRef != "" {
			body["ref"] = e2eRef
		}

		var result struct {
			Split  bool   `json:"split"`
			TaskID string `json:"task_id"`
			Tasks  []struct {
				TaskID string `json:"task_id"`
				Module string `json:"module"`
			} `json:"tasks"`
		}

		path := fmt.Sprintf("/api/v1/repos/%s/%s/e2e", owner, repo)
		if err := client.Do(cmd.Context(), "POST", path, body, &result); err != nil {
			return fmt.Errorf("提交 E2E 任务失败: %w", err)
		}

		// 多模块拆分场景（API 仅在多任务时设置 split=true，> 1 为防御性检查）
		if result.Split && len(result.Tasks) > 1 {
			if e2eNoWait {
				if flagJSON {
					return printer.PrintJSON(result)
				}
				printer.PrintHuman("整仓扫描发现 %d 个 E2E 模块，已拆分入队：", len(result.Tasks))
				for i, t := range result.Tasks {
					printer.PrintHuman("  [%d] module=%-20s task_id=%s", i+1, t.Module, t.TaskID)
				}
				return nil
			}
			if flagJSON {
				return printer.PrintJSON(result)
			}
			printer.PrintHuman("已拆分为 %d 个子任务，逐任务等待完成...", len(result.Tasks))

			opts := dtw.DefaultWaitOptions()
			if e2eTimeout > 0 {
				opts.Timeout = e2eTimeout
			}

			var firstErr error
			for i, t := range result.Tasks {
				printer.PrintHuman("  [%d/%d] module=%s task_id=%s 等待中...",
					i+1, len(result.Tasks), t.Module, t.TaskID)
				status, err := dtw.WaitForTask(cmd.Context(), client, t.TaskID, opts)
				if err != nil {
					printer.PrintHuman("  [%d/%d] module=%s 等待失败: %v", i+1, len(result.Tasks), t.Module, err)
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				printer.PrintHuman("  [%d/%d] module=%s status=%s", i+1, len(result.Tasks), t.Module, status.Status)
				if status.Error != "" {
					printer.PrintHuman("    错误: %s", status.Error)
				}
				if status.Status == "failed" && firstErr == nil {
					firstErr = fmt.Errorf("E2E 任务失败: module=%s task_id=%s", t.Module, t.TaskID)
				}
			}
			return firstErr
		}

		// 单任务场景
		taskID := result.TaskID
		if taskID == "" && len(result.Tasks) > 0 {
			taskID = result.Tasks[0].TaskID
		}

		if e2eNoWait {
			return printer.Print(fmt.Sprintf("E2E 任务已创建: %s", taskID), result)
		}

		if !flagJSON {
			printer.PrintHuman("E2E 任务已创建: %s", taskID)
		}

		opts := dtw.DefaultWaitOptions()
		if e2eTimeout > 0 {
			opts.Timeout = e2eTimeout
		}

		if !flagJSON {
			printer.PrintHuman("等待任务完成...")
		}
		status, err := dtw.WaitForTask(cmd.Context(), client, taskID, opts)
		if err != nil {
			return fmt.Errorf("等待任务失败: %w", err)
		}

		if flagJSON {
			return printer.PrintJSON(status)
		}

		printer.PrintHuman("task_id: %s  status: %s", status.ID, status.Status)
		if status.Error != "" {
			printer.PrintHuman("错误: %s", status.Error)
		}

		if status.Status == "failed" {
			return fmt.Errorf("E2E 任务失败")
		}
		return nil
	},
}

func init() {
	e2eRunCmd.Flags().StringVar(&e2eRepo, "repo", "", "目标仓库 (owner/repo)")
	e2eRunCmd.Flags().StringVar(&e2eModule, "module", "", "目标模块路径（可选）")
	e2eRunCmd.Flags().StringVar(&e2eCase, "case", "", "测试用例名称（指定 case 时 module 必填）")
	e2eRunCmd.Flags().StringVar(&e2eEnv, "env", "", "目标环境标识（如 staging / prod）")
	e2eRunCmd.Flags().StringVar(&e2eBaseURL, "base-url", "", "被测应用的 Base URL")
	e2eRunCmd.Flags().StringVar(&e2eRef, "ref", "", "基准分支（可选，留空用仓库默认分支）")
	e2eRunCmd.Flags().BoolVar(&e2eNoWait, "no-wait", false, "提交后不等待结果")
	e2eRunCmd.Flags().DurationVar(&e2eTimeout, "timeout", 0, "等待超时时间（默认使用 dtw.DefaultWaitOptions，30m）")

	_ = e2eRunCmd.MarkFlagRequired("repo")

	e2eCmd.AddCommand(e2eRunCmd)
	rootCmd.AddCommand(e2eCmd)
}
