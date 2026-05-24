package main

import (
	"fmt"
	"os"

	fluxplaneapp "github.com/fluxplane/fluxplane-core/apps/fluxplane"
)

func main() {
	cmd := fluxplaneapp.NewCommand()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
