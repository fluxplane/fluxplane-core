package main

import (
	"fmt"
	"os"

	"github.com/fluxplane/agentruntime/apps/coder"
)

func main() {
	cmd := coder.NewCommand()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
