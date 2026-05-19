package coder

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type appHealthcheckOptions struct {
	url     string
	timeout time.Duration
	client  *http.Client
}

func newAppHealthcheckCommand() *cobra.Command {
	var opts appHealthcheckOptions
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Check a served app health endpoint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAppHealthcheck(cmd.Context(), opts, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.url, "url", "http://127.0.0.1:18080/control/status", "health endpoint URL")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 2*time.Second, "health request timeout")
	return cmd
}

func runAppHealthcheck(ctx context.Context, opts appHealthcheckOptions, out io.Writer) error {
	url := strings.TrimSpace(opts.url)
	if url == "" {
		return fmt.Errorf("app healthcheck: --url is required")
	}
	timeout := opts.timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("app healthcheck: %w", err)
	}
	client := opts.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("app healthcheck: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("app healthcheck: %s returned %s", url, resp.Status)
	}
	if out != nil {
		_, _ = io.WriteString(out, "ok\n")
	}
	return nil
}
