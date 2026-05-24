package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	distlocal "github.com/fluxplane/fluxplane-core/adapters/distribution/local"
	coreagent "github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/operation"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
	"github.com/spf13/cobra"
)

type operationRunOptions struct {
	appDir        string
	authPath      string
	allowAuthEnv  bool
	privateNet    bool
	yolo          bool
	dev           bool
	pluginFactory func(PluginFactoryContext) []pluginhost.Plugin
	loader        Loader
}

// NewOperationCommand returns the operation utility command group.
func NewOperationCommand() *cobra.Command {
	return NewOperationCommandWithLoader(distlocal.Load, nil)
}

// NewOperationCommandWithLoader returns the operation utility command group
// with injectable app loading and plugin factory hooks for tests.
func NewOperationCommandWithLoader(loader Loader, pluginFactory func(PluginFactoryContext) []pluginhost.Plugin) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "op",
		Short: "Run configured operations",
	}
	cmd.AddCommand(newOperationRunCommand(loader, pluginFactory))
	return cmd
}

func newOperationRunCommand(loader Loader, pluginFactory func(PluginFactoryContext) []pluginhost.Plugin) *cobra.Command {
	opts := operationRunOptions{appDir: ".", loader: loader, pluginFactory: pluginFactory}
	cmd := &cobra.Command{
		Use:   "run NAME [JSON]",
		Short: "Run one configured operation with JSON parameters",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, err := operationRunParams(cmd.InOrStdin(), args[1:])
			if err != nil {
				return err
			}
			return runOperationCommand(cmd.Context(), opts, args[0], params, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.appDir, "app", opts.appDir, "app directory containing fluxplane.yaml")
	cmd.Flags().StringVar(&opts.authPath, "auth-path", "", "native plugin auth store path")
	cmd.Flags().BoolVar(&opts.allowAuthEnv, "allow-plugin-auth-env", false, "allow plugin auth methods to resolve credentials from the process environment")
	cmd.Flags().BoolVar(&opts.privateNet, "allow-private-network", false, "allow operations to reach private or local network targets")
	cmd.Flags().BoolVar(&opts.yolo, "yolo", false, "auto-approve local operation risk gates for this run")
	cmd.Flags().BoolVar(&opts.dev, "dev", false, "enable local developer datasources such as session history")
	return cmd
}

func operationRunParams(in io.Reader, args []string) (map[string]any, error) {
	if len(args) == 0 {
		return map[string]any{}, nil
	}
	raw := strings.TrimSpace(args[0])
	if raw == "-" {
		data, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		raw = strings.TrimSpace(string(data))
	}
	if raw == "" {
		return map[string]any{}, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("op run: params must be a JSON object: %w", err)
	}
	if params == nil {
		params = map[string]any{}
	}
	return params, nil
}

func runOperationCommand(ctx context.Context, opts operationRunOptions, name string, params map[string]any, out io.Writer) error {
	loader := opts.loader
	if loader == nil {
		loader = distlocal.Load
	}
	loaded, err := loader(ctx, opts.appDir)
	if err != nil {
		return err
	}
	runtime, err := Launch(ctx, RuntimeOptions{
		Root:                loaded.Root,
		Spec:                loaded.Distribution.Spec,
		Bundles:             loaded.Distribution.Bundles,
		Launch:              loaded.Launch,
		AuthPath:            opts.authPath,
		AllowPluginAuthEnv:  opts.allowAuthEnv,
		AllowPrivateNetwork: opts.privateNet,
		Yolo:                opts.yolo,
		Dev:                 opts.dev,
		PluginFactory:       opts.pluginFactory,
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	op, ok := runtime.Composition.Operations.Get(operation.Name(strings.TrimSpace(name)))
	if !ok {
		return fmt.Errorf("op run: unknown operation %q; available operations: %s", name, strings.Join(operationNames(runtime.Composition.Operations.All()), ", "))
	}
	agent := runtime.Composition.Agent
	if agent == nil {
		agent = operationRunAgent(runtime, loaded.Distribution.Spec.DefaultSession)
	}
	callID := operation.CallID("op-run")
	opCtx := operation.NewContext(ctx, event.Discard())
	opCtx = sessionenv.OperationContext(opCtx, sessionenv.Config{
		Agent:             agent,
		OperationExecutor: runtime.Composition.OperationExecutor,
		Events:            event.Discard(),
	}, callID)
	result := runtime.Composition.OperationExecutor.Execute(opCtx, op, params)
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if result.IsError() {
		if result.Error != nil && result.Error.Message != "" {
			return fmt.Errorf("op run: %s", result.Error.Message)
		}
		return fmt.Errorf("op run: operation status %s", result.Status)
	}
	return nil
}

func operationRunAgent(runtime Runtime, defaultSession coresession.Ref) coreagent.Agent {
	sessionName := strings.TrimSpace(string(defaultSession.Name))
	if sessionName == "" {
		sessionName = "default"
	}
	sessionBinding, err := runtime.Composition.SessionCatalog.Resolve(sessionName)
	if err != nil || sessionBinding.Spec.Agent.Name == "" || runtime.Composition.Resolver == nil {
		return nil
	}
	agentID, err := runtime.Composition.Resolver.ResolveInScope("agent", string(sessionBinding.Spec.Agent.Name), sessionBinding.ID)
	if err != nil {
		return nil
	}
	agentBinding, ok := runtime.Composition.AgentCatalog[agentID.Address()]
	if !ok {
		return nil
	}
	return staticOperationRunAgent{spec: agentBinding.Spec}
}

type staticOperationRunAgent struct {
	spec coreagent.Spec
}

func (a staticOperationRunAgent) Spec() coreagent.Spec {
	return a.spec
}

func (staticOperationRunAgent) Step(coreagent.Context, coreagent.StepInput) coreagent.StepResult {
	return coreagent.StepResult{}
}

func operationNames(ops []operation.Operation) []string {
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		if op == nil {
			continue
		}
		if name := strings.TrimSpace(string(op.Spec().Ref.Name)); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

var _ Loader = distlocal.Load
