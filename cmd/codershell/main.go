package main

import (
	"fmt"
	"os"

	codershell "github.com/fluxplane/agentruntime/apps/coder/shell"
)

func main() {
	cmd := codershell.NewCommand()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
