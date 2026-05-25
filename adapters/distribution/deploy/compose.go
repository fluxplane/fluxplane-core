package deploy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	distlocal "github.com/fluxplane/fluxplane-core/adapters/distribution/local"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	"gopkg.in/yaml.v3"
)

type dockerComposeStack struct {
	Name       string
	AppDir     string
	Image      string
	AuthPath   string
	AppRuntime appRuntimeOptions
	Launch     distribution.LaunchConfig
}

// ComposeOptions configures Docker Compose artifact generation.
type ComposeOptions struct {
	AppDir   string
	Profile  string
	Profiles []string
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
	Profile            string
	Profiles           []string
	TempDir            string
	Image              string
	BaseImage          string
	AuthPath           string
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
	app, err := BuildApp(ctx, AppBuildOptions{
		AppDir:             opts.AppDir,
		Profile:            opts.Profile,
		Profiles:           opts.Profiles,
		Targets:            []string{"dockerfile", "docker-compose", "docker-image"},
		Image:              opts.Image,
		DryRun:             opts.DryRun,
		Force:              opts.Force,
		BaseImage:          baseImage,
		AuthPath:           opts.AuthPath,
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
	stack, err := dockerComposeStackFor(ctx, app.AppDir, opts.Profile, opts.Profiles, firstTag(app.Tags), opts.AuthPath, opts.Provider, opts.Model, opts.Effort, opts.AllowPluginAuthEnv)
	if err != nil {
		return ComposeDeployResult{}, err
	}
	envFile, err := ensureComposeRuntimeEnvFile(app.AppDir, stack.Launch, opts.DryRun, out)
	if err != nil {
		return ComposeDeployResult{}, err
	}
	command := dockerComposeUpCommand(app.Compose, opts.Detach, envFile)
	result := ComposeDeployResult{
		BaseImage: BaseImageResult{Tags: []string{baseImage}},
		AppBuild:  app,
		Command:   command,
		DryRun:    opts.DryRun,
	}
	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
		return result, nil
	}
	if opts.dockerClient == nil {
		if err := runner.Run(ctx, app.AppDir, command[0], command[1:], out, errOut); err != nil {
			return ComposeDeployResult{}, err
		}
		return result, nil
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
	envFile, err := composeRuntimeEnvFileForTeardown(loaded.Root, loaded.Launch, opts.DryRun, out)
	if err != nil {
		return ComposeUndeployResult{}, err
	}
	command := dockerComposeDownCommand(composePath, envFile, opts.Volumes)
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
	if opts.dockerClient == nil {
		if err := runner.Run(ctx, loaded.Root, command[0], command[1:], out, errOut); err != nil {
			return ComposeUndeployResult{}, err
		}
		return result, nil
	}
	stack, err := dockerComposeStackFor(ctx, loaded.Root, "", nil, "", "", "", "", "", false)
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
	loaded, err := distlocal.LoadWithOptions(ctx, appDir, distlocal.LoadOptions{Profile: opts.Profile, Profiles: opts.Profiles})
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
		Profiles: selectedRuntimeProfiles(opts.Profile, opts.Profiles),
	})
	composePath := filepath.Join(loaded.Root, "docker-compose.yaml")
	content, err := dockerComposeContent(loaded.Root, filepath.Dir(composePath), loaded.Distribution.Spec.Name, image, defaultAuthPath, appRuntime, loaded.Launch)
	if err != nil {
		return ComposeResult{}, err
	}
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

func dockerComposeStackFor(ctx context.Context, appDir, profile string, profiles []string, image, authPath, provider, model, effort string, allowPluginAuthEnv bool) (dockerComposeStack, error) {
	loaded, err := distlocal.LoadWithOptions(ctx, appDir, distlocal.LoadOptions{Profile: profile, Profiles: profiles})
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
		Name:     distributionName(loaded.Distribution.Spec),
		AppDir:   loaded.Root,
		Image:    image,
		AuthPath: firstNonEmpty(strings.TrimSpace(authPath), defaultAuthPath),
		AppRuntime: resolveAppRuntime(loaded, appRuntimeOptions{
			Provider:           provider,
			Model:              model,
			Effort:             effort,
			AllowPluginAuthEnv: allowPluginAuthEnv,
			Profiles:           selectedRuntimeProfiles(profile, profiles),
		}),
		Launch: loaded.Launch,
	}, nil
}

