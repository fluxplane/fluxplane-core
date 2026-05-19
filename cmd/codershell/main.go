package main

import (
	"fmt"
	"os"

	"github.com/fluxplane/agentruntime/apps/coder/shell"
)

func main() {
	path := "."
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	if err := shell.Run(shell.Options{Path: path}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
