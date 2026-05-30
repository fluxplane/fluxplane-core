package workspace

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
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
	return resolveExecutableInPath(name, pathValue)
}

type WorkspaceEnvironment struct {
	root HostEnvironment
	sets map[string]map[string]string
}

// EnvFileSet contains env values loaded from workspace env files.
type EnvFileSet struct {
	Files  []string
	Values map[string]string
}

// LoadEnvFiles resolves and parses env files relative to root. Later files
// override earlier values, matching workspace runtime environment semantics.
func LoadEnvFiles(root string, patterns []string) (EnvFileSet, error) {
	files, err := resolveEnvFiles(root, patterns)
	if err != nil {
		return EnvFileSet{}, err
	}
	values := map[string]string{}
	for _, file := range files {
		loaded, err := parseEnvFile(file)
		if err != nil {
			return EnvFileSet{}, err
		}
		for key, value := range loaded {
			values[key] = value
		}
	}
	return EnvFileSet{Files: files, Values: values}, nil
}

func NewEnvironment(workspace *HostWorkspace) (*WorkspaceEnvironment, error) {
	sets := map[string]map[string]string{}
	for _, root := range workspace.roots {
		values := defaultHostEnv()
		envFiles, err := LoadEnvFiles(root.root, root.envFiles)
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

	return formatEnv(values), nil
}

// ResolveExecutable resolves an executable using the workspace process PATH.
func (e *WorkspaceEnvironment) ResolveExecutable(ctx context.Context, name string) (string, bool, error) {
	pathValue, ok, err := e.Lookup(ctx, "PATH")
	if err != nil || !ok {
		return "", false, err
	}
	return resolveExecutableInPath(name, pathValue)
}

func resolveExecutableInPath(name, pathValue string) (string, bool, error) {
	if strings.TrimSpace(name) == "" || strings.ContainsRune(name, filepath.Separator) {
		return "", false, nil
	}
	for _, dir := range filepath.SplitList(pathValue) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode().Perm()&0111 == 0 {
			continue
		}
		return candidate, true, nil
	}
	return "", false, nil
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

func resolveEnvFiles(root string, patterns []string) ([]string, error) {
	var files []string
	for _, raw := range patterns {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			continue
		}
		absPattern, err := envFilePattern(root, pattern)
		if err != nil {
			return nil, err
		}
		if !hasGlobMeta(absPattern) {
			if resolved, ok, err := resolveEnvFile(root, absPattern); err != nil {
				return nil, err
			} else if ok {
				files = append(files, resolved)
			}
			continue
		}
		matches, err := filepath.Glob(absPattern)
		if err != nil {
			return nil, fmt.Errorf("env file glob %q: %w", pattern, err)
		}
		sort.Strings(matches)
		for _, match := range matches {
			resolved, ok, err := resolveEnvFile(root, match)
			if err != nil {
				return nil, err
			}
			if ok {
				files = append(files, resolved)
			}
		}
	}
	return files, nil
}

func envFilePattern(root, pattern string) (string, error) {
	if filepath.IsAbs(pattern) {
		dir := staticPatternDir(pattern)
		if err := pathWithin(root, dir); err != nil {
			return "", fmt.Errorf("env file %q escapes workspace root", pattern)
		}
		return filepath.Clean(pattern), nil
	}
	clean := filepath.Clean(filepath.FromSlash(pattern))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("env file %q escapes workspace root", pattern)
	}
	return filepath.Join(root, clean), nil
}

func staticPatternDir(pattern string) string {
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

func resolveEnvFile(root, path string) (string, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("env file %q: %w", path, err)
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("env file %q is a directory", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false, fmt.Errorf("env file %q: %w", path, err)
	}
	if err := pathWithin(root, real); err != nil {
		return "", false, fmt.Errorf("env file %q escapes workspace root", path)
	}
	return real, true, nil
}

func parseEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("env file %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimSuffix(scanner.Text(), "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, rawValue, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validEnvKey(key) {
			return nil, fmt.Errorf("env file %q line %d has invalid key", path, lineNo)
		}
		value, err := parseEnvValue(strings.TrimSpace(rawValue))
		if err != nil {
			return nil, fmt.Errorf("env file %q line %d: %w", path, lineNo, err)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("env file %q: %w", path, err)
	}
	return values, nil
}

func parseEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	switch raw[0] {
	case '\'':
		if len(raw) < 2 || raw[len(raw)-1] != '\'' {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return raw[1 : len(raw)-1], nil
	case '"':
		if len(raw) < 2 || raw[len(raw)-1] != '"' {
			return "", fmt.Errorf("unterminated double-quoted value")
		}
		return unescapeDoubleQuotedEnv(raw[1 : len(raw)-1])
	default:
		return strings.TrimSpace(stripEnvComment(raw)), nil
	}
}

func unescapeDoubleQuotedEnv(value string) (string, error) {
	var out strings.Builder
	escaped := false
	for _, r := range value {
		if escaped {
			switch r {
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			case '\\', '"':
				out.WriteRune(r)
			default:
				out.WriteByte('\\')
				out.WriteRune(r)
			}
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		out.WriteRune(r)
	}
	if escaped {
		return "", fmt.Errorf("unterminated escape")
	}
	return out.String(), nil
}

func stripEnvComment(value string) string {
	for i, r := range value {
		if r == '#' && (i == 0 || unicode.IsSpace(rune(value[i-1]))) {
			return strings.TrimSpace(value[:i])
		}
	}
	return value
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func formatEnv(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
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

func trimStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	out := make([]string, 0, len(input))
	for _, value := range input {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func hasGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
