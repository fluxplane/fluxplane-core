package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
	"github.com/fluxplane/engine/orchestration/distribution"
	"gopkg.in/yaml.v3"
)

type dockerComposeStack struct {
	Name           string
	AppDir         string
	Image          string
	ConnectorsPath string
	AppRuntime     appRuntimeOptions
	Launch         distribution.LaunchConfig
}

// ComposeOptions configures Docker Compose artifact generation.
type ComposeOptions struct {
	AppDir   string
	Image    string
	Provider string
	Model    string
	Effort   string
	DryRun   bool
	Out      io.Writer
}

// ComposeResult describes the generated Docker Compose artifact.
type ComposeResult struct {
	AppDir  string
	Image   string
	Content string
	DryRun  bool
}

// ComposeDeployOptions configures local Docker Compose deployment.
type ComposeDeployOptions struct {
	AppDir             string
	TempDir            string
	Image              string
	BaseImage          string
	ConnectorsPath     string
	AllowPluginAuthEnv bool
	Provider           string
	Model              string
	Effort             string
	DryRun             bool
	Force              bool
	Detach             bool
	Out                io.Writer
	Err                io.Writer
	Runner             CommandRunner
	dockerClient       DockerClient
}

// ComposeDeployResult describes the local Docker Compose deployment steps.
type ComposeDeployResult struct {
	BaseImage BaseImageResult
	AppBuild  AppBuildResult
	Command   []string
	DryRun    bool
}

// ComposeUndeployOptions configures local Docker Compose teardown.
type ComposeUndeployOptions struct {
	AppDir       string
	DryRun       bool
	Volumes      bool
	Out          io.Writer
	Err          io.Writer
	Runner       CommandRunner
	dockerClient DockerClient
}

// ComposeUndeployResult describes the local Docker Compose teardown command.
type ComposeUndeployResult struct {
	AppDir  string
	Compose string
	Command []string
	DryRun  bool
	Volumes bool
}

// DeployDockerCompose builds local app artifacts and starts them with Docker Compose.
func DeployDockerCompose(ctx context.Context, opts ComposeDeployOptions) (ComposeDeployResult, error) {
	baseImage := strings.TrimSpace(opts.BaseImage)
	if baseImage == "" {
		baseImage = defaultBaseImage
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	dockerClient := dockerClientFor(opts.Runner, opts.dockerClient)
	base, err := BuildFluxplaneBaseDocker(ctx, BaseImageOptions{
		Tags:         []string{baseImage},
		TempDir:      opts.TempDir,
		DryRun:       opts.DryRun,
		Out:          out,
		Err:          errOut,
		Runner:       opts.Runner,
		dockerClient: opts.dockerClient,
	})
	if err != nil {
		return ComposeDeployResult{}, err
	}
	app, err := BuildApp(ctx, AppBuildOptions{
		AppDir:             opts.AppDir,
		Targets:            []string{"dockerfile", "docker-compose", "docker-image"},
		Image:              opts.Image,
		DryRun:             opts.DryRun,
		Force:              opts.Force,
		BaseImage:          baseImage,
		ConnectorsPath:     opts.ConnectorsPath,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
		Provider:           opts.Provider,
		Model:              opts.Model,
		Effort:             opts.Effort,
		Out:                out,
		Err:                errOut,
		Runner:             opts.Runner,
		dockerClient:       opts.dockerClient,
	})
	if err != nil {
		return ComposeDeployResult{}, err
	}
	command := []string{"docker", "compose", "-f", app.Compose, "up"}
	if opts.Detach {
		command = append(command, "-d")
	}
	result := ComposeDeployResult{
		BaseImage: base,
		AppBuild:  app,
		Command:   command,
		DryRun:    opts.DryRun,
	}
	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
		return result, nil
	}
	if opts.Runner != nil && opts.dockerClient == nil {
		if err := runner.Run(ctx, app.AppDir, command[0], command[1:], out, errOut); err != nil {
			return ComposeDeployResult{}, err
		}
		return result, nil
	}
	stack, err := dockerComposeStackFor(ctx, app.AppDir, firstTag(app.Tags), opts.ConnectorsPath, opts.Provider, opts.Model, opts.Effort, opts.AllowPluginAuthEnv)
	if err != nil {
		return ComposeDeployResult{}, err
	}
	if err := dockerClient.DeployComposeStack(ctx, stack, out, errOut); err != nil {
		return ComposeDeployResult{}, err
	}
	return result, nil
}

