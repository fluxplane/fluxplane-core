package workspace

import (
	"context"
	"fmt"
	"os"
	"strings"

	fpsystem "github.com/fluxplane/fluxplane-system"
)

var defaultProcessEnvKeys = []string{"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "GOCACHE"}
var hostForwardedProcessEnvKeys = []string{
	"SSH_AUTH_SOCK",
	"DISPLAY",
	"WAYLAND_DISPLAY",
	"XAUTHORITY",
	"XDG_RUNTIME_DIR",
	"XDG_SESSION_TYPE",
	"XDG_CURRENT_DESKTOP",
	"DESKTOP_SESSION",
	"DBUS_SESSION_BUS_ADDRESS",
	"PULSE_SERVER",
	"PIPEWIRE_REMOTE",
}

var processOverrideEnvKeys = map[string]bool{
	"CGO_ENABLED": true,
	"GOARCH":      true,
	"GOBIN":       true,
	"GOOS":        true,
	"GOPATH":      true,
}

// HostEnvironment implements Environment using an explicitly allowed env set.
type HostEnvironment struct {
	values map[string]string
}

// Lookup returns an allowed environment variable value for key.
func (e HostEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e.values[strings.TrimSpace(key)]
	return value, ok, nil
}

// ResolveExecutable resolves an executable using the environment PATH.
func (e HostEnvironment) ResolveExecutable(ctx context.Context, name string) (string, bool, error) {
	pathValue, ok, err := e.Lookup(ctx, "PATH")
	if err != nil || !ok {
		return "", false, err
	}
	return fpsystem.ResolveExecutableInPath(name, pathValue)
}

type WorkspaceEnvironment struct {
	root HostEnvironment
	sets map[string]map[string]string
}

type EnvFileSet = fpsystem.EnvFileSet

func LoadEnvFiles(root string, patterns []string) (EnvFileSet, error) {
	return fpsystem.LoadEnvFiles(root, patterns)
}

func NewEnvironment(workspace *HostWorkspace) (*WorkspaceEnvironment, error) {
	sets := map[string]map[string]string{}
	for _, root := range workspace.roots {
		values := defaultHostEnv()
		envFiles, err := fpsystem.LoadEnvFiles(root.root, root.envFiles)
		if err != nil {
			return nil, err
		}
		for key, value := range envFiles.Values {
			values[key] = value
		}
		for key := range values {
			if hostValue, ok := os.LookupEnv(key); ok {
				values[key] = hostValue
			}
		}
		for _, key := range hostForwardedProcessEnvKeys {
			if hostValue, ok := os.LookupEnv(key); ok {
				values[key] = hostValue
			}
		}
		sets[root.name] = values
	}
	rootValues := cloneEnvMap(sets[""])
	return &WorkspaceEnvironment{
		root: HostEnvironment{values: rootValues},
		sets: sets,
	}, nil
}

func defaultHostEnv() map[string]string {
	out := map[string]string{}
	for _, key := range defaultProcessEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			out[key] = value
		}
	}
	for _, key := range hostForwardedProcessEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			out[key] = value
		}
	}
	return out
}

func (e *WorkspaceEnvironment) Lookup(ctx context.Context, key string) (string, bool, error) {
	if e == nil {
		return "", false, nil
	}
	return e.root.Lookup(ctx, key)
}

func (e *WorkspaceEnvironment) processEnv(root workspaceRoot, overrides []string) ([]string, error) {
	values := cloneEnvMap(e.valuesForRoot(root))
	for _, entry := range overrides {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("process env entry %q is invalid", entry)
		}
		if strings.ContainsAny(value, "\x00\n\r") {
			return nil, fmt.Errorf("process env value for %s contains unsupported control character", key)
		}
		if _, allowed := values[key]; !allowed && !processOverrideEnvKeys[key] {
			return nil, fmt.Errorf("process env key %q is not allowed", key)
		}
		values[key] = value
	}

	return fpsystem.FormatEnv(values), nil
}

// ResolveExecutable resolves an executable using the workspace process PATH.
func (e *WorkspaceEnvironment) ResolveExecutable(ctx context.Context, name string) (string, bool, error) {
	pathValue, ok, err := e.Lookup(ctx, "PATH")
	if err != nil || !ok {
		return "", false, err
	}
	return fpsystem.ResolveExecutableInPath(name, pathValue)
}

func (e *WorkspaceEnvironment) valuesForRoot(root workspaceRoot) map[string]string {
	if e == nil {
		return defaultHostEnv()
	}
	if values, ok := e.sets[root.name]; ok {
		return values
	}
	return e.sets[""]
}

func cloneEnvMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func trimStrings(input []string) []string                 { return fpsystem.TrimStrings(input) }
func parseEnvFile(path string) (map[string]string, error) { return fpsystem.ParseEnvFile(path) }
func resolveExecutableInPath(name, pathValue string) (string, bool, error) {
	return fpsystem.ResolveExecutableInPath(name, pathValue)
}
func unescapeDoubleQuotedEnv(value string) (string, error) {
	return fpsystem.UnescapeDoubleQuotedEnv(value)
}
func resolveEnvFiles(root string, patterns []string) ([]string, error) {
	return fpsystem.ResolveEnvFiles(root, patterns)
}
func envFilePattern(root, pattern string) (string, error) {
	return fpsystem.EnvFilePattern(root, pattern)
}
func staticPatternDir(pattern string) string { return fpsystem.StaticPatternDir(pattern) }
