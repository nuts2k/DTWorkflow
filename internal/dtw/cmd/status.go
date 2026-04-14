package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看服务器运行状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		var result map[string]any
		if err := client.Do(context.Background(), "GET", "/api/v1/status", nil, &result); err != nil {
			return fmt.Errorf("查询状态失败: %w", err)
		}

		if flagJSON {
			return printer.PrintJSON(result)
		}

		printer.PrintHuman("服务器状态: 正常")
		for k, v := range result {
			printer.PrintHuman("  %s: %v", k, v)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
