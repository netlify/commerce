package main

import (
	"fmt"
	"os"

	"github.com/netlify/commerce/cmd"
)

func main() {
	cmd.InitCommandFlags()
	if err := cmd.RootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to run command: %v\n", err)
		os.Exit(1)
	}
}
