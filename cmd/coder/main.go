package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fluxplane/agentruntime/apps/coderapp"
)

func main() {
	app, err := coderapp.New(context.Background(), coderapp.Config{
		Root:            ".",
		CoderConfigPath: configPathFromArgs(os.Args[1:]),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd := app.Command()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func configPathFromArgs(args []string) string {
	for i, arg := range args {
		if arg == "--config" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(arg, "--config=") {
			return strings.TrimPrefix(arg, "--config=")
		}
	}
	return ""
}
