package cmd

import (
	"fmt"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		info := versionInfo{
			Version:   version,
			Commit:    gitCommit,
			BuildTime: buildTime,
			Go:        runtime.Version(),
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
		}

		PrintResult(info, func(data any) string {
			v := data.(versionInfo)
			return fmt.Sprintf("dtworkflow %s\n  commit: %s\n  built:  %s\n  go:     %s\n  os/arch: %s/%s\n",
				v.Version, v.Commit, v.BuildTime, v.Go, v.OS, v.Arch)
		})
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