// UndeployDockerCompose stops a local Docker Compose app deployment.
func UndeployDockerCompose(ctx context.Context, opts ComposeUndeployOptions) (ComposeUndeployResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return ComposeUndeployResult{}, err
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	dockerClient := dockerClientFor(opts.Runner, opts.dockerClient)
	composePath := filepath.Join(loaded.Root, "docker-compose.yaml")
	command := []string{"docker", "compose", "-f", composePath, "down"}
	if opts.Volumes {
		command = append(command, "-v")
	}
	result := ComposeUndeployResult{
		AppDir:  loaded.Root,
		Compose: composePath,
		Command: command,
		DryRun:  opts.DryRun,
		Volumes: opts.Volumes,
	}
	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
		return result, nil
	}
	if opts.Runner != nil && opts.dockerClient == nil {
		if err := runner.Run(ctx, loaded.Root, command[0], command[1:], out, errOut); err != nil {
			return ComposeUndeployResult{}, err
		}
		return result, nil
	}
	stack, err := dockerComposeStackFor(ctx, loaded.Root, "", "", "", "", "", false)
	if err != nil {
		return ComposeUndeployResult{}, err
	}
	if err := dockerClient.UndeployComposeStack(ctx, stack, opts.Volumes, out, errOut); err != nil {
		return ComposeUndeployResult{}, err
	}
	return result, nil
}

// GenerateDockerCompose generates a minimal Docker Compose deployment for an app image.
func GenerateDockerCompose(ctx context.Context, opts ComposeOptions) (ComposeResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return ComposeResult{}, err
	}
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = firstTag(resolveTags(loaded.Distribution.Spec, nil))
	}
	if image == "" {
		image = defaultAppImage
	}
	appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
		Provider: opts.Provider,
		Model:    opts.Model,
		Effort:   opts.Effort,
	})
	content := dockerComposeContent(loaded.Distribution.Spec.Name, image, defaultConnectorsPath, appRuntime, loaded.Launch)
	result := ComposeResult{
		AppDir:  loaded.Root,
		Image:   image,
		Content: content,
		DryRun:  opts.DryRun,
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	if opts.DryRun {
		_, _ = io.WriteString(out, content)
		return result, nil
	}
	return ComposeResult{}, errors.New("distribution deploy: docker-compose generation currently requires --dry-run")
}

func dockerComposeStackFor(ctx context.Context, appDir, image, connectorsPath, provider, model, effort string, allowPluginAuthEnv bool) (dockerComposeStack, error) {
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return dockerComposeStack{}, err
	}
	if strings.TrimSpace(image) == "" {
		image = firstTag(resolveTags(loaded.Distribution.Spec, nil))
	}
	if strings.TrimSpace(image) == "" {
		image = defaultAppImage
	}
	return dockerComposeStack{
		Name:           distributionName(loaded.Distribution.Spec),
		AppDir:         loaded.Root,
		Image:          image,
		ConnectorsPath: firstNonEmpty(strings.TrimSpace(connectorsPath), defaultConnectorsPath),
		AppRuntime: resolveAppRuntime(loaded, appRuntimeOptions{
			Provider:           provider,
			Model:              model,
			Effort:             effort,
			AllowPluginAuthEnv: allowPluginAuthEnv,
		}),
		Launch: loaded.Launch,
	}, nil
}

func dockerComposeContent(name, image, connectorsPath string, appRuntime appRuntimeOptions, launch distribution.LaunchConfig) string {
	service := strings.TrimSpace(name)
	if service == "" {
		service = "app"
	}
	appRuntime = appRuntime.withDefaults()
	service = composeServiceName(service)

	spec := composeFile{
		Services: map[string]composeService{
			service: {
				Image:       image,
				Command:     composeInlineStrings(appServeCommand(connectorsPath, appRuntime)),
				Environment: composeEnv(appRuntime, launch),
				DependsOn:   composeDependencyMap(launch),
				Restart:     "unless-stopped",
			},
		},
	}
	if composeUsesMySQL(launch) {
		spec.Services["mysql"] = mysqlComposeService()
	}
	if composeUsesNATS(launch) {
		spec.Services["nats"] = natsComposeService()
	}
	volumes := composeVolumes(launch)
	if len(volumes) > 0 {
		spec.Volumes = volumes
	}
	content, err := yaml.Marshal(spec)
	if err != nil {
		return ""
	}
	return string(content)
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]map[string]any `yaml:"volumes,omitempty"`
}

