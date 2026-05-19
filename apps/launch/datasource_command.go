package launch

import (
	"context"
	"fmt"
	"io"
	"strings"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	coredata "github.com/fluxplane/agentruntime/core/data"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/orchestration/datasourceindex"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
	"github.com/spf13/cobra"
)

type datasourceIndexOptions struct {
	datasource     string
	entity         string
	full           bool
	force          bool
	dryRun         bool
	limit          int
	concurrency    int
	freshness      string
	phase          string
	connectorsPath string
	storePath      string
	provider       string
	model          string
	dev            bool
}

type datasourceGitLabCheckOptions struct {
	datasource   string
	mergeRequest string
	output       string
	envFiles     []string
	hostEnv      bool
	storePath    string
	provider     string
	model        string
}

func NewDatasourceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "datasource",
		Short: "Manage configured datasources",
	}
	cmd.AddCommand(newDatasourceIndexCommand())
	cmd.AddCommand(newDatasourceGitLabCommand())
	return cmd
}

func newDatasourceIndexCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Manage datasource indexes",
	}
	cmd.AddCommand(newDatasourceIndexBuildCommand())
	cmd.AddCommand(newDatasourceIndexEmbedCommand())
	cmd.AddCommand(newDatasourceIndexStatusCommand())
	cmd.AddCommand(newDatasourceIndexClearCommand())
	return cmd
}

func newDatasourceGitLabCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gitlab",
		Short: "Diagnose GitLab datasource access",
	}
	cmd.AddCommand(newDatasourceGitLabCheckCommand())
	return cmd
}

func newDatasourceGitLabCheckCommand() *cobra.Command {
	opts := datasourceGitLabCheckOptions{
		datasource: gitlabplugin.Name,
		envFiles:   []string{".env"},
	}
	cmd := &cobra.Command{
		Use:   "check [app-dir]",
		Short: "Check GitLab datasource credentials, MR diff access, and index status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatasourceGitLabCheck(cmd.Context(), opts, optionalAppDir(args), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.datasource, "datasource", opts.datasource, "GitLab datasource name to select")
	cmd.Flags().StringVar(&opts.mergeRequest, "mr", "", "merge request URL or project!iid to check")
	cmd.Flags().StringVar(&opts.output, "output", "text", "output format: text or json")
	cmd.Flags().StringArrayVar(&opts.envFiles, "env-file", opts.envFiles, "root workspace env file or glob to load; may be repeated")
	cmd.Flags().BoolVar(&opts.hostEnv, "host-env", false, "allow host environment values to override env-file values")
	cmd.Flags().StringVar(&opts.storePath, "store", "", "datasource index store path")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "embedding provider: axon, hash, or openai")
	cmd.Flags().StringVar(&opts.model, "model", "", "embedding model id")
	return cmd
}

func newDatasourceIndexBuildCommand() *cobra.Command {
	var opts datasourceIndexOptions
	cmd := &cobra.Command{
		Use:   "build [app-dir]",
		Short: "Build or update the datasource index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatasourceIndexBuild(cmd.Context(), opts, optionalAppDir(args), cmd.OutOrStdout())
		},
	}
	addDatasourceIndexFlags(cmd, &opts)
	cmd.Flags().BoolVar(&opts.full, "full", false, "delete stale indexed records after a complete scan")
	cmd.Flags().BoolVar(&opts.force, "force", false, "bypass datasource index freshness checks")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "scan corpus without writing index state")
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "maximum corpus records per datasource/entity")
	cmd.Flags().IntVar(&opts.concurrency, "concurrency", 0, "datasource/entity indexing concurrency")
	cmd.Flags().StringVar(&opts.freshness, "freshness", "", "skip datasource/entity indexing when the latest successful run is fresher than this duration")
	cmd.Flags().StringVar(&opts.phase, "phase", datasourceindex.PhaseAll, "index phase: all, fields, or semantic")
	return cmd
}

func newDatasourceIndexEmbedCommand() *cobra.Command {
	var opts datasourceIndexOptions
	cmd := &cobra.Command{
		Use:   "embed [app-dir]",
		Short: "Embed queued datasource semantic corpus",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatasourceIndexEmbed(cmd.Context(), opts, optionalAppDir(args), cmd.OutOrStdout())
		},
	}
	addDatasourceIndexFlags(cmd, &opts)
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "maximum queued records to embed")
	return cmd
}

