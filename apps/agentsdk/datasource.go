package agentsdk

import (
	"context"
	"fmt"
	"io"
	"strings"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	"github.com/fluxplane/agentruntime/apps/launch"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/orchestration/datasourceindex"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
	"github.com/spf13/cobra"
)

type datasourceIndexOptions struct {
	datasource     string
	entity         string
	full           bool
	dryRun         bool
	limit          int
	connectorsPath string
	storePath      string
	provider       string
	model          string
	dev            bool
}

func newDatasourceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "datasource",
		Short: "Manage configured datasources",
	}
	cmd.AddCommand(newDatasourceIndexCommand())
	return cmd
}

func newDatasourceIndexCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Manage datasource semantic indexes",
	}
	cmd.AddCommand(newDatasourceIndexBuildCommand())
	cmd.AddCommand(newDatasourceIndexStatusCommand())
	cmd.AddCommand(newDatasourceIndexClearCommand())
	return cmd
}

func newDatasourceIndexBuildCommand() *cobra.Command {
	var opts datasourceIndexOptions
	cmd := &cobra.Command{
		Use:   "build [app-dir]",
		Short: "Build or update the datasource semantic index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatasourceIndexBuild(cmd.Context(), opts, optionalAppDir(args), cmd.OutOrStdout())
		},
	}
	addDatasourceIndexFlags(cmd, &opts)
	cmd.Flags().BoolVar(&opts.full, "full", false, "delete stale indexed records after a complete scan")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "scan corpus without writing index state")
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "maximum corpus records per datasource/entity")
	return cmd
}

func newDatasourceIndexStatusCommand() *cobra.Command {
	var opts datasourceIndexOptions
	cmd := &cobra.Command{
		Use:   "status [app-dir]",
		Short: "Show datasource semantic index status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatasourceIndexStatus(cmd.Context(), opts, optionalAppDir(args), cmd.OutOrStdout())
		},
	}
	addDatasourceIndexFlags(cmd, &opts)
	return cmd
}

func newDatasourceIndexClearCommand() *cobra.Command {
	var opts datasourceIndexOptions
	cmd := &cobra.Command{
		Use:   "clear [app-dir]",
		Short: "Remove datasource semantic index entries",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatasourceIndexClear(cmd.Context(), opts, optionalAppDir(args), cmd.OutOrStdout())
		},
	}
	addDatasourceIndexFlags(cmd, &opts)
	return cmd
}

func addDatasourceIndexFlags(cmd *cobra.Command, opts *datasourceIndexOptions) {
	cmd.Flags().StringVar(&opts.datasource, "datasource", "", "datasource name to select")
	cmd.Flags().StringVar(&opts.entity, "entity", "", "entity type to select")
	cmd.Flags().StringVar(&opts.connectorsPath, "connectors-path", "~/.connectors", "connector credential store path")
	cmd.Flags().StringVar(&opts.storePath, "store", "", "semantic index store path")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "embedding provider: axon, hash, or openai")
	cmd.Flags().StringVar(&opts.model, "model", "", "embedding model id")
	cmd.Flags().BoolVar(&opts.dev, "dev", false, "enable local developer datasources such as session history")
}

func runDatasourceIndexBuild(ctx context.Context, opts datasourceIndexOptions, appDir string, out io.Writer) error {
	env, err := datasourceIndexRuntime(ctx, opts, appDir)
	if err != nil {
		return err
	}
	defer func() { _ = env.Close() }()
	result, err := datasourceindex.Build(ctx, datasourceindex.Request{
		Registry:   env.Registry,
		Index:      env.Index,
		Datasource: coredatasource.Name(strings.TrimSpace(opts.datasource)),
		Entity:     coredatasource.EntityType(strings.TrimSpace(opts.entity)),
		Full:       opts.full,
		DryRun:     opts.dryRun,
		Limit:      opts.limit,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "documents=%d indexed=%d skipped=%d deleted=%d failed=%d\n", result.Documents, result.Indexed, result.Skipped, result.Deleted, result.Failed)
	return nil
}

func runDatasourceIndexStatus(ctx context.Context, opts datasourceIndexOptions, appDir string, out io.Writer) error {
	env, err := datasourceIndexRuntime(ctx, opts, appDir)
	if err != nil {
		return err
	}
	defer func() { _ = env.Close() }()
	status, err := env.Index.Status(ctx, semantic.StatusRequest{
		Datasource: coredatasource.Name(strings.TrimSpace(opts.datasource)),
		Entity:     coredatasource.EntityType(strings.TrimSpace(opts.entity)),
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "%-24s %-20s %-32s %-8s %-6s %s\n", "DATASOURCE", "ENTITY", "ID", "STATUS", "CHUNKS", "INDEXED")
	for _, doc := range status.Documents {
		_, _ = fmt.Fprintf(out, "%-24s %-20s %-32s %-8s %-6d %s\n", doc.Ref.Datasource, doc.Ref.Entity, doc.Ref.ID, doc.Status, doc.ChunkCount, doc.IndexedAt.Format("2006-01-02T15:04:05Z"))
	}
	return nil
}

func runDatasourceIndexClear(ctx context.Context, opts datasourceIndexOptions, appDir string, out io.Writer) error {
	env, err := datasourceIndexRuntime(ctx, opts, appDir)
	if err != nil {
		return err
	}
	defer func() { _ = env.Close() }()
	status, err := env.Index.Status(ctx, semantic.StatusRequest{
		Datasource: coredatasource.Name(strings.TrimSpace(opts.datasource)),
		Entity:     coredatasource.EntityType(strings.TrimSpace(opts.entity)),
	})
	if err != nil {
		return err
	}
	for _, doc := range status.Documents {
		if err := env.Index.Delete(ctx, doc.Ref); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(out, "deleted=%d\n", len(status.Documents))
	return nil
}

func datasourceIndexRuntime(ctx context.Context, opts datasourceIndexOptions, appDir string) (launch.DatasourceIndexRuntime, error) {
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return launch.DatasourceIndexRuntime{}, err
	}
	return launch.NewDatasourceIndexRuntime(ctx, launch.DatasourceIndexOptions{
		Root:      loaded.Root,
		Spec:      loaded.Distribution.Spec,
		Bundles:   loaded.Distribution.Bundles,
		Launch:    loaded.Launch,
		AuthPath:  opts.connectorsPath,
		StorePath: opts.storePath,
		Provider:  opts.provider,
		Model:     opts.model,
		Dev:       opts.dev,
	})
}

func optionalAppDir(args []string) string {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "."
	}
	return args[0]
}