type composeService struct {
	Image       string                      `yaml:"image"`
	Command     composeInlineStrings        `yaml:"command,omitempty"`
	Environment map[string]string           `yaml:"environment,omitempty"`
	DependsOn   map[string]composeDependsOn `yaml:"depends_on,omitempty"`
	Restart     string                      `yaml:"restart,omitempty"`
	Volumes     []string                    `yaml:"volumes,omitempty"`
	Healthcheck *composeHealthcheck         `yaml:"healthcheck,omitempty"`
}

type composeDependsOn struct {
	Condition string `yaml:"condition"`
}

type composeHealthcheck struct {
	Test     composeInlineStrings `yaml:"test"`
	Interval string               `yaml:"interval"`
	Timeout  string               `yaml:"timeout"`
	Retries  int                  `yaml:"retries"`
}

type composeInlineStrings []string

func (values composeInlineStrings) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, value := range values {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
	return node, nil
}

func composeEnv(appRuntime appRuntimeOptions, launch distribution.LaunchConfig) map[string]string {
	env := map[string]string{}
	appRuntime = appRuntime.withDefaults()
	if strings.EqualFold(appRuntime.Provider, DefaultAppProvider) {
		env[openRouterAPIKeyEnv] = "${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is required}"
	}
	if composeUsesMySQL(launch) {
		name := strings.TrimSpace(launch.Data.Store.DSNEnv)
		if name == "" {
			name = defaultMySQLDSNEnv
		}
		env[name] = "agentruntime:agentruntime@tcp(mysql:3306)/agentruntime?parseTime=true&multiStatements=true"
	}
	if composeUsesNATS(launch) {
		name := strings.TrimSpace(launch.Events.Store.DSNEnv)
		if name == "" {
			name = defaultNATSDSNEnv
		}
		env[name] = "nats://nats:4222"
	}
	return env
}

func composeDependencyMap(launch distribution.LaunchConfig) map[string]composeDependsOn {
	deps := map[string]composeDependsOn{}
	if composeUsesMySQL(launch) {
		deps["mysql"] = composeDependsOn{Condition: "service_healthy"}
	}
	if composeUsesNATS(launch) {
		deps["nats"] = composeDependsOn{Condition: "service_healthy"}
	}
	if len(deps) == 0 {
		return nil
	}
	return deps
}

func mysqlComposeService() composeService {
	return composeService{
		Image: "mysql:8.4",
		Environment: map[string]string{
			"MYSQL_DATABASE":      "agentruntime",
			"MYSQL_PASSWORD":      "agentruntime",
			"MYSQL_ROOT_PASSWORD": "agentruntime-root",
			"MYSQL_USER":          "agentruntime",
		},
		Volumes: []string{"mysql-data:/var/lib/mysql"},
		Healthcheck: &composeHealthcheck{
			Test:     composeInlineStrings{"CMD", "mysqladmin", "ping", "-h", "localhost"},
			Interval: "5s",
			Timeout:  "5s",
			Retries:  20,
		},
	}
}

func natsComposeService() composeService {
	return composeService{
		Image:   "nats:2.11-alpine",
		Command: composeInlineStrings{"-js", "-sd", "/data", "-m", "8222"},
		Volumes: []string{"nats-data:/data"},
		Healthcheck: &composeHealthcheck{
			Test:     composeInlineStrings{"CMD-SHELL", "wget -q -O - http://127.0.0.1:8222/healthz | grep -q ok"},
			Interval: "5s",
			Timeout:  "5s",
			Retries:  20,
		},
	}
}

func composeVolumes(launch distribution.LaunchConfig) map[string]map[string]any {
	if !composeUsesMySQL(launch) && !composeUsesNATS(launch) {
		return nil
	}
	volumes := map[string]map[string]any{}
	if composeUsesMySQL(launch) {
		volumes["mysql-data"] = map[string]any{}
	}
	if composeUsesNATS(launch) {
		volumes["nats-data"] = map[string]any{}
	}
	return volumes
}

func composeUsesMySQL(launch distribution.LaunchConfig) bool {
	return strings.EqualFold(strings.TrimSpace(launch.Data.Store.Kind), "mysql")
}

func composeUsesNATS(launch distribution.LaunchConfig) bool {
	kind := strings.ToLower(strings.TrimSpace(launch.Events.Store.Kind))
	return kind == "nats" || kind == "jetstream" || kind == "nats-jetstream"
}

func composeServiceName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}
