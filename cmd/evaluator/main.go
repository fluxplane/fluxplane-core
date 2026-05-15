package main

import (
	"fmt"
	"os"

	"github.com/fluxplane/agentruntime/apps/evaluator"
)

func main() {
	cmd := evaluator.NewCommand()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