func newDatasourceIndexStatusCommand() *cobra.Command {
	var opts datasourceIndexOptions
	cmd := &cobra.Command{
		Use:   "status [app-dir]",
		Short: "Show datasource index status",
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
		Short: "Remove datasource index entries",
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
	cmd.Flags().StringVar(&opts.storePath, "store", "", "datasource index store path")
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
	concurrency, freshness, err := DatasourceIndexBuildConfig(env.Config, opts.concurrency, opts.freshness)
	if err != nil {
		return err
	}
	result, err := datasourceindex.Build(ctx, datasourceindex.Request{
		Registry:    env.Registry,
		Index:       env.Index,
		DataStore:   env.Data,
		DataSources: env.Sources,
		Datasource:  coredatasource.Name(strings.TrimSpace(opts.datasource)),
		Entity:      coredatasource.EntityType(strings.TrimSpace(opts.entity)),
		Full:        opts.full,
		Force:       opts.force,
		DryRun:      opts.dryRun,
		Limit:       opts.limit,
		Concurrency: concurrency,
		Freshness:   freshness,
		Phase:       opts.phase,
		Progress:    datasourceIndexProgressWriter(out),
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "documents=%d indexed=%d queued=%d skipped=%d deleted=%d failed=%d\n", result.Documents, result.Indexed, result.Queued, result.Skipped, result.Deleted, result.Failed)
	return nil
}

func runDatasourceIndexEmbed(ctx context.Context, opts datasourceIndexOptions, appDir string, out io.Writer) error {
	env, err := datasourceIndexRuntime(ctx, opts, appDir)
	if err != nil {
		return err
	}
	defer func() { _ = env.Close() }()
	result, err := env.Index.ProcessQueue(ctx, semantic.ProcessQueueRequest{
		Datasource: coredatasource.Name(strings.TrimSpace(opts.datasource)),
		Entity:     coredatasource.EntityType(strings.TrimSpace(opts.entity)),
		Limit:      opts.limit,
		Progress:   datasourceIndexEmbedProgressWriter(out),
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "queued=%d embedded=%d skipped=%d failed=%d\n", result.Queued, result.Embedded, result.Skipped, result.Failed)
	return nil
}

func datasourceIndexProgressWriter(out io.Writer) datasourceindex.ProgressReporter {
	return func(event datasourceindex.ProgressEvent) {
		switch event.Kind {
		case datasourceindex.ProgressEntityStart:
			_, _ = fmt.Fprintf(out, "index %s/%s phase=%s start\n", event.Datasource, event.Entity, event.Phase)
		case datasourceindex.ProgressEntityFresh:
			_, _ = fmt.Fprintf(out, "index %s/%s phase=%s fresh until=%s\n", event.Datasource, event.Entity, event.Phase, event.FreshUntil.Format("2006-01-02T15:04:05Z"))
		case datasourceindex.ProgressPageFetched:
			_, _ = fmt.Fprintf(out, "index %s/%s phase=%s page documents=%d tombstones=%d\n", event.Datasource, event.Entity, event.Phase, event.Documents, event.Tombstones)
		case datasourceindex.ProgressDocumentFailed, datasourceindex.ProgressTombstoneFailed:
			_, _ = fmt.Fprintf(out, "index %s/%s phase=%s failed id=%s error=%s\n", event.Datasource, event.Entity, event.Phase, event.RecordID, event.Message)
		case datasourceindex.ProgressDocumentQueued:
			_, _ = fmt.Fprintf(out, "index %s/%s phase=%s queued id=%s\n", event.Datasource, event.Entity, event.Phase, event.RecordID)
		case datasourceindex.ProgressEntityComplete:
			_, _ = fmt.Fprintf(out, "index %s/%s phase=%s complete documents=%d indexed=%d queued=%d skipped=%d deleted=%d failed=%d\n", event.Datasource, event.Entity, event.Phase, event.Documents, event.Indexed, event.Queued, event.Skipped, event.Deleted, event.Failed)
		case datasourceindex.ProgressComplete:
			_, _ = fmt.Fprintf(out, "index phase=%s complete documents=%d indexed=%d queued=%d skipped=%d deleted=%d failed=%d\n", event.Phase, event.Documents, event.Indexed, event.Queued, event.Skipped, event.Deleted, event.Failed)
		}
	}
}

func datasourceIndexEmbedProgressWriter(out io.Writer) semantic.QueueProgressReporter {
	return func(event semantic.QueueProgressEvent) {
		switch event.Kind {
		case semantic.QueueProgressStart:
			_, _ = fmt.Fprintf(out, "index phase=%s queued=%d start\n", datasourceindex.PhaseEmbed, event.Queued)
		case semantic.QueueProgressEmbedded:
			_, _ = fmt.Fprintf(out, "index %s/%s phase=%s embedded id=%s\n", event.Datasource, event.Entity, datasourceindex.PhaseEmbed, event.RecordID)
		case semantic.QueueProgressSkipped:
			_, _ = fmt.Fprintf(out, "index %s/%s phase=%s skipped id=%s status=%s\n", event.Datasource, event.Entity, datasourceindex.PhaseEmbed, event.RecordID, event.Status)
		case semantic.QueueProgressFailed:
			_, _ = fmt.Fprintf(out, "index %s/%s phase=%s failed id=%s error=%s\n", event.Datasource, event.Entity, datasourceindex.PhaseEmbed, event.RecordID, event.Message)
		case semantic.QueueProgressComplete:
			_, _ = fmt.Fprintf(out, "index phase=%s complete queued=%d embedded=%d skipped=%d failed=%d\n", datasourceindex.PhaseEmbed, event.Queued, event.Embedded, event.Skipped, event.Failed)
		}
	}
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
	_, _ = fmt.Fprintf(out, "%-24s %-20s %-32s %-10s %-8s %-6s %s\n", "DATASOURCE", "ENTITY", "ID", "TYPE", "STATUS", "CHUNKS", "INDEXED")
	for _, doc := range status.Documents {
		_, _ = fmt.Fprintf(out, "%-24s %-20s %-32s %-10s %-8s %-6d %s\n", doc.Ref.Datasource, doc.Ref.Entity, doc.Ref.ID, "semantic", doc.Status, doc.ChunkCount, doc.IndexedAt.Format("2006-01-02T15:04:05Z"))
	}
	for _, record := range status.Records {
		_, _ = fmt.Fprintf(out, "%-24s %-20s %-32s %-10s %-8s %-6s %s\n", record.Ref.Datasource, record.Ref.Entity, record.Ref.ID, "field", "indexed", "-", "-")
	}
	for _, job := range status.Queue {
		_, _ = fmt.Fprintf(out, "%-24s %-20s %-32s %-10s %-8s %-6s %s\n", job.Ref.Datasource, job.Ref.Entity, job.Ref.ID, "queue", job.Status, "-", "-")
	}
	for _, run := range status.Runs {
		_, _ = fmt.Fprintf(out, "%-24s %-20s %-32s %-10s %-8s %-6s %s\n", run.Datasource, run.Entity, run.Phase, "run", run.Status, "-", run.CompletedAt.Format("2006-01-02T15:04:05Z"))
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
	deletedCount := 0
	dataResult, err := env.Data.QueryRecords(ctx, coredata.Query{
		Sources:  selectedDataSources(coredatasource.Name(strings.TrimSpace(opts.datasource))),
		Entities: selectedDataEntities(coredatasource.EntityType(strings.TrimSpace(opts.entity))),
		Limit:    100000,
	})
	if err != nil {
		return err
	}
	for _, record := range dataResult.Records {
		if record.Ref.Source == "_runtime" {
			continue
		}
		if err := env.Data.DeleteRecords(ctx, coredata.Scope{}, record.Ref); err != nil {
			return err
		}
		deletedCount++
	}
	semanticCount, err := clearSemanticIndexStatus(ctx, env.Index, status)
	if err != nil {
		return err
	}
	deletedCount += semanticCount
	if err := env.Index.DeleteIndexRuns(ctx, semantic.StatusRequest{
		Datasource: coredatasource.Name(strings.TrimSpace(opts.datasource)),
		Entity:     coredatasource.EntityType(strings.TrimSpace(opts.entity)),
	}); err != nil {
		return err
	}
	if err := datasourceindex.DeleteDataIndexRuns(ctx, env.Data, coredatasource.Name(strings.TrimSpace(opts.datasource)), coredatasource.EntityType(strings.TrimSpace(opts.entity))); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "deleted=%d\n", deletedCount)
	return nil
}

func clearSemanticIndexStatus(ctx context.Context, index *semantic.Index, status semantic.StatusResult) (int, error) {
	if index == nil {
		return 0, nil
	}
	deleted := map[string]bool{}
	count := 0
	for _, doc := range status.Documents {
		if err := index.Delete(ctx, doc.Ref); err != nil {
			return count, err
		}
		deleted[doc.Key] = true
		count++
	}
	for _, record := range status.Records {
		if deleted[record.Key] {
			continue
		}
		if err := index.Delete(ctx, record.Ref); err != nil {
			return count, err
		}
		deleted[record.Key] = true
		count++
	}
	for _, job := range status.Queue {
		if deleted[job.Key] {
			continue
		}
		if err := index.Delete(ctx, job.Ref); err != nil {
			return count, err
		}
		deleted[job.Key] = true
		count++
	}
	return count, nil
}

func selectedDataSources(value coredatasource.Name) []coredata.SourceName {
	if value == "" {
		return nil
	}
	return []coredata.SourceName{coredata.SourceName(value)}
}

func selectedDataEntities(value coredatasource.EntityType) []coredata.EntityType {
	if value == "" {
		return nil
	}
	return []coredata.EntityType{coredata.EntityType(value)}
}

func datasourceIndexRuntime(ctx context.Context, opts datasourceIndexOptions, appDir string) (DatasourceIndexRuntime, error) {
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return DatasourceIndexRuntime{}, err
	}
	return NewDatasourceIndexRuntime(ctx, DatasourceIndexOptions{
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
