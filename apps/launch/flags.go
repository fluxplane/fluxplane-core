package launch

import (
	"fmt"
	"strings"

	fluxplane "github.com/fluxplane/fluxplane-core"
	distrun "github.com/fluxplane/fluxplane-core/adapters/distribution/run"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/spf13/pflag"
)

// ModelFlags captures model selection and reasoning CLI flags shared by app
// launch commands.
type ModelFlags struct {
	Provider    string
	Model       string
	Thinking    string
	ThinkingSet bool
	Effort      string
	EffortSet   bool
}

// BindModelFlags binds provider, model, thinking, and effort flags to opts.
func BindModelFlags(flags *pflag.FlagSet, opts *ModelFlags, defaults ModelFlags) {
	if opts == nil {
		return
	}
	*opts = defaults
	flags.StringVar(&opts.Provider, "provider", opts.Provider, "model provider")
	flags.StringVar(&opts.Model, "model", opts.Model, "model name or provider/model")
	flags.StringVar(&opts.Thinking, "thinking", opts.Thinking, "reasoning mode: auto|on|off")
	flags.StringVar(&opts.Effort, "effort", opts.Effort, "reasoning effort: low|medium|high|max")
}

// CaptureChanged records which reasoning flags were explicitly set.
func (o *ModelFlags) CaptureChanged(flags *pflag.FlagSet) {
	if o == nil {
		return
	}
	o.ThinkingSet = flags.Changed("thinking")
	o.EffortSet = flags.Changed("effort")
}

// Validate validates model-related flag combinations.
func (o ModelFlags) Validate() error {
	return distrun.ValidateReasoningFlags(o.Thinking, o.ThinkingSet, o.Effort, o.EffortSet)
}

// LocalRuntimeFlags captures local runtime behavior flags shared by commands
// that open or serve local sessions.
type LocalRuntimeFlags struct {
	Debug            bool
	Yolo             bool
	Dev              bool
	AllowMaxToolRisk string
}

// LocalRuntimeFlagHelp customizes local runtime flag descriptions for a command.
type LocalRuntimeFlagHelp struct {
	Debug string
	Yolo  string
	Dev   string
}

// BindLocalRuntimeFlags binds debug, yolo, and dev flags to opts.
func BindLocalRuntimeFlags(flags *pflag.FlagSet, opts *LocalRuntimeFlags, help LocalRuntimeFlagHelp) {
	if opts == nil {
		return
	}
	debugHelp := firstNonEmptyFlagHelp(help.Debug, "print run events as highlighted JSON markdown")
	yoloHelp := firstNonEmptyFlagHelp(help.Yolo, "auto-approve local operation risk gates")
	devHelp := firstNonEmptyFlagHelp(help.Dev, "enable local developer diagnostics, session history datasource, and usage datasource")
	flags.BoolVar(&opts.Debug, "debug", opts.Debug, debugHelp)
	flags.BoolVar(&opts.Yolo, "yolo", opts.Yolo, yoloHelp)
	flags.BoolVar(&opts.Dev, "dev", opts.Dev, devHelp)
	flags.StringVar(&opts.AllowMaxToolRisk, "allow-max-tool-risk", opts.AllowMaxToolRisk, "maximum model-visible tool risk: low|medium|high|critical; omitted allows all")
}

func (o LocalRuntimeFlags) Validate() error {
	if _, err := operation.ParseRiskLevel(o.AllowMaxToolRisk); err != nil {
		return fmt.Errorf("invalid --allow-max-tool-risk: %w", err)
	}
	return nil
}

func (o LocalRuntimeFlags) ToolProjectionMaxRisk() operation.RiskLevel {
	risk, _ := operation.ParseRiskLevel(o.AllowMaxToolRisk)
	return risk
}

func ToolProjectionConfigFromRuntime(o LocalRuntimeFlags) fluxplane.ToolProjectionConfig {
	return fluxplane.ToolProjectionConfig{MaxRisk: o.ToolProjectionMaxRisk()}
}

// LaunchEnvironmentFlags captures local launch environment flags shared by app
// run and serve commands.
type LaunchEnvironmentFlags struct {
	AuthPath           string
	EnvFiles           []string
	AllowPluginAuthEnv bool
}

// BindLaunchEnvironmentFlags binds plugin auth and env-file flags.
func BindLaunchEnvironmentFlags(flags *pflag.FlagSet, opts *LaunchEnvironmentFlags) {
	if opts == nil {
		return
	}
	flags.StringVar(&opts.AuthPath, "auth-path", opts.AuthPath, "native plugin auth store path")
	flags.StringArrayVar(&opts.EnvFiles, "env-file", opts.EnvFiles, "root workspace env file or glob to load; may be repeated")
	flags.BoolVar(&opts.AllowPluginAuthEnv, "allow-plugin-auth-env", opts.AllowPluginAuthEnv, "allow plugin auth methods to resolve credentials from the process environment")
}

func firstNonEmptyFlagHelp(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
