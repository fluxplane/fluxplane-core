// Package codercli owns Cobra command construction for the coder product.
package codercli

import (
	"context"

	coderapp "github.com/fluxplane/agentruntime/apps/coder/app"
	"github.com/spf13/cobra"
)

// NewCommand resolves coder app configuration and returns the root coder CLI.
func NewCommand(ctx context.Context, cfg coderapp.Config) (*cobra.Command, error) {
	app, err := coderapp.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return app.Command(), nil
}
