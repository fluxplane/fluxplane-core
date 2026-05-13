package main

import (
	"fmt"
	"os"

	"github.com/fluxplane/agentruntime/apps/agentsdk"
)

func main() {
	if err := agentsdk.NewCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
