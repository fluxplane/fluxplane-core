package deploy

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	distrun "github.com/fluxplane/engine/adapters/distribution/run"
	coredistribution "github.com/fluxplane/engine/core/distribution"
	corellm "github.com/fluxplane/engine/core/llm"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/orchestration/distribution"
)

const (
	defaultAuthPath       = "/auth"
	defaultBaseImage      = "fluxplane/fluxplane-base:local"
	defaultCoderBaseImage = "fluxplane/coder-base:local"
	defaultAppImage       = "agentruntime-app:latest"
	defaultMySQLDSNEnv    = "AGENTRUNTIME_DATASTORE_MYSQL_DSN"
	defaultNATSDSNEnv     = "AGENTRUNTIME_EVENTSTORE_NATS_DSN"
	defaultHealthAddr     = "127.0.0.1:18080"
	defaultKubeHealthAddr = "0.0.0.0:18080"
	defaultHealthURL      = "http://127.0.0.1:18080/control/status"
	openRouterAPIKeyEnv   = "OPENROUTER_API_KEY"
	defaultRegistryPort   = "5000"
)

const deployStackLabel = "agentruntime.fluxplane.io/deploy-stack"

const dockerComposeWaitTimeoutSeconds = "30"

const (
	// DefaultAppProvider is the model provider used by generated app containers.
	DefaultAppProvider = "openrouter"
	// DefaultAppModel is the model used by generated app containers.
	DefaultAppModel = "openai/gpt-5.5"
	// DefaultAppEffort is the reasoning effort used by generated app containers.
	DefaultAppEffort = "medium"
)

var (
	detectKubernetesContext = currentKubernetesContext
	detectK3DClusterName    = currentK3DClusterName
)

// CommandRunner runs an external command.
type CommandRunner interface {
	Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error
}

// DockerClient performs deploy-package Docker operations.
type DockerClient interface {
	BuildImage(ctx context.Context, contextDir, dockerfile string, tags, platforms []string, push bool, stdout, stderr io.Writer) error
	TagImage(ctx context.Context, source, target string, stdout, stderr io.Writer) error
	PushImage(ctx context.Context, image string, stdout, stderr io.Writer) error
	DeployComposeStack(ctx context.Context, stack dockerComposeStack, stdout, stderr io.Writer) error
	UndeployComposeStack(ctx context.Context, stack dockerComposeStack, volumes bool, stdout, stderr io.Writer) error
}

// KubernetesClient performs deploy-package Kubernetes operations.
type KubernetesClient interface {
	ApplyManifest(ctx context.Context, content string, stdout, stderr io.Writer) error
	DeleteManifest(ctx context.Context, content string, stdout, stderr io.Writer) error
	WaitDeployment(ctx context.Context, namespace, name string, timeout time.Duration, stdout, stderr io.Writer) error
}

// PortForwarder starts a temporary local port-forward.
type PortForwarder interface {
	Forward(ctx context.Context, namespace string, stdout, stderr io.Writer) (PortForward, error)
}

// PortForward stops a temporary local port-forward.
type PortForward interface {
	Close() error
}

// CommandRunnerFunc adapts a function to CommandRunner.
type CommandRunnerFunc func(context.Context, string, string, []string, io.Writer, io.Writer) error

func (f CommandRunnerFunc) Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	return f(ctx, dir, name, args, stdout, stderr)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

type appRuntimeOptions struct {
	Provider           string
	Model              string
	Effort             string
	AllowPluginAuthEnv bool
}

func (opts appRuntimeOptions) withDefaults() appRuntimeOptions {
	opts.Provider = firstNonEmpty(strings.TrimSpace(opts.Provider), DefaultAppProvider)
	opts.Model = firstNonEmpty(strings.TrimSpace(opts.Model), DefaultAppModel)
	opts.Effort = firstNonEmpty(strings.TrimSpace(opts.Effort), DefaultAppEffort)
	return opts
}

func resolveAppRuntime(loaded distribution.Loaded, opts appRuntimeOptions) appRuntimeOptions {
	providerOverride := strings.TrimSpace(opts.Provider)
	model := firstNonEmpty(strings.TrimSpace(opts.Model), strings.TrimSpace(loaded.Distribution.Spec.Deploy.Model), strings.TrimSpace(loaded.Distribution.Spec.DefaultModel.Model))
	if model == "" {
		model = DefaultAppModel
		if providerOverride == "" {
			providerOverride = DefaultAppProvider
		}
	}
	provider := firstNonEmpty(providerOverride, strings.TrimSpace(loaded.Distribution.Spec.DefaultModel.Provider), "openai")
	effort := strings.TrimSpace(opts.Effort)
	registry, err := distrun.DefaultModelRegistryWithAliases(llmProviderSpecs(loaded.Distribution.Bundles), llmModelAliases(loaded.Distribution.Bundles))
	if err == nil {
		selection := registry.ResolveModelSelection(provider, model)
		provider = selection.Provider
		model = selection.Model
		if effort == "" {
			if _, modelSpec, ok := registry.ModelSpec(provider, model); ok {
				effort = strings.TrimSpace(modelSpec.Params.ReasoningEffort)
			}
		}
	}
	return appRuntimeOptions{
		Provider:           provider,
		Model:              model,
		Effort:             firstNonEmpty(effort, DefaultAppEffort),
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
	}.withDefaults()
}

func llmProviderSpecs(bundles []resource.ContributionBundle) []corellm.ProviderSpec {
	var out []corellm.ProviderSpec
	for _, bundle := range bundles {
		out = append(out, bundle.LLMProviders...)
	}
	return out
}

