package main

import (
	"fmt"
	"os"

	"github.com/fluxplane/fluxplane-core/apps/evaluator"
)

func main() {
	cmd := evaluator.NewCommand()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
