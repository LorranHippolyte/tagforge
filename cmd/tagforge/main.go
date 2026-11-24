package main

import (
	"fmt"
	"os"

	"github.com/LorranHippolyte/tagforge/internal/cli"
)

func main() {
	cmd := cli.NewRootCmd(cli.Dependencies{})
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
