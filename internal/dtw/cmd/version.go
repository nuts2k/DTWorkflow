package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示 dtw 版本信息",
	Run: func(cmd *cobra.Command, args []string) {
		if flagJSON {
			printer.PrintJSON(map[string]string{
				"version":    dtwVersion,
				"commit":     dtwCommit,
				"build_time": dtwBuildTime,
			})
		} else {
			fmt.Printf("dtw %s (commit: %s, built: %s)\n", dtwVersion, dtwCommit, dtwBuildTime)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
