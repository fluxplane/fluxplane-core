package golangplugin

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/core/operation"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const defaultToolchainTimeout = 30 * time.Second

var curatedGoEnvVars = []string{
	"GOVERSION",
	"GOOS",
	"GOARCH",
	"GOHOSTOS",
	"GOHOSTARCH",
	"CGO_ENABLED",
	"GOMOD",
	"GOWORK",
	"GOROOT",
	"GOPATH",
	"GOBIN",
	"GOMODCACHE",
	"GOCACHE",
	"GOTOOLDIR",
	"GO111MODULE",
	"GOTOOLCHAIN",
	"GOFLAGS",
	"GOPROXY",
	"GOSUMDB",
	"GOINSECURE",
	"GOPRIVATE",
	"GONOPROXY",
	"GONOSUMDB",
}

func (p Plugin) goInfo() operationruntime.TypedResultHandler[language.GoInfoQuery, operation.Rendered] {
	return func(ctx operation.Context, req language.GoInfoQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_info_input", err.Error(), nil)
		}
		versionRun, err := p.runGoTool(ctx, req.Path, []string{"version"}, req.MaxBytes, defaultToolchainTimeout)
		if err != nil {
			return operation.Failed("go_info_failed", err.Error(), processData(versionRun))
		}
		envRun, err := p.runGoTool(ctx, req.Path, append([]string{"env", "-json"}, curatedGoEnvVars...), req.MaxBytes, defaultToolchainTimeout)
		if err != nil {
			return operation.Failed("go_info_failed", err.Error(), processData(envRun))
		}
		env, diagnostics := parseGoEnvOutput(envRun.Stdout)
		result := language.GoInfoResult{
			Version: map[string]string{
				"go":        strings.TrimSpace(versionRun.Stdout),
				"goversion": env["GOVERSION"],
			},
			Target: map[string]string{
				"goos":        env["GOOS"],
				"goarch":      env["GOARCH"],
				"gohostos":    env["GOHOSTOS"],
				"gohostarch":  env["GOHOSTARCH"],
				"cgo_enabled": env["CGO_ENABLED"],
			},
			Modules: map[string]string{
				"go111module": env["GO111MODULE"],
				"gotoolchain": env["GOTOOLCHAIN"],
				"goflags":     env["GOFLAGS"],
			},
			Network: map[string]any{
				"goproxy":    parseGoProxy(env["GOPROXY"]),
				"gosumdb":    env["GOSUMDB"],
				"goinsecure": splitGoList(env["GOINSECURE"]),
			},
			Diagnostics: diagnostics,
		}
		if boolDefault(req.IncludePaths, true) {
			result.Workspace = map[string]string{"path": cleanRel(req.Path), "gomod": env["GOMOD"], "gowork": env["GOWORK"]}
			result.Paths = map[string]string{
				"goroot":     env["GOROOT"],
				"gopath":     env["GOPATH"],
				"gobin":      env["GOBIN"],
				"gomodcache": env["GOMODCACHE"],
				"gocache":    env["GOCACHE"],
				"gotooldir":  env["GOTOOLDIR"],
			}
		}
		if boolDefault(req.IncludePrivate, true) {
			result.Private = map[string][]string{
				"goprivate": splitGoList(env["GOPRIVATE"]),
				"gonoproxy": splitGoList(env["GONOPROXY"]),
				"gonosumdb": splitGoList(env["GONOSUMDB"]),
			}
		}
		if req.IncludeRawEnv {
			result.RawEnv = selectGoEnvValues(env, curatedGoEnvVars)
		}
		lines := []string{"Go info"}
		if version := result.Version["go"]; version != "" {
			lines = append(lines, "- version: "+version)
		}
		if target := joinNonEmpty("/", result.Target["goos"], result.Target["goarch"]); target != "" {
			lines = append(lines, "- target: "+target)
		}
		if result.Workspace != nil {
			lines = append(lines, "- module: "+emptyDefault(result.Workspace["gomod"], "(none)"))
		}
		if proxy, ok := result.Network["goproxy"].(language.GoProxyConfig); ok && proxy.Raw != "" {
			lines = append(lines, "- proxy: "+proxy.Raw)
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"info": result, "diagnostics": result.Diagnostics}})
	}
}

