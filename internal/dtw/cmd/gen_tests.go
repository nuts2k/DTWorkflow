package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
)

// gen-tests 命令标志（M4.1 Layer 4）
var (
	genTestsRepo      string
	genTestsModule    string
	genTestsRef       string
	genTestsFramework string
	genTestsNoWait    bool
	genTestsTimeout   time.Duration
)

var genTestsCmd = &cobra.Command{
	Use:   "gen-tests",
	Short: "通过 REST API 触发 gen_tests（测试生成）任务",
	Long:  "通过 dtworkflow serve 的 REST API 触发仓库的测试生成任务。默认等待任务完成，可通过 --no-wait 仅提交不等待。",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 解析 owner/repo
		parts := strings.SplitN(genTestsRepo, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("--repo 格式应为 owner/repo")
		}
		owner, repo := parts[0], parts[1]

		// 参数基本校验（API 层会再校验一次，本地校验可给出更快反馈）
		if err := validateGenTestsFrameworkFlag(genTestsFramework); err != nil {
			return err
		}
		if err := validateGenTestsModuleFlag(genTestsModule); err != nil {
			return err
		}

		// 只把用户实际给出的字段放进 body，空字段省略，由 API 层按默认值处理
		body := map[string]string{}
		if genTestsModule != "" {
			body["module"] = genTestsModule
		}
		if genTestsRef != "" {
			body["ref"] = genTestsRef
		}
		if genTestsFramework != "" {
			body["framework"] = genTestsFramework
		}

		var result struct {
			TaskID string `json:"task_id"`
		}

		path := fmt.Sprintf("/api/v1/repos/%s/%s/gen-tests", owner, repo)
		if err := client.Do(cmd.Context(), "POST", path, body, &result); err != nil {
			return fmt.Errorf("提交 gen_tests 失败: %w", err)
		}

		// --no-wait：仅提交不轮询
		if genTestsNoWait {
			return printer.Print(fmt.Sprintf("gen_tests 任务已创建: %s", result.TaskID), result)
		}

		if !flagJSON {
			printer.PrintHuman("gen_tests 任务已创建: %s", result.TaskID)
		}

		// 等待任务完成
		opts := dtw.DefaultWaitOptions()
		if genTestsTimeout > 0 {
			opts.Timeout = genTestsTimeout
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
			return fmt.Errorf("gen_tests 任务失败")
		}
		return nil
	},
}

func init() {
	genTestsCmd.Flags().StringVar(&genTestsRepo, "repo", "", "目标仓库 (owner/repo)")
	genTestsCmd.Flags().StringVar(&genTestsModule, "module", "", "目标模块路径（可选，留空为整仓生成）")
	genTestsCmd.Flags().StringVar(&genTestsRef, "ref", "", "基准分支（可选，留空用仓库默认分支）")
	genTestsCmd.Flags().StringVar(&genTestsFramework, "framework", "", "强制测试框架（junit5 / vitest，可选）")
	genTestsCmd.Flags().BoolVar(&genTestsNoWait, "no-wait", false, "提交后不等待结果")
	genTestsCmd.Flags().DurationVar(&genTestsTimeout, "timeout", 0, "等待超时时间（默认使用 dtw.DefaultWaitOptions，30m）")

	_ = genTestsCmd.MarkFlagRequired("repo")

	rootCmd.AddCommand(genTestsCmd)
}

// validateGenTestsFrameworkFlag 仅允许空 / junit5 / vitest 三种取值。
// 本地兜底，API 层还会再校验一次；防止空跑一个网络请求才发现打错框架名。
func validateGenTestsFrameworkFlag(framework string) error {
	switch framework {
	case "", "junit5", "vitest":
		return nil
	default:
		return fmt.Errorf("--framework 合法值为 \"junit5\" / \"vitest\"，当前值: %q", framework)
	}
}

// validateGenTestsModuleFlag 拒绝绝对路径与包含 .. 的相对路径。
// API 层会再做一次规范化与越界校验，这里只过滤明显非法输入。
func validateGenTestsModuleFlag(module string) error {
	if module == "" {
		return nil
	}
	if strings.HasPrefix(module, "/") {
		return fmt.Errorf("--module 不能为绝对路径: %q", module)
	}
	if module == ".." || strings.HasPrefix(module, "../") || strings.Contains(module, "/../") || strings.HasSuffix(module, "/..") {
		return fmt.Errorf("--module 不能包含 ..: %q", module)
	}
	return nil
}
