package main

import (
	"fmt"
	"os"

	distcli "github.com/fluxplane/agentruntime/adapters/distribution/cli"
	"github.com/fluxplane/agentruntime/apps/coder"
)

func main() {
	cmd := distcli.NewCommand(coder.Distribution())
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