func (p Plugin) goEnv() operationruntime.TypedResultHandler[language.GoEnvQuery, operation.Rendered] {
	return func(ctx operation.Context, req language.GoEnvQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_env_input", err.Error(), nil)
		}
		if req.Changed && len(req.Vars) > 0 {
			return operation.Failed("invalid_go_env_input", "vars cannot be combined with changed", nil)
		}
		args := []string{"env", "-json"}
		if req.Changed {
			args = append(args, "-changed")
		}
		vars := append([]string(nil), req.Vars...)
		if len(vars) == 0 && !req.All && !req.Changed {
			vars = curatedGoEnvVars
		}
		if len(vars) > 0 {
			if err := validateGoEnvVars(vars); err != nil {
				return operation.Failed("invalid_go_env_input", err.Error(), nil)
			}
			args = append(args, vars...)
		}
		run, err := p.runGoTool(ctx, req.Path, args, req.MaxBytes, defaultToolchainTimeout)
		if err != nil {
			return operation.Failed("go_env_failed", err.Error(), processData(run))
		}
		values, diagnostics := parseGoEnvOutput(run.Stdout)
		if boolDefault(req.Redact, true) {
			values = redactGoEnv(values)
		}
		result := language.GoEnvResult{Values: values, All: req.All, Changed: req.Changed, Diagnostics: diagnostics}
		lines := []string{fmt.Sprintf("Go env: %d values", len(values))}
		for _, key := range sortedMapKeys(values) {
			lines = append(lines, fmt.Sprintf("- %s=%s", key, values[key]))
			if len(lines) >= 12 {
				break
			}
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"env": result, "values": result.Values, "diagnostics": result.Diagnostics}})
	}
}

func (p Plugin) goVersion() operationruntime.TypedResultHandler[language.GoVersionQuery, operation.Rendered] {
	return func(ctx operation.Context, req language.GoVersionQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_version_input", err.Error(), nil)
		}
		args := []string{"version"}
		if req.ModuleInfo || req.JSON {
			args = append(args, "-m", "-json")
		}
		if req.Verbose {
			args = append(args, "-v")
		}
		files, err := workspaceArgPaths(req.Files)
		if err != nil {
			return operation.Failed("invalid_go_version_input", err.Error(), nil)
		}
		if req.MaxResults > 0 && len(files) > maxResults(req.MaxResults) {
			files = files[:maxResults(req.MaxResults)]
		}
		args = append(args, files...)
		run, err := p.runGoTool(ctx, req.Path, args, req.MaxBytes, defaultToolchainTimeout)
		if err != nil {
			return operation.Failed("go_version_failed", err.Error(), processData(run))
		}
		result := language.GoVersionResult{}
		if req.ModuleInfo || req.JSON {
			result.Records, result.Diagnostics = parseGoVersionJSON(run.Stdout)
		} else {
			result.Version = strings.TrimSpace(run.Stdout)
			if len(files) > 0 {
				result.Records = parseGoVersionLines(run.Stdout)
			}
		}
		lines := []string{"Go version"}
		if result.Version != "" {
			lines = append(lines, "- "+result.Version)
		}
		for _, record := range result.Records {
			line := "- " + emptyDefault(record.Path, "go")
			if record.Version != "" {
				line += ": " + record.Version
			}
			lines = append(lines, line)
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"version": result, "records": result.Records, "diagnostics": result.Diagnostics}})
	}
}

func (p Plugin) runGoTool(ctx operation.Context, workdir string, args []string, maxBytesValue int, timeout time.Duration) (system.ProcessResult, error) {
	if p.system == nil || p.system.Process() == nil {
		return system.ProcessResult{}, fmt.Errorf("golangplugin: system process is nil")
	}
	rel, err := cleanOptionalWorkspacePath(workdir)
	if err != nil {
		return system.ProcessResult{}, err
	}
	limit := maxBytes(maxBytesValue)
	return p.system.Process().Run(ctx, system.ProcessRequest{
		Command:   "go",
		Args:      append([]string(nil), args...),
		Workdir:   rel,
		Env:       system.DefaultProcessEnv(),
		Timeout:   timeout,
		MaxStdout: limit,
		MaxStderr: limit,
	})
}

func goInfoIntent(_ operation.Context, req language.GoInfoQuery) (operation.IntentSet, error) {
	return goToolIntentSet(req.Path, []string{"version"}, append([]string{"env", "-json"}, curatedGoEnvVars...))
}

func goEnvIntent(_ operation.Context, req language.GoEnvQuery) (operation.IntentSet, error) {
	args := []string{"env", "-json"}
	if req.Changed {
		args = append(args, "-changed")
	}
	vars := append([]string(nil), req.Vars...)
	if len(vars) == 0 && !req.All && !req.Changed {
		vars = curatedGoEnvVars
	}
	args = append(args, vars...)
	return goToolIntentSet(req.Path, args)
}

func goVersionIntent(_ operation.Context, req language.GoVersionQuery) (operation.IntentSet, error) {
	args := []string{"version"}
	if req.ModuleInfo || req.JSON {
		args = append(args, "-m", "-json")
	}
	if req.Verbose {
		args = append(args, "-v")
	}
	files, err := workspaceArgPaths(req.Files)
	if err != nil {
		return operation.IntentSet{}, err
	}
	args = append(args, files...)
	return goToolIntentSet(req.Path, args)
}

