package main

import (
	"os"

	dtwcmd "otws19.zicp.vip/kelin/dtworkflow/internal/dtw/cmd"
)

func main() {
	if err := dtwcmd.Execute(); err != nil {
		os.Exit(1)
	}
}
