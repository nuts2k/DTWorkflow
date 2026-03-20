package main

import (
	"fmt"
	"os"

	"otws19.zicp.vip/kelin/dtworkflow/internal/cmd"
)

func main() {
	err := cmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cmd.ExitCode(err))
	}
}
