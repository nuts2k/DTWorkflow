package main

import (
	"os"

	"otws19.zicp.vip/kelin/dtworkflow/internal/cmd"
)

func main() {
	err := cmd.Execute()
	if err != nil {
		cmd.PrintError(err)
		os.Exit(cmd.ExitCode(err))
	}
}