func llmModelAliases(bundles []resource.ContributionBundle) []corellm.ModelAliasSpec {
	var out []corellm.ModelAliasSpec
	for _, bundle := range bundles {
		out = append(out, bundle.LLMModelAliases...)
	}
	return out
}

func resolveTags(spec coredistribution.Spec, override []string) []string {
	if tags := cleanStrings(override); len(tags) > 0 {
		return tags
	}
	if spec.Build.Docker != nil && strings.TrimSpace(spec.Build.Docker.Image) != "" {
		image := strings.TrimSpace(spec.Build.Docker.Image)
		tags := cleanStrings(spec.Build.Docker.Tags)
		if len(tags) == 0 {
			return []string{image + ":latest"}
		}
		out := make([]string, 0, len(tags))
		for _, tag := range tags {
			if strings.Contains(tag, "/") || strings.Contains(tag, ":") {
				out = append(out, tag)
				continue
			}
			out = append(out, image+":"+tag)
		}
		return out
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = "agentruntime-app"
	}
	return []string{name + ":latest"}
}

func resolveAppBuildTags(spec coredistribution.Spec, opts AppBuildOptions) []string {
	var override []string
	if image := strings.TrimSpace(opts.Image); image != "" {
		override = append(override, image)
	}
	override = append(override, opts.Tags...)
	return resolveTags(spec, override)
}

func distributionName(spec coredistribution.Spec) string {
	if name := strings.TrimSpace(spec.Name); name != "" {
		return name
	}
	return "app"
}

func appServeCommand(authPath string, appRuntime appRuntimeOptions) []string {
	return appServeCommandWithHealthAddr(authPath, appRuntime, defaultHealthAddr)
}

func appServeCommandWithHealthAddr(authPath string, appRuntime appRuntimeOptions, healthAddr string) []string {
	authPath = strings.TrimSpace(authPath)
	if authPath == "" {
		authPath = defaultAuthPath
	}
	if strings.TrimSpace(healthAddr) == "" {
		healthAddr = defaultHealthAddr
	}
	appRuntime = appRuntime.withDefaults()
	command := []string{
		"serve", "/app",
		"--auth-path", authPath,
		"--health-addr", healthAddr,
		"--provider", appRuntime.Provider,
		"--model", appRuntime.Model,
		"--effort", appRuntime.Effort,
	}
	if appRuntime.AllowPluginAuthEnv {
		command = append(command, "--allow-plugin-auth-env")
	}
	return command
}

func appHealthcheckCommand() []string {
	return []string{"/usr/local/bin/fluxplane", "healthcheck", "--url", defaultHealthURL}
}

func cleanStrings(input []string) []string {
	var out []string
	for _, value := range input {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func maybeWriteFile(filename, content string, perm fs.FileMode, dryRun, force bool, out io.Writer) error {
	if dryRun {
		_, _ = fmt.Fprintf(out, "write=%s\n", filename)
		return nil
	}
	if _, err := os.Stat(filename); err == nil && !force {
		return fmt.Errorf("distribution build: %s already exists; pass --force to overwrite", filename)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filename, []byte(content), perm)
}

func appDeployDir(appRoot string) string {
	return filepath.Join(appRoot, ".deploy")
}

func maybeWriteKubernetesManifest(appRoot, filename, content string, dryRun, force bool, out io.Writer) error {
	if err := maybeWriteFile(filename, content, 0o600, dryRun, force, out); err != nil {
		return err
	}
	if dryRun || !isInAppDeployDir(appRoot, filename) {
		return nil
	}
	return ensureGitignoreEntry(appRoot, ".deploy/")
}

func isInAppDeployDir(appRoot, filename string) bool {
	rel, err := filepath.Rel(appRoot, filename)
	if err != nil {
		return false
	}
	return rel == ".deploy" || strings.HasPrefix(rel, ".deploy"+string(filepath.Separator))
}

func ensureGitignoreEntry(root, entry string) error {
	filename := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(filename)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	content := text
	if text != "" && !strings.HasSuffix(text, "\n") {
		content += "\n"
	}
	content += entry + "\n"
	return os.WriteFile(filename, []byte(content), 0o644)
}

func writeTempManifest(pattern, content string) (string, error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if _, err := io.WriteString(file, content); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func printAppBuildCommand(out io.Writer, command []string, dryRun bool) {
	if dryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
	}
}

func firstTag(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return tags[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseKubernetesNodeSelectors(values []string) (map[string]string, error) {
	raw := cleanStrings(values)
	if len(raw) == 0 {
		return nil, nil
	}
	selectors := make(map[string]string, len(raw))
	for _, value := range raw {
		key, selectorValue, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		selectorValue = strings.TrimSpace(selectorValue)
		if !ok || key == "" || selectorValue == "" {
			return nil, fmt.Errorf("distribution deploy: node selector must be key=value, got %q", value)
		}
		selectors[key] = selectorValue
	}
	return selectors, nil
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func joinYAMLDocuments(docs []string) string {
	var cleaned []string
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc != "" {
			cleaned = append(cleaned, doc+"\n")
		}
	}
	return strings.Join(cleaned, "---\n")
}

func splitYAMLDocuments(content string) []string {
	var docs []string
	for _, doc := range strings.Split(content, "\n---\n") {
		doc = strings.TrimSpace(doc)
		if doc != "" {
			docs = append(docs, doc+"\n")
		}
	}
	return docs
}

func printKubernetesSecretSummary(out io.Writer, name string, keys []string, dryRun bool) {
	if !dryRun {
		return
	}
	if len(keys) == 0 {
		_, _ = fmt.Fprintf(out, "secret=%s external=true\n", name)
		return
	}
	_, _ = fmt.Fprintf(out, "secret=%s keys=%s\n", name, strings.Join(keys, ","))
}
