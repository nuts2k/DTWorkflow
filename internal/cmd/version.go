package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

type versionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	Go        string `json:"go"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本信息",
	Run: func(cmd *cobra.Command, args []string) {
		info := versionInfo{
			Version:   version,
			Commit:    gitCommit,
			BuildTime: buildTime,
			Go:        runtime.Version(),
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
		}

		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(info); err != nil {
				fmt.Fprintf(os.Stderr, "输出版本信息失败: %v\n", err)
				os.Exit(1)
			}
			return
		}

		fmt.Printf("dtworkflow %s\n", info.Version)
		fmt.Printf("  commit: %s\n", info.Commit)
		fmt.Printf("  built:  %s\n", info.BuildTime)
		fmt.Printf("  go:     %s\n", info.Go)
		fmt.Printf("  os/arch: %s/%s\n", info.OS, info.Arch)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