func goToolIntentSet(workdir string, commands ...[]string) (operation.IntentSet, error) {
	if _, err := cleanOptionalWorkspacePath(workdir); err != nil {
		return operation.IntentSet{}, err
	}
	ops := make([]operation.IntentOperation, 0, len(commands)+1)
	for _, args := range commands {
		ops = append(ops, goProcessIntent(args...))
	}
	if cleanRel(workdir) != "" {
		ops = append(ops, operation.IntentOperation{
			Behavior:  operation.IntentFilesystemRead,
			Target:    operation.PathTarget{Path: operation.Path(cleanRel(workdir))},
			Role:      operation.IntentRoleReadTarget,
			Certainty: operation.IntentCertain,
		})
	}
	return operation.IntentSet{Operations: ops}, nil
}

func goProcessIntent(args ...string) operation.IntentOperation {
	out := make([]operation.Argument, 0, len(args))
	for _, arg := range args {
		out = append(out, operation.Argument(arg))
	}
	return operation.IntentOperation{
		Behavior:  operation.IntentCommandExecution,
		Target:    operation.ProcessTarget{Command: operation.Command("go"), Args: out},
		Role:      operation.IntentRoleProcessCommand,
		Certainty: operation.IntentCertain,
	}
}

func parseGoEnvOutput(raw string) (map[string]string, []language.Diagnostic) {
	values := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return values, nil
	}
	if err := json.Unmarshal([]byte(raw), &values); err == nil {
		return values, nil
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return values, []language.Diagnostic{{Severity: "warning", Code: "go_env_parse_failed", Message: err.Error()}}
	}
	for key, value := range generic {
		values[key] = fmt.Sprint(value)
	}
	return values, nil
}

func parseGoProxy(raw string) language.GoProxyConfig {
	cfg := language.GoProxyConfig{Raw: strings.TrimSpace(raw)}
	if cfg.Raw == "" {
		return cfg
	}
	for _, group := range strings.Split(cfg.Raw, ",") {
		var entries []string
		for _, entry := range strings.Split(group, "|") {
			if trimmed := strings.TrimSpace(entry); trimmed != "" {
				entries = append(entries, trimmed)
			}
		}
		if len(entries) > 0 {
			cfg.Groups = append(cfg.Groups, language.GoProxyGroup{Entries: entries})
		}
	}
	return cfg
}

func splitGoList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func selectGoEnvValues(values map[string]string, keys []string) map[string]string {
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := values[key]; ok {
			out[key] = value
		}
	}
	return out
}

func redactGoEnv(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") {
			out[key] = "[redacted]"
			continue
		}
		out[key] = value
	}
	return out
}

func validateGoEnvVars(vars []string) error {
	for _, name := range vars {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("env var name is empty")
		}
		for _, r := range name {
			if r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
				return fmt.Errorf("invalid go env var %q", name)
			}
		}
	}
	return nil
}

func parseGoVersionLines(raw string) []language.GoVersionRecord {
	var records []language.GoVersionRecord
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			records = append(records, language.GoVersionRecord{Path: strings.TrimSuffix(fields[0], ":"), Version: fields[1], Raw: line})
		}
	}
	return records
}

func parseGoVersionJSON(raw string) ([]language.GoVersionRecord, []language.Diagnostic) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	var records []language.GoVersionRecord
	for {
		var obj map[string]any
		if err := decoder.Decode(&obj); err != nil {
			if err == io.EOF {
				break
			}
			return records, []language.Diagnostic{{Severity: "warning", Code: "go_version_parse_failed", Message: err.Error()}}
		}
		record := language.GoVersionRecord{BuildInfo: obj}
		if pathValue, ok := obj["Path"].(string); ok {
			record.Path = pathValue
		}
		if version, ok := obj["GoVersion"].(string); ok {
			record.Version = version
		}
		records = append(records, record)
	}
	return records, nil
}

func workspaceArgPaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		rel, err := cleanRequiredWorkspacePath(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, nil
}

func cleanOptionalWorkspacePath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "." {
		return "", nil
	}
	return cleanRequiredWorkspacePath(raw)
}

func cleanRequiredWorkspacePath(raw string) (string, error) {
	rel := cleanRel(raw)
	if rel == "" {
		return "", fmt.Errorf("workspace path is empty")
	}
	if path.IsAbs(strings.ReplaceAll(raw, "\\", "/")) || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("workspace path %q escapes the workspace", raw)
	}
	return rel, nil
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func joinNonEmpty(sep string, values ...string) string {
	var out []string
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return strings.Join(out, sep)
}

func emptyDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func processData(result system.ProcessResult) map[string]any {
	return map[string]any{
		"command":          result.Command,
		"args":             result.Args,
		"workdir":          result.Workdir,
		"stdout":           result.Stdout,
		"stderr":           result.Stderr,
		"exit_code":        result.ExitCode,
		"timed_out":        result.TimedOut,
		"stdout_truncated": result.StdoutTruncated,
		"stderr_truncated": result.StderrTruncated,
	}
}
