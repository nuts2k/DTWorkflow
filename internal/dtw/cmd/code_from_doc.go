package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/code"
	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
	"otws19.zicp.vip/kelin/dtworkflow/internal/validation"
)

var (
	codeFromDocRepo    string
	codeFromDocDocPath string
	codeFromDocBranch  string
	codeFromDocRef     string
	codeFromDocNoWait  bool
	codeFromDocTimeout time.Duration
)

var codeFromDocCmd = &cobra.Command{
	Use:   "code-from-doc",
	Short: "触发文档驱动自动编码任务",
	Long:  "通过 dtworkflow serve 的 REST API 触发文档驱动自动编码任务。默认等待任务完成，可通过 --no-wait 仅提交不等待。",
	RunE: func(cmd *cobra.Command, args []string) error {
		parts := strings.SplitN(codeFromDocRepo, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("--repo 格式应为 owner/repo")
		}
		owner, repo := parts[0], parts[1]

		if err := validation.ValidateDocPath(codeFromDocDocPath); err != nil {
			return fmt.Errorf("--doc %w", err)
		}

		docPath := validation.NormalizeDocPath(codeFromDocDocPath)
		_ = code.DocSlug(docPath) // 校验 slug 可生成
		branch := strings.TrimSpace(codeFromDocBranch)
		if err := validation.ValidateBranchRef(branch); err != nil {
			return fmt.Errorf("--branch %w", err)
		}

		body := map[string]string{
			"doc_path": docPath,
		}
		if branch != "" {
			body["branch"] = branch
		}
		if codeFromDocRef != "" {
			body["ref"] = codeFromDocRef
		}

		var result struct {
			TaskID string `json:"task_id"`
		}

		path := fmt.Sprintf("/api/v1/repos/%s/%s/code-from-doc", owner, repo)
		if err := client.Do(cmd.Context(), "POST", path, body, &result); err != nil {
			return fmt.Errorf("提交 code_from_doc 失败: %w", err)
		}

		taskID := result.TaskID

		if codeFromDocNoWait {
			return printer.Print(fmt.Sprintf("code_from_doc 任务已创建: %s", taskID), result)
		}

		if !flagJSON {
			printer.PrintHuman("code_from_doc 任务已创建: %s", taskID)
		}

		opts := dtw.DefaultWaitOptions()
		if codeFromDocTimeout > 0 {
			opts.Timeout = codeFromDocTimeout
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
			return fmt.Errorf("code_from_doc 任务失败")
		}
		return nil
	},
}

func init() {
	codeFromDocCmd.Flags().StringVar(&codeFromDocRepo, "repo", "", "目标仓库 (owner/repo)")
	codeFromDocCmd.Flags().StringVar(&codeFromDocDocPath, "doc", "", "设计文档路径（必填）")
	codeFromDocCmd.Flags().StringVar(&codeFromDocBranch, "branch", "", "目标分支（省略则从 base ref 派生 auto-code/{slug}）")
	codeFromDocCmd.Flags().StringVar(&codeFromDocRef, "ref", "", "基础 ref（可选，留空用仓库默认分支）")
	codeFromDocCmd.Flags().BoolVar(&codeFromDocNoWait, "no-wait", false, "提交后不等待结果")
	codeFromDocCmd.Flags().DurationVar(&codeFromDocTimeout, "timeout", 0, "等待超时时间（默认使用 dtw.DefaultWaitOptions，30m）")

	_ = codeFromDocCmd.MarkFlagRequired("repo")
	_ = codeFromDocCmd.MarkFlagRequired("doc")

	rootCmd.AddCommand(codeFromDocCmd)
}