func dockerComposeContent(appRoot, composeDir, name, image, authPath string, appRuntime appRuntimeOptions, launch distribution.LaunchConfig) (string, error) {
	service := strings.TrimSpace(name)
	if service == "" {
		service = "app"
	}
	appRuntime = appRuntime.withDefaults()
	service = composeServiceName(service)
	envFiles, err := composeEnvFiles(appRoot, composeDir, launch)
	if err != nil {
		return "", err
	}

	spec := composeFile{
		Services: map[string]composeService{
			service: {
				Image:       image,
				Command:     composeInlineStrings(appServeCommand(authPath, appRuntime)),
				EnvFile:     envFiles,
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
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(spec); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]map[string]any `yaml:"volumes,omitempty"`
}

type composeService struct {
	Image       string                      `yaml:"image"`
	Command     composeInlineStrings        `yaml:"command,omitempty"`
	EnvFile     []string                    `yaml:"env_file,omitempty"`
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

func composeEnvFiles(appRoot, composeDir string, launch distribution.LaunchConfig) ([]string, error) {
	return resolveComposeEnvFiles(appRoot, composeDir, launch.Workspace.EnvFiles)
}

func resolveComposeEnvFiles(appRoot, composeDir string, patterns []string) ([]string, error) {
	patterns = cleanStrings(patterns)
	if len(patterns) == 0 {
		return nil, nil
	}
	root, err := filepath.Abs(firstNonEmpty(strings.TrimSpace(appRoot), "."))
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	base, err := filepath.Abs(firstNonEmpty(strings.TrimSpace(composeDir), root))
	if err != nil {
		return nil, err
	}
	base = filepath.Clean(base)
	var out []string
	seen := map[string]bool{}
	for _, pattern := range patterns {
		absPattern, err := composeEnvFilePattern(root, pattern)
		if err != nil {
			return nil, err
		}
		var matches []string
		if strings.ContainsAny(absPattern, "*?[") {
			matches, err = filepath.Glob(absPattern)
			if err != nil {
				return nil, fmt.Errorf("env file glob %q: %w", pattern, err)
			}
			sort.Strings(matches)
		} else {
			matches = []string{absPattern}
		}
		for _, match := range matches {
			rel, ok, err := resolveComposeEnvFile(root, base, match)
			if err != nil {
				return nil, err
			}
			if !ok || seen[rel] {
				continue
			}
			seen[rel] = true
			out = append(out, rel)
		}
	}
	return out, nil
}

func composeEnvFilePattern(root, pattern string) (string, error) {
	if filepath.IsAbs(pattern) {
		clean := filepath.Clean(pattern)
		if err := composePathWithin(root, composeStaticPatternDir(clean)); err != nil {
			return "", fmt.Errorf("env file %q escapes workspace root", pattern)
		}
		return clean, nil
	}
	clean := filepath.Clean(filepath.FromSlash(pattern))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("env file %q escapes workspace root", pattern)
	}
	return filepath.Join(root, clean), nil
}

func composeStaticPatternDir(pattern string) string {
	idx := strings.IndexAny(pattern, "*?[")
	if idx < 0 {
		return filepath.Dir(filepath.Clean(pattern))
	}
	prefix := pattern[:idx]
	dir := filepath.Dir(prefix)
	if dir == "." || dir == "" {
		dir = string(os.PathSeparator)
	}
	return filepath.Clean(dir)
}

func resolveComposeEnvFile(root, base, filename string) (string, bool, error) {
	info, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("env file %q: %w", filename, err)
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("env file %q is a directory", filename)
	}
	real, err := filepath.EvalSymlinks(filename)
	if err != nil {
		return "", false, fmt.Errorf("env file %q: %w", filename, err)
	}
	if err := composePathWithin(root, real); err != nil {
		return "", false, fmt.Errorf("env file %q escapes workspace root", filename)
	}
	rel, err := filepath.Rel(base, filename)
	if err != nil {
		return "", false, err
	}
	return filepath.ToSlash(rel), true, nil
}

func composePathWithin(root, candidate string) error {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if rel == "." || rel == "" {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes workspace root")
	}
	return nil
}

func composeEnv(_ appRuntimeOptions, launch distribution.LaunchConfig) map[string]string {
	env := map[string]string{}
	if composeUsesMySQL(launch) {
		name := strings.TrimSpace(launch.Data.Store.DSNEnv)
		if name == "" {
			name = defaultMySQLDSNEnv
		}
		env[name] = "${" + name + ":?" + name + " is required}"
	}
	if composeUsesNATS(launch) {
		name := strings.TrimSpace(launch.Events.Store.DSNEnv)
		if name == "" {
			name = defaultNATSDSNEnv
		}
		env[name] = "${" + name + ":?" + name + " is required}"
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
		Image:       "mysql:8.4",
		Environment: mysqlComposeEnvironment(),
		Volumes:     []string{"mysql-data:/var/lib/mysql"},
		Healthcheck: &composeHealthcheck{
			Test:     composeInlineStrings{"CMD", "mysqladmin", "ping", "-h", "localhost"},
			Interval: "5s",
			Timeout:  "5s",
			Retries:  20,
		},
	}
}

func mysqlComposeEnvironment() map[string]string {
	return map[string]string{
		"MYSQL_DATABASE":      "${MYSQL_DATABASE:?MYSQL_DATABASE is required}",
		"MYSQL_PASSWORD":      "${MYSQL_PASSWORD:?MYSQL_PASSWORD is required}",
		"MYSQL_ROOT_PASSWORD": "${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}",
		"MYSQL_USER":          "${MYSQL_USER:?MYSQL_USER is required}",
	}
}

func mysqlDockerEnvironment(runtimeEnv map[string]string) map[string]string {
	return map[string]string{
		"MYSQL_DATABASE":      runtimeEnv["MYSQL_DATABASE"],
		"MYSQL_PASSWORD":      runtimeEnv["MYSQL_PASSWORD"],
		"MYSQL_ROOT_PASSWORD": runtimeEnv["MYSQL_ROOT_PASSWORD"],
		"MYSQL_USER":          runtimeEnv["MYSQL_USER"],
	}
}

func ensureComposeRuntimeEnvFile(appRoot string, launch distribution.LaunchConfig, dryRun bool, out io.Writer) (string, error) {
	if !composeUsesMySQL(launch) && !composeUsesNATS(launch) {
		return "", nil
	}
	filename := filepath.Join(appRoot, dockerComposeRuntimeEnvFile)
	if dryRun {
		_, _ = fmt.Fprintf(out, "write=%s\n", filename)
		return filename, nil
	}
	existing := map[string]string{}
	if env, err := readEnvFile(filename); err == nil {
		existing = env
	} else if !os.IsNotExist(err) {
		return "", err
	}
	env, changed, err := composeRuntimeEnvWithDefaults(launch, existing)
	if err != nil {
		return "", err
	}
	if changed {
		if err := maybeWriteFile(filename, envFileContent(env), 0o600, false, true, out); err != nil {
			return "", err
		}
	}
	if err := ensureGitignoreEntry(appRoot, ".deploy/"); err != nil {
		return "", err
	}
	return filename, nil
}

func composeRuntimeEnvFileForTeardown(appRoot string, launch distribution.LaunchConfig, dryRun bool, out io.Writer) (string, error) {
	filename := filepath.Join(appRoot, dockerComposeRuntimeEnvFile)
	if composeUsesMySQL(launch) || composeUsesNATS(launch) {
		return ensureComposeRuntimeEnvFile(appRoot, launch, dryRun, out)
	}
	if _, err := os.Stat(filename); err == nil {
		return filename, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return "", nil
}

func composeRuntimeEnvForStack(stack dockerComposeStack) (map[string]string, error) {
	if !composeUsesMySQL(stack.Launch) && !composeUsesNATS(stack.Launch) {
		return nil, nil
	}
	existing := map[string]string{}
	if strings.TrimSpace(stack.AppDir) != "" {
		filename := filepath.Join(stack.AppDir, dockerComposeRuntimeEnvFile)
		env, err := readEnvFile(filename)
		if err == nil {
			existing = env
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	env, _, err := composeRuntimeEnvWithDefaults(stack.Launch, existing)
	return env, err
}

func composeRuntimeEnvWithDefaults(launch distribution.LaunchConfig, existing map[string]string) (map[string]string, bool, error) {
	env := map[string]string{}
	for key, value := range existing {
		env[key] = value
	}
	changed := false
	ensure := func(key, value string) {
		if strings.TrimSpace(env[key]) != "" {
			return
		}
		env[key] = value
		changed = true
	}
	if composeUsesMySQL(launch) {
		ensure("MYSQL_DATABASE", "fluxplane")
		ensure("MYSQL_USER", "fluxplane")
		if strings.TrimSpace(env["MYSQL_PASSWORD"]) == "" {
			password, err := randomComposeSecret()
			if err != nil {
				return nil, false, err
			}
			ensure("MYSQL_PASSWORD", password)
		}
		if strings.TrimSpace(env["MYSQL_ROOT_PASSWORD"]) == "" {
			rootPassword, err := randomComposeSecret()
			if err != nil {
				return nil, false, err
			}
			ensure("MYSQL_ROOT_PASSWORD", rootPassword)
		}
		name := strings.TrimSpace(launch.Data.Store.DSNEnv)
		if name == "" {
			name = defaultMySQLDSNEnv
		}
		ensure(name, fmt.Sprintf("%s:%s@tcp(mysql:3306)/%s?parseTime=true&multiStatements=true", env["MYSQL_USER"], env["MYSQL_PASSWORD"], env["MYSQL_DATABASE"]))
	}
	if composeUsesNATS(launch) {
		name := strings.TrimSpace(launch.Events.Store.DSNEnv)
		if name == "" {
			name = defaultNATSDSNEnv
		}
		ensure(name, "nats://nats:4222")
	}
	return env, changed, nil
}

func readEnvFile(filename string) (map[string]string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			env[key] = value
		}
	}
	return env, nil
}

func randomComposeSecret() (string, error) {
	var data [18]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("distribution deploy: generate compose runtime secret: %w", err)
	}
	return hex.EncodeToString(data[:]), nil
}

func envFileContent(env map[string]string) string {
	var b strings.Builder
	for _, key := range sortedKeys(env) {
		_, _ = fmt.Fprintf(&b, "%s=%s\n", key, env[key])
	}
	return b.String()
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
