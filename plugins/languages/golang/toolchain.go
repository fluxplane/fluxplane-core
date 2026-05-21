package golang

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fluxplane/engine/core/language"
	"github.com/fluxplane/engine/core/language/golang"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/testrun"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
	"github.com/fluxplane/engine/runtime/system"
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

func (p Plugin) goInfo() operationruntime.TypedResultHandler[golang.GoInfoQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoInfoQuery) operation.Result {
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
		result := golang.GoInfoResult{
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
		if proxy, ok := result.Network["goproxy"].(golang.GoProxyConfig); ok && proxy.Raw != "" {
			lines = append(lines, "- proxy: "+proxy.Raw)
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"info": result, "diagnostics": result.Diagnostics}})
	}
}

func (p Plugin) goEnv() operationruntime.TypedResultHandler[golang.GoEnvQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoEnvQuery) operation.Result {
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
		result := golang.GoEnvResult{Values: values, All: req.All, Changed: req.Changed, Diagnostics: diagnostics}
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

func (p Plugin) goVersion() operationruntime.TypedResultHandler[golang.GoVersionQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoVersionQuery) operation.Result {
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
		result := golang.GoVersionResult{}
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

func (p Plugin) goDoc() operationruntime.TypedResultHandler[golang.GoDocQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoDocQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_doc_input", err.Error(), nil)
		}
		workdir, args, selector, diagnostics, err := p.goDocRequest(ctx, req)
		if err != nil {
			return operation.Failed("invalid_go_doc_input", err.Error(), nil)
		}
		run, err := p.runGoTool(ctx, workdir, args, req.MaxBytes, defaultToolchainTimeout)
		if err != nil && run.Command == "" {
			return operation.Failed("go_doc_failed", err.Error(), processData(run))
		}
		if err != nil && strings.TrimSpace(run.Stderr) != "" {
			diagnostics = append(diagnostics, language.Diagnostic{Severity: "warning", Code: "go_doc_output", Message: strings.TrimSpace(run.Stderr)})
		}
		result := golang.GoDocResult{
			Text:        strings.TrimSpace(run.Stdout),
			Package:     strings.TrimSpace(req.Package),
			Symbol:      selector,
			Workdir:     cleanRel(workdir),
			Diagnostics: diagnostics,
		}
		lines := []string{"Go doc"}
		if result.Package != "" {
			lines = append(lines, "- package: "+result.Package)
		}
		if result.Symbol != "" {
			lines = append(lines, "- symbol: "+result.Symbol)
		}
		if result.Text != "" {
			lines = append(lines, result.Text)
		}
		if len(result.Diagnostics) > 0 {
			lines = append(lines, fmt.Sprintf("Diagnostics: %d", len(result.Diagnostics)))
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"doc": result, "diagnostics": result.Diagnostics}})
	}
}

func (p Plugin) goList() operationruntime.TypedResultHandler[golang.GoListQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoListQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_list_input", err.Error(), nil)
		}
		args, patterns, err := goListArgs(req)
		if err != nil {
			return operation.Failed("invalid_go_list_input", err.Error(), nil)
		}
		run, err := p.runGoTool(ctx, req.Path, args, req.MaxBytes, defaultToolchainTimeout)
		if err != nil {
			return operation.Failed("go_list_failed", err.Error(), processData(run))
		}
		records, diagnostics, complete := parseGoListJSON(run.Stdout, req.MaxResults)
		result := golang.GoListResult{
			Records:     records,
			Modules:     req.Modules,
			Diagnostics: diagnostics,
			Complete:    complete,
		}
		lines := []string{fmt.Sprintf("Go list: %d record(s)", len(result.Records))}
		if req.Modules {
			lines[0] = fmt.Sprintf("Go list modules: %d record(s)", len(result.Records))
		}
		lines = append(lines, "- patterns: "+strings.Join(patterns, ", "))
		for _, record := range result.Records {
			if req.Modules {
				lines = append(lines, "- "+goListRecordString(record, "Path"))
			} else {
				lines = append(lines, "- "+goListRecordString(record, "ImportPath"))
			}
			if len(lines) >= 12 {
				break
			}
		}
		if len(result.Diagnostics) > 0 {
			lines = append(lines, fmt.Sprintf("Diagnostics: %d", len(result.Diagnostics)))
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"list": result, "records": result.Records, "diagnostics": result.Diagnostics}})
	}
}

func (p Plugin) goTest() operationruntime.TypedResultHandler[golang.GoTestQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoTestQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_test_input", err.Error(), nil)
		}
		args, patterns, err := goTestArgs(req)
		if err != nil {
			return operation.Failed("invalid_go_test_input", err.Error(), nil)
		}
		run, err := p.runGoTool(ctx, req.Path, args, req.MaxOutputBytes, defaultToolchainTimeout)
		if err != nil && run.Command == "" {
			return operation.Failed("go_test_failed", err.Error(), processData(run))
		}
		result := parseGoTestJSON(run.Stdout)
		result.Passed = err == nil
		if result.Passed {
			result.Diagnostics = filterGoTestParseDiagnostics(result.Diagnostics)
		}
		if run.Stderr != "" {
			result.Diagnostics = append(result.Diagnostics, language.Diagnostic{Severity: "error", Code: "go_test_stderr", Message: strings.TrimSpace(run.Stderr)})
		}
		if !result.Passed && goTestHasBuildFailed(result) {
			rawRun, _ := p.runGoTool(ctx, req.Path, goTestDiagnosticArgs(args), req.MaxOutputBytes, defaultToolchainTimeout)
			mergeGoTestBuildDiagnostics(&result, rawRun)
		}
		populateGoTestRunEvent(&result, run, patterns)
		text := renderGoTestResult(result, patterns)
		return operation.OK(operation.Rendered{Text: text, Data: map[string]any{"test": result, "packages": result.Packages, "diagnostics": result.Diagnostics, "test_run_event": result.TestRunEvent}})
	}
}

func (p Plugin) goVet() operationruntime.TypedResultHandler[golang.GoVetQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoVetQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_vet_input", err.Error(), nil)
		}
		args, patterns, err := goVetArgs(req)
		if err != nil {
			return operation.Failed("invalid_go_vet_input", err.Error(), nil)
		}
		run, err := p.runGoTool(ctx, req.Path, args, req.MaxOutputBytes, defaultToolchainTimeout)
		if err != nil && run.Command == "" {
			return operation.Failed("go_vet_failed", err.Error(), processData(run))
		}
		output := strings.TrimSpace(strings.Join([]string{run.Stdout, run.Stderr}, "\n"))
		diagnostics := parseGoVetOutput(output, req.JSON)
		result := golang.GoVetResult{Diagnostics: diagnostics, Output: output, Passed: err == nil && len(diagnostics) == 0}
		lines := []string{fmt.Sprintf("Go vet: %s", passFail(result.Passed))}
		lines = append(lines, "- patterns: "+strings.Join(patterns, ", "))
		for _, diag := range diagnostics {
			lines = append(lines, "- "+diagnosticLine(diag))
			if len(lines) >= 12 {
				break
			}
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"vet": result, "diagnostics": result.Diagnostics}})
	}
}

func (p Plugin) goBuild() operationruntime.TypedResultHandler[golang.GoBuildQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoBuildQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_build_input", err.Error(), nil)
		}
		args, patterns, err := goBuildArgs(req)
		if err != nil {
			return operation.Failed("invalid_go_build_input", err.Error(), nil)
		}
		if len(patterns) == 1 && !strings.Contains(patterns[0], "...") {
			listRun, listErr := p.runGoTool(ctx, req.Path, []string{"list", "-json", patterns[0]}, req.MaxOutputBytes, defaultToolchainTimeout)
			records, _, _ := parseGoListJSON(listRun.Stdout, 1)
			if listErr == nil && len(records) == 1 && goListRecordString(records[0], "Name") == "main" {
				scratch, err := p.system.Workspace().CreateScratch(ctx, "agentruntime-go-build-*")
				if err != nil {
					return operation.Failed("go_build_failed", err.Error(), nil)
				}
				defer func() { _ = scratch.RemoveAll(context.Background()) }()
				args = append([]string{"build", "-o", strings.TrimRight(scratch.Root(), "/") + "/main"}, args[1:]...)
			}
		}
		run, err := p.runGoTool(ctx, req.Path, args, req.MaxOutputBytes, defaultToolchainTimeout)
		if err != nil && run.Command == "" {
			return operation.Failed("go_build_failed", err.Error(), processData(run))
		}
		output := strings.TrimSpace(strings.Join([]string{run.Stdout, run.Stderr}, "\n"))
		result := golang.GoBuildResult{Diagnostics: diagnosticsFromText(output, "go_build_output"), Output: output, Passed: err == nil}
		lines := []string{fmt.Sprintf("Go build: %s", passFail(result.Passed))}
		lines = append(lines, "- patterns: "+strings.Join(patterns, ", "))
		for _, diag := range result.Diagnostics {
			lines = append(lines, "- "+diagnosticLine(diag))
			if len(lines) >= 12 {
				break
			}
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"build": result, "diagnostics": result.Diagnostics}})
	}
}

func (p Plugin) goFmt() operationruntime.TypedResultHandler[golang.GoFmtQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoFmtQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_fmt_input", err.Error(), nil)
		}
		args, patterns, dryRun, err := goFmtArgs(req)
		if err != nil {
			return operation.Failed("invalid_go_fmt_input", err.Error(), nil)
		}
		run, err := p.runGoTool(ctx, req.Path, args, req.MaxOutputBytes, defaultToolchainTimeout)
		if err != nil {
			return operation.Failed("go_fmt_failed", err.Error(), processData(run))
		}
		output := strings.TrimSpace(strings.Join([]string{run.Stdout, run.Stderr}, "\n"))
		files := parseGoFmtFiles(output, dryRun)
		result := golang.GoFmtResult{Files: files, Output: output, DryRun: dryRun, WouldWrite: dryRun && len(files) > 0, Changed: !dryRun && len(files) > 0}
		lines := []string{fmt.Sprintf("Go fmt: %d file(s)", len(files))}
		if dryRun {
			lines[0] = fmt.Sprintf("Go fmt dry-run: %d file(s)", len(files))
		}
		lines = append(lines, "- patterns: "+strings.Join(patterns, ", "))
		for _, file := range files {
			lines = append(lines, "- "+file)
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"fmt": result, "files": result.Files}})
	}
}

func (p Plugin) goInstall() operationruntime.TypedResultHandler[golang.GoInstallQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoInstallQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_install_input", err.Error(), nil)
		}
		args, packages, dryRun, env, err := goInstallArgs(req)
		if err != nil {
			return operation.Failed("invalid_go_install_input", err.Error(), nil)
		}
		run, err := p.runGoToolEnv(ctx, req.Path, args, env, req.MaxOutputBytes, defaultToolchainTimeout)
		if err != nil {
			return operation.Failed("go_install_failed", err.Error(), processData(run))
		}
		output := strings.TrimSpace(strings.Join([]string{run.Stdout, run.Stderr}, "\n"))
		result := golang.GoInstallResult{Packages: packages, Output: output, DryRun: dryRun, Installed: !dryRun}
		lines := []string{"Go install"}
		if dryRun {
			lines[0] = "Go install dry-run"
		}
		for _, pkg := range packages {
			lines = append(lines, "- "+pkg)
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"install": result, "packages": result.Packages}})
	}
}

func (p Plugin) goGet() operationruntime.TypedResultHandler[golang.GoGetQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoGetQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_get_input", err.Error(), nil)
		}
		args, packages, dryRun, err := goGetArgs(req)
		if err != nil {
			return operation.Failed("invalid_go_get_input", err.Error(), nil)
		}
		if dryRun {
			command := strings.TrimSpace("go " + strings.Join(args, " "))
			result := golang.GoGetResult{Packages: packages, DryRun: true, Changed: false, Command: command}
			lines := []string{"Go get dry-run", "- command: " + command}
			for _, pkg := range packages {
				lines = append(lines, "- "+pkg)
			}
			return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"get": result, "packages": result.Packages}})
		}
		run, err := p.runGoTool(ctx, req.Path, args, req.MaxOutputBytes, defaultToolchainTimeout)
		if err != nil {
			return operation.Failed("go_get_failed", err.Error(), processData(run))
		}
		output := strings.TrimSpace(strings.Join([]string{run.Stdout, run.Stderr}, "\n"))
		result := golang.GoGetResult{Packages: packages, Output: output, DryRun: false, Changed: true, Command: processCommand(run)}
		lines := []string{"Go get"}
		for _, pkg := range packages {
			lines = append(lines, "- "+pkg)
		}
		if output != "" {
			lines = append(lines, output)
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"get": result, "packages": result.Packages}})
	}
}

func (p Plugin) goModTidy() operationruntime.TypedResultHandler[golang.GoModTidyQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.GoModTidyQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_mod_tidy_input", err.Error(), nil)
		}
		args, dryRun, err := goModTidyArgs(req)
		if err != nil {
			return operation.Failed("invalid_go_mod_tidy_input", err.Error(), nil)
		}
		run, err := p.runGoTool(ctx, req.Path, args, req.MaxOutputBytes, defaultToolchainTimeout)
		output := strings.TrimSpace(strings.Join([]string{run.Stdout, run.Stderr}, "\n"))
		if err != nil && (!dryRun || run.Command == "") {
			return operation.Failed("go_mod_tidy_failed", err.Error(), processData(run))
		}
		result := golang.GoModTidyResult{Output: output, DryRun: dryRun, WouldChange: dryRun && strings.TrimSpace(run.Stdout) != "", Changed: !dryRun, Command: processCommand(run)}
		lines := []string{"Go mod tidy"}
		if dryRun {
			lines[0] = "Go mod tidy dry-run"
		}
		if output != "" {
			lines = append(lines, output)
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"mod_tidy": result}})
	}
}

func (p Plugin) runGoTool(ctx operation.Context, workdir string, args []string, maxBytesValue int, timeout time.Duration) (system.ProcessResult, error) {
	return p.runGoToolEnv(ctx, workdir, args, nil, maxBytesValue, timeout)
}

func (p Plugin) runGoToolEnv(ctx operation.Context, workdir string, args []string, env []string, maxBytesValue int, timeout time.Duration) (system.ProcessResult, error) {
	if p.system == nil || p.system.Process() == nil {
		return system.ProcessResult{}, fmt.Errorf("golang: system process is nil")
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
		Env:       append(system.DefaultProcessEnv(), env...),
		Timeout:   timeout,
		MaxStdout: limit,
		MaxStderr: limit,
	})
}

func goInfoIntent(_ operation.Context, req golang.GoInfoQuery) (operation.IntentSet, error) {
	return goToolIntentSet(req.Path, []string{"version"}, append([]string{"env", "-json"}, curatedGoEnvVars...))
}

func goEnvIntent(_ operation.Context, req golang.GoEnvQuery) (operation.IntentSet, error) {
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

func goVersionIntent(_ operation.Context, req golang.GoVersionQuery) (operation.IntentSet, error) {
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

func goDocIntent(_ operation.Context, req golang.GoDocQuery) (operation.IntentSet, error) {
	workdir, args, err := goDocArgs(req, "")
	if err != nil {
		return operation.IntentSet{}, err
	}
	return goToolIntentSet(workdir, args)
}

func goListIntent(_ operation.Context, req golang.GoListQuery) (operation.IntentSet, error) {
	args, _, err := goListArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	return goToolIntentSet(req.Path, args)
}

func goTestIntent(_ operation.Context, req golang.GoTestQuery) (operation.IntentSet, error) {
	args, _, err := goTestArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	return goToolIntentSet(req.Path, args)
}

func goVetIntent(_ operation.Context, req golang.GoVetQuery) (operation.IntentSet, error) {
	args, _, err := goVetArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	return goToolIntentSet(req.Path, args)
}

func goBuildIntent(_ operation.Context, req golang.GoBuildQuery) (operation.IntentSet, error) {
	args, _, err := goBuildArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	return goToolIntentSet(req.Path, args)
}

func goFmtIntent(_ operation.Context, req golang.GoFmtQuery) (operation.IntentSet, error) {
	args, _, _, err := goFmtArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	return goToolIntentSet(req.Path, args)
}

func goInstallIntent(_ operation.Context, req golang.GoInstallQuery) (operation.IntentSet, error) {
	args, _, _, _, err := goInstallArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	return goToolIntentSet(req.Path, args)
}

func goGetIntent(_ operation.Context, req golang.GoGetQuery) (operation.IntentSet, error) {
	args, _, _, err := goGetArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	return goToolIntentSet(req.Path, args)
}

func goModTidyIntent(_ operation.Context, req golang.GoModTidyQuery) (operation.IntentSet, error) {
	args, _, err := goModTidyArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
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

func (p Plugin) goDocRequest(ctx context.Context, req golang.GoDocQuery) (string, []string, string, []language.Diagnostic, error) {
	selector := strings.TrimSpace(req.Symbol)
	var diagnostics []language.Diagnostic
	if selector == "" && req.Path != "" && (req.Offset != nil || req.Line > 0 || req.Column > 0) {
		nav, err := p.resolveNavigation(ctx, golang.NavigationQuery{
			Language:    req.Language,
			Path:        req.Path,
			Line:        req.Line,
			Column:      req.Column,
			Offset:      req.Offset,
			Scope:       golang.NavigationScopePackage,
			IncludeDocs: true,
			MaxResults:  1,
			MaxBytes:    req.MaxBytes,
		}, true)
		if err != nil {
			diagnostics = append(diagnostics, language.Diagnostic{Path: cleanRel(req.Path), Severity: "warning", Code: "go_doc_position_resolution_failed", Message: err.Error()})
		} else if len(nav.Symbols) > 0 {
			selector = goDocSymbolSelector(nav.Symbols[0])
		} else if nav.Target.Name != "" {
			selector = nav.Target.Name
		}
	}
	workdir, args, err := goDocArgs(req, selector)
	return workdir, args, selector, diagnostics, err
}

func goDocArgs(req golang.GoDocQuery, resolvedSymbol string) (string, []string, error) {
	workdir, err := goDocWorkdir(req.Path)
	if err != nil {
		return "", nil, err
	}
	args := []string{"doc"}
	if req.All {
		args = append(args, "-all")
	}
	if req.Short {
		args = append(args, "-short")
	}
	if req.Source {
		args = append(args, "-src")
	}
	if req.IncludeUnexported {
		args = append(args, "-u")
	}
	if req.IncludeCmd {
		args = append(args, "-cmd")
	}
	pkg := strings.TrimSpace(req.Package)
	symbol := strings.TrimSpace(req.Symbol)
	if symbol == "" {
		symbol = strings.TrimSpace(resolvedSymbol)
	}
	if err := validateGoDocSelector(pkg, "package"); err != nil {
		return "", nil, err
	}
	if err := validateGoDocSelector(symbol, "symbol"); err != nil {
		return "", nil, err
	}
	if pkg != "" {
		args = append(args, pkg)
	}
	if symbol != "" {
		args = append(args, symbol)
	}
	return workdir, args, nil
}

func goDocWorkdir(raw string) (string, error) {
	rel, err := cleanOptionalWorkspacePath(raw)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(rel, ".go") {
		return pathDir(rel), nil
	}
	return rel, nil
}

func validateGoDocSelector(value, label string) error {
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s selector %q must not start with '-'", label, value)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(";&|<>`$\\", r) {
			return fmt.Errorf("%s selector %q contains unsupported character %q", label, value, r)
		}
	}
	return nil
}

func goDocSymbolSelector(symbol language.Symbol) string {
	name := strings.TrimSpace(symbol.Name)
	if name == "" {
		return ""
	}
	if symbol.Kind == language.SymbolField && symbol.Container != "" {
		return strings.TrimSpace(symbol.Container) + "." + name
	}
	return name
}

func goListArgs(req golang.GoListQuery) ([]string, []string, error) {
	args := []string{"list", "-json", "-buildvcs=false"}
	if req.IncludeErrors {
		args = append(args, "-e")
	}
	if req.Modules {
		args = append(args, "-m")
	}
	if req.Deps {
		args = append(args, "-deps")
	}
	if req.Test {
		args = append(args, "-test")
	}
	if req.Compiled {
		args = append(args, "-compiled")
	}
	if req.Find {
		args = append(args, "-find")
	}
	patterns := append([]string(nil), req.Patterns...)
	if len(patterns) == 0 {
		patterns = []string{"."}
	}
	if err := validateGoPatterns(patterns); err != nil {
		return nil, nil, err
	}
	for _, pattern := range patterns {
		if req.Modules && pattern == "." {
			continue
		}
		args = append(args, pattern)
	}
	return args, patterns, nil
}

func goTestArgs(req golang.GoTestQuery) ([]string, []string, error) {
	args := []string{"test", "-json"}
	if req.Run != "" {
		if err := validateGoTestRegexpFlag(req.Run, "run"); err != nil {
			return nil, nil, err
		}
		args = append(args, "-run="+req.Run)
	}
	if req.Skip != "" {
		if err := validateGoTestRegexpFlag(req.Skip, "skip"); err != nil {
			return nil, nil, err
		}
		args = append(args, "-skip="+req.Skip)
	}
	if req.Short {
		args = append(args, "-short")
	}
	if req.Failfast {
		args = append(args, "-failfast")
	}
	if req.Count != nil {
		if *req.Count < 0 {
			return nil, nil, fmt.Errorf("count must be >= 0")
		}
		args = append(args, fmt.Sprintf("-count=%d", *req.Count))
	}
	if req.Timeout != "" {
		if err := validateGoDuration(req.Timeout, "timeout"); err != nil {
			return nil, nil, err
		}
		args = append(args, "-timeout="+req.Timeout)
	}
	switch strings.TrimSpace(req.Vet) {
	case "", "default":
	case "off", "all":
		args = append(args, "-vet="+req.Vet)
	default:
		return nil, nil, fmt.Errorf("unsupported vet mode %q", req.Vet)
	}
	if req.Race {
		args = append(args, "-race")
	}
	if req.Cover {
		args = append(args, "-cover")
	}
	patterns, err := defaultGoPatterns(req.Patterns)
	if err != nil {
		return nil, nil, err
	}
	args = append(args, patterns...)
	return args, patterns, nil
}

func goVetArgs(req golang.GoVetQuery) ([]string, []string, error) {
	if req.Fix {
		return nil, nil, fmt.Errorf("fix is unsupported for go_vet")
	}
	if req.Diff {
		return nil, nil, fmt.Errorf("diff is unsupported for go_vet")
	}
	if strings.TrimSpace(req.Vettool) != "" {
		return nil, nil, fmt.Errorf("vettool is unsupported for go_vet")
	}
	args := []string{"vet"}
	if req.JSON {
		args = append(args, "-json")
	}
	if len(req.Tags) > 0 {
		tags, err := validateGoTags(req.Tags)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "-tags="+strings.Join(tags, ","))
	}
	patterns, err := defaultGoPatterns(req.Patterns)
	if err != nil {
		return nil, nil, err
	}
	args = append(args, patterns...)
	return args, patterns, nil
}

func goBuildArgs(req golang.GoBuildQuery) ([]string, []string, error) {
	if strings.TrimSpace(req.Output) != "" {
		return nil, nil, fmt.Errorf("output is unsupported for go_build")
	}
	args := []string{"build", "-buildvcs=false"}
	if len(req.Tags) > 0 {
		tags, err := validateGoTags(req.Tags)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "-tags="+strings.Join(tags, ","))
	}
	if req.Race {
		args = append(args, "-race")
	}
	if req.Cover {
		args = append(args, "-cover")
	}
	if req.Trimpath {
		args = append(args, "-trimpath")
	}
	if req.Mod != "" {
		if err := validateGoModMode(req.Mod); err != nil {
			return nil, nil, err
		}
		args = append(args, "-mod="+req.Mod)
	}
	patterns, err := defaultGoPatterns(req.Patterns)
	if err != nil {
		return nil, nil, err
	}
	args = append(args, patterns...)
	return args, patterns, nil
}

func goFmtArgs(req golang.GoFmtQuery) ([]string, []string, bool, error) {
	dryRun := boolDefault(req.DryRun, true)
	args := []string{"fmt"}
	if dryRun {
		args = append(args, "-n")
	}
	if req.Trace {
		args = append(args, "-x")
	}
	if req.Mod != "" {
		if err := validateGoModMode(req.Mod); err != nil {
			return nil, nil, false, err
		}
		args = append(args, "-mod="+req.Mod)
	}
	patterns, err := defaultGoPatterns(req.Patterns)
	if err != nil {
		return nil, nil, false, err
	}
	args = append(args, patterns...)
	return args, patterns, dryRun, nil
}

func goInstallArgs(req golang.GoInstallQuery) ([]string, []string, bool, []string, error) {
	dryRun := boolDefault(req.DryRun, true)
	args := []string{"install", "-buildvcs=false"}
	if dryRun {
		args = append(args, "-n")
	}
	if req.Trace {
		args = append(args, "-x")
	}
	if len(req.Tags) > 0 {
		tags, err := validateGoTags(req.Tags)
		if err != nil {
			return nil, nil, false, nil, err
		}
		args = append(args, "-tags="+strings.Join(tags, ","))
	}
	if req.Race {
		args = append(args, "-race")
	}
	if req.Trimpath {
		args = append(args, "-trimpath")
	}
	version := strings.TrimSpace(req.Version)
	if version != "" {
		if err := validateGoFlagValue(version, "version"); err != nil {
			return nil, nil, false, nil, err
		}
		if req.Mod != "" {
			return nil, nil, false, nil, fmt.Errorf("mod is unsupported when version is set")
		}
	} else if req.Mod != "" {
		if err := validateGoModMode(req.Mod); err != nil {
			return nil, nil, false, nil, err
		}
		args = append(args, "-mod="+req.Mod)
	}
	packages := append([]string(nil), req.Packages...)
	if len(packages) == 0 {
		return nil, nil, false, nil, fmt.Errorf("packages are required")
	}
	if err := validateGoPatterns(packages); err != nil {
		return nil, nil, false, nil, err
	}
	if version != "" {
		for i, pkg := range packages {
			packages[i] = pkg + "@" + version
		}
	}
	env, err := goInstallEnv(req.Env)
	if err != nil {
		return nil, nil, false, nil, err
	}
	args = append(args, packages...)
	return args, packages, dryRun, env, nil
}

func goGetArgs(req golang.GoGetQuery) ([]string, []string, bool, error) {
	dryRun := boolDefault(req.DryRun, true)
	args := []string{"get"}
	if req.Trace {
		args = append(args, "-x")
	}
	if req.Mod != "" {
		if err := validateGoModMode(req.Mod); err != nil {
			return nil, nil, false, err
		}
		args = append(args, "-mod="+req.Mod)
	}
	packages := append([]string(nil), req.Packages...)
	if len(packages) == 0 {
		return nil, nil, false, fmt.Errorf("packages are required")
	}
	if err := validateGoPatterns(packages); err != nil {
		return nil, nil, false, err
	}
	args = append(args, packages...)
	return args, packages, dryRun, nil
}

func goModTidyArgs(req golang.GoModTidyQuery) ([]string, bool, error) {
	dryRun := boolDefault(req.DryRun, true)
	args := []string{"mod", "tidy"}
	if dryRun {
		args = append(args, "-diff")
	}
	if req.Compat != "" {
		if err := validateGoFlagValue(req.Compat, "compat"); err != nil {
			return nil, false, err
		}
		args = append(args, "-compat="+req.Compat)
	}
	if req.Go != "" {
		if err := validateGoFlagValue(req.Go, "go"); err != nil {
			return nil, false, err
		}
		args = append(args, "-go="+req.Go)
	}
	if req.E {
		args = append(args, "-e")
	}
	if req.V {
		args = append(args, "-v")
	}
	if req.X {
		args = append(args, "-x")
	}
	return args, dryRun, nil
}

func defaultGoPatterns(patterns []string) ([]string, error) {
	out := append([]string(nil), patterns...)
	if len(out) == 0 {
		out = []string{"."}
	}
	if err := validateGoPatterns(out); err != nil {
		return nil, err
	}
	return out, nil
}

func validateGoPatterns(patterns []string) error {
	for _, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			return fmt.Errorf("go pattern is empty")
		}
		if strings.HasPrefix(trimmed, "-") {
			return fmt.Errorf("go pattern %q must not start with '-'", pattern)
		}
		for _, r := range trimmed {
			if r < 0x20 || r == 0x7f || strings.ContainsRune(";&|<>`$\\", r) {
				return fmt.Errorf("go pattern %q contains unsupported character %q", pattern, r)
			}
		}
	}
	return nil
}

func validateGoTags(tags []string) ([]string, error) {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			return nil, fmt.Errorf("build tag is empty")
		}
		if err := validateGoFlagValue(trimmed, "tag"); err != nil {
			return nil, err
		}
		out = append(out, trimmed)
	}
	return out, nil
}

func validateGoModMode(value string) error {
	switch strings.TrimSpace(value) {
	case "readonly", "vendor", "mod":
		return nil
	default:
		return fmt.Errorf("unsupported mod mode %q", value)
	}
}

func validateGoDuration(value, label string) error {
	if _, err := time.ParseDuration(value); err != nil {
		return fmt.Errorf("invalid %s duration %q: %w", label, value, err)
	}
	return validateGoFlagValue(value, label)
}

func validateGoFlagValue(value, label string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is empty", label)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(";&|<>`$\\", r) {
			return fmt.Errorf("%s %q contains unsupported character %q", label, value, r)
		}
	}
	return nil
}

func validateGoTestRegexpFlag(value, label string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is empty", label)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s %q contains unsupported control character %q", label, value, r)
		}
	}
	if _, err := regexp.Compile(value); err != nil {
		return fmt.Errorf("invalid %s regular expression %q: %w", label, value, err)
	}
	return nil
}

func goInstallEnv(values map[string]string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	allowed := map[string]bool{"GOBIN": true, "GOPATH": true, "GOOS": true, "GOARCH": true, "CGO_ENABLED": true}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if !allowed[key] {
			return nil, fmt.Errorf("env key %q is unsupported for go_install", key)
		}
		value := values[key]
		if strings.ContainsAny(value, "\x00\n\r") {
			return nil, fmt.Errorf("env value for %s contains unsupported control character", key)
		}
		out = append(out, key+"="+value)
	}
	return out, nil
}

func parseGoListJSON(raw string, maxResultsValue int) ([]map[string]any, []language.Diagnostic, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil, true
	}
	limit := maxResults(maxResultsValue)
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var records []map[string]any
	var diagnostics []language.Diagnostic
	complete := true
	for {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				break
			}
			return records, append(diagnostics, language.Diagnostic{Severity: "warning", Code: "go_list_parse_failed", Message: err.Error()}), false
		}
		if len(records) < limit {
			records = append(records, record)
			diagnostics = append(diagnostics, goListDiagnostics(record)...)
			continue
		}
		complete = false
	}
	return records, diagnostics, complete
}

func goListDiagnostics(record map[string]any) []language.Diagnostic {
	var diagnostics []language.Diagnostic
	if errObj, ok := record["Error"]; ok {
		if diag, ok := goListErrorDiagnostic(record, errObj, "go_list_error"); ok {
			diagnostics = append(diagnostics, diag)
		}
	}
	if deps, ok := record["DepsErrors"].([]any); ok {
		for _, depErr := range deps {
			if diag, ok := goListErrorDiagnostic(record, depErr, "go_list_deps_error"); ok {
				diagnostics = append(diagnostics, diag)
			}
		}
	}
	return diagnostics
}

func goListErrorDiagnostic(record map[string]any, raw any, code string) (language.Diagnostic, bool) {
	errMap, ok := raw.(map[string]any)
	if !ok {
		msg := strings.TrimSpace(fmt.Sprint(raw))
		if msg == "" {
			return language.Diagnostic{}, false
		}
		return language.Diagnostic{Path: goListRecordString(record, "Dir"), Severity: "error", Code: code, Message: msg}, true
	}
	msg := goListAnyString(errMap["Err"])
	if msg == "" {
		msg = strings.TrimSpace(fmt.Sprint(raw))
	}
	diag := language.Diagnostic{
		Path:     goListRecordString(record, "Dir"),
		Severity: "error",
		Code:     code,
		Message:  msg,
	}
	if pos := goListAnyString(errMap["Pos"]); pos != "" {
		diag.Message = pos + ": " + diag.Message
	}
	return diag, diag.Message != ""
}

func goListRecordString(record map[string]any, key string) string {
	if value := goListAnyString(record[key]); value != "" {
		return value
	}
	switch key {
	case "ImportPath":
		return goListAnyString(record["Path"])
	case "Path":
		return goListAnyString(record["ImportPath"])
	}
	return ""
}

func goListAnyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func parseGoTestJSON(raw string) golang.GoTestResult {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	decoder.UseNumber()
	packages := map[string]*golang.GoTestPackageResult{}
	var order []string
	var events []map[string]any
	var diagnostics []language.Diagnostic
	complete := true
	for {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			if strings.TrimSpace(raw) != "" {
				diagnostics = append(diagnostics, language.Diagnostic{Severity: "warning", Code: "go_test_parse_failed", Message: err.Error()})
			}
			complete = false
			break
		}
		events = append(events, event)
		pkgName := goListAnyString(event["Package"])
		if pkgName == "" {
			continue
		}
		pkg := packages[pkgName]
		if pkg == nil {
			pkg = &golang.GoTestPackageResult{Package: pkgName}
			packages[pkgName] = pkg
			order = append(order, pkgName)
		}
		action := goListAnyString(event["Action"])
		testName := goListAnyString(event["Test"])
		if elapsed, ok := event["Elapsed"].(json.Number); ok {
			if value, err := elapsed.Float64(); err == nil {
				pkg.Elapsed = value
			}
		}
		if output := strings.TrimRight(goListAnyString(event["Output"]), "\r\n"); strings.TrimSpace(output) != "" && len(pkg.Output) < 50 {
			pkg.Output = append(pkg.Output, output)
		}
		switch action {
		case "pass":
			if testName == "" {
				pkg.Status = "pass"
			} else {
				pkg.Passed++
			}
		case "fail":
			if testName == "" {
				pkg.Status = "fail"
			} else {
				pkg.Failed++
			}
		case "skip":
			if testName == "" {
				pkg.Status = "skip"
			} else {
				pkg.Skipped++
			}
		}
	}
	out := make([]golang.GoTestPackageResult, 0, len(order))
	for _, pkgName := range order {
		out = append(out, *packages[pkgName])
	}
	return golang.GoTestResult{Packages: out, Events: events, Diagnostics: diagnostics, Complete: complete}
}

func filterGoTestParseDiagnostics(diagnostics []language.Diagnostic) []language.Diagnostic {
	if len(diagnostics) == 0 {
		return nil
	}
	out := diagnostics[:0]
	for _, diag := range diagnostics {
		if diag.Code == "go_test_parse_failed" {
			continue
		}
		out = append(out, diag)
	}
	return out
}

func parseGoVetOutput(raw string, jsonMode bool) []language.Diagnostic {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if jsonMode {
		var root map[string]map[string][]map[string]any
		if err := json.Unmarshal([]byte(trimmed), &root); err == nil {
			var diagnostics []language.Diagnostic
			for pkg, analyzers := range root {
				for analyzer, entries := range analyzers {
					for _, entry := range entries {
						message := goListAnyString(entry["message"])
						if message == "" {
							continue
						}
						diag := language.Diagnostic{
							Path:     trimVetPath(goListAnyString(entry["posn"])),
							Severity: "warning",
							Code:     "go_vet_" + analyzer,
							Message:  message,
						}
						if diag.Path == "" {
							diag.Path = pkg
						}
						diagnostics = append(diagnostics, diag)
					}
				}
			}
			return diagnostics
		}
	}
	return diagnosticsFromText(trimmed, "go_vet_output")
}
func goTestHasBuildFailed(result golang.GoTestResult) bool {
	for _, pkg := range result.Packages {
		for _, output := range pkg.Output {
			if strings.Contains(output, "[build failed]") {
				return true
			}
		}
	}
	return false
}

func goTestDiagnosticArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "-json" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func mergeGoTestBuildDiagnostics(result *golang.GoTestResult, rawRun system.ProcessResult) {
	if result == nil || strings.TrimSpace(rawRun.Stderr) == "" {
		return
	}
	for i := range result.Packages {
		if result.Packages[i].Status != "fail" {
			continue
		}
		for _, line := range strings.Split(rawRun.Stderr, "\n") {
			line = strings.TrimSpace(line)
			if _, ok := parseGoCompilerDiagnostic(line); ok {
				result.Packages[i].Output = append(result.Packages[i].Output, line)
			}
		}
	}
}

func populateGoTestRunEvent(result *golang.GoTestResult, run system.ProcessResult, patterns []string) {
	if result == nil {
		return
	}
	summary := testrun.Summary{}
	for _, pkg := range result.Packages {
		summary.PackagesTotal++
		switch pkg.Status {
		case "pass":
			summary.PackagesPassed++
		case "fail":
			summary.PackagesFailed++
		case "skip":
			summary.PackagesSkipped++
		}
		summary.TestsPassed += pkg.Passed
		summary.TestsFailed += pkg.Failed
		summary.TestsSkipped += pkg.Skipped
	}
	summary.TestsTotal = summary.TestsPassed + summary.TestsFailed + summary.TestsSkipped
	failures := goTestFailures(*result, run)
	status := testrun.StatusPassed
	if !result.Passed || len(failures) > 0 || summary.PackagesFailed > 0 || summary.TestsFailed > 0 {
		status = testrun.StatusFailed
	}
	if run.TimedOut {
		status = testrun.StatusError
	}
	if status == testrun.StatusPassed && summary.TestsTotal == 0 && summary.PackagesSkipped > 0 && summary.PackagesPassed == 0 {
		status = testrun.StatusSkipped
	}
	result.TestRunEvent = testrun.Event{
		Kind:       testrun.EventFinished,
		Toolchain:  "go",
		Command:    strings.TrimSpace(strings.Join(append([]string{run.Command}, run.Args...), " ")),
		Target:     strings.Join(patterns, ", "),
		Status:     status,
		Summary:    summary,
		Failures:   failures,
		DurationMS: run.Duration.Milliseconds(),
		Truncated:  run.StdoutTruncated || run.StderrTruncated,
	}
	if result.TestRunEvent.Truncated {
		result.TestRunEvent.Note = "output truncated; failure details were prioritized"
	}
}

func goTestFailures(result golang.GoTestResult, run system.ProcessResult) []testrun.Failure {
	var failures []testrun.Failure
	seen := map[string]bool{}
	for _, pkg := range result.Packages {
		for _, failure := range failuresFromGoTestPackage(pkg) {
			key := failureKey(failure)
			if !seen[key] {
				seen[key] = true
				failures = append(failures, failure)
			}
		}
	}
	for _, diag := range result.Diagnostics {
		failure := failureFromDiagnostic(diag)
		key := failureKey(failure)
		if !seen[key] {
			seen[key] = true
			failures = append(failures, failure)
		}
	}
	if run.TimedOut {
		failures = append(failures, testrun.Failure{Kind: testrun.FailureTimeout, Message: "go test timed out"})
	}
	return failures
}

func failuresFromGoTestPackage(pkg golang.GoTestPackageResult) []testrun.Failure {
	var failures []testrun.Failure
	var currentTest string
	for _, output := range pkg.Output {
		line := strings.TrimSpace(output)
		if line == "" {
			continue
		}
		if name, ok := parseGoTestRunLine(line, "=== RUN"); ok {
			currentTest = name
			continue
		}
		if name, ok := parseGoTestRunLine(line, "--- FAIL:"); ok {
			currentTest = name
			continue
		}
		if diag, ok := parseGoCompilerDiagnostic(line); ok {
			failures = append(failures, testrun.Failure{Kind: testrun.FailureBuild, Package: pkg.Package, File: diag.Path, Line: diag.Line, Column: diag.Column, Message: diag.Message})
			continue
		}
		if strings.HasPrefix(line, "panic:") || strings.Contains(line, "panic:") {
			failures = append(failures, testrun.Failure{Kind: testrun.FailurePanic, Package: pkg.Package, Test: currentTest, Message: line})
			continue
		}
		if strings.Contains(line, "setup failed") || strings.HasPrefix(line, "FAIL") && currentTest == "" {
			failures = append(failures, testrun.Failure{Kind: testrun.FailureSetup, Package: pkg.Package, Message: line})
			continue
		}
		if currentTest != "" && looksLikeGoTestFailureLine(line) {
			file, lineNo, message := parseGoTestFailureLine(line)
			failures = append(failures, testrun.Failure{Kind: testrun.FailureAssertion, Package: pkg.Package, Test: currentTest, File: file, Line: lineNo, Message: message})
		}
	}
	if pkg.Status == "fail" && len(failures) == 0 {
		failures = append(failures, testrun.Failure{Kind: testrun.FailureUnknown, Package: pkg.Package, Message: "package failed"})
	}
	return failures
}

func parseGoTestRunLine(line, prefix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if idx := strings.Index(name, " "); idx >= 0 {
		name = name[:idx]
	}
	return name, name != ""
}

type parsedGoDiagnostic struct {
	Path    string
	Line    int
	Column  int
	Message string
}

func parseGoCompilerDiagnostic(line string) (parsedGoDiagnostic, bool) {
	parts := strings.SplitN(line, ":", 4)
	if len(parts) < 4 || !strings.HasSuffix(parts[0], ".go") {
		return parsedGoDiagnostic{}, false
	}
	lineNo, err := strconv.Atoi(parts[1])
	if err != nil {
		return parsedGoDiagnostic{}, false
	}
	column, err := strconv.Atoi(parts[2])
	if err != nil {
		return parsedGoDiagnostic{}, false
	}
	message := strings.TrimSpace(parts[3])
	if message == "" {
		return parsedGoDiagnostic{}, false
	}
	return parsedGoDiagnostic{Path: parts[0], Line: lineNo, Column: column, Message: message}, true
}

func looksLikeGoTestFailureLine(line string) bool {
	_, lineNo, message := parseGoTestFailureLine(line)
	return lineNo > 0 && message != ""
}

func parseGoTestFailureLine(line string) (string, int, string) {
	parts := strings.SplitN(line, ":", 3)
	if len(parts) < 3 || !strings.HasSuffix(parts[0], ".go") {
		return "", 0, line
	}
	lineNo, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, line
	}
	return parts[0], lineNo, strings.TrimSpace(parts[2])
}

func failureFromDiagnostic(diag language.Diagnostic) testrun.Failure {
	failure := testrun.Failure{Kind: testrun.FailureUnknown, Package: diag.Code, File: diag.Path, Message: diag.Message}
	if parsed, ok := parseGoCompilerDiagnostic(diag.Message); ok {
		failure.Kind = testrun.FailureBuild
		failure.File = parsed.Path
		failure.Line = parsed.Line
		failure.Column = parsed.Column
		failure.Message = parsed.Message
	}
	if diag.Code == "go_test_stderr" {
		failure.Kind = testrun.FailureSetup
	}
	return failure
}

func failureKey(failure testrun.Failure) string {
	return string(failure.Kind) + "\x00" + failure.Package + "\x00" + failure.Test + "\x00" + failure.File + "\x00" + strconv.Itoa(failure.Line) + "\x00" + strconv.Itoa(failure.Column) + "\x00" + failure.Message
}

func renderGoTestResult(result golang.GoTestResult, patterns []string) string {
	status := strings.ToUpper(passFail(result.Passed))
	lines := []string{fmt.Sprintf("go_test: %s %s", status, strings.Join(patterns, ", "))}
	if len(result.TestRunEvent.Failures) > 0 {
		lines = append(lines, "")
		groups := map[testrun.FailureKind]string{
			testrun.FailureBuild:     "build failed:",
			testrun.FailureAssertion: "failed tests:",
			testrun.FailurePanic:     "panic:",
			testrun.FailureTimeout:   "timeout:",
			testrun.FailureSetup:     "setup failed:",
			testrun.FailureUnknown:   "failures:",
		}
		order := []testrun.FailureKind{testrun.FailureBuild, testrun.FailureAssertion, testrun.FailurePanic, testrun.FailureTimeout, testrun.FailureSetup, testrun.FailureUnknown}
		for _, kind := range order {
			sectionStarted := false
			shown := 0
			for _, failure := range result.TestRunEvent.Failures {
				if failure.Kind != kind {
					continue
				}
				if !sectionStarted {
					lines = append(lines, groups[kind])
					sectionStarted = true
				}
				lines = append(lines, renderGoTestFailure(failure))
				shown++
				if shown >= 5 {
					break
				}
			}
			if sectionStarted {
				lines = append(lines, "")
			}
		}
	}
	summary := result.TestRunEvent.Summary
	lines = append(lines, fmt.Sprintf("summary: packages=%d passed=%d failed=%d skipped=%d; tests=%d passed=%d failed=%d skipped=%d", summary.PackagesTotal, summary.PackagesPassed, summary.PackagesFailed, summary.PackagesSkipped, summary.TestsTotal, summary.TestsPassed, summary.TestsFailed, summary.TestsSkipped))
	if result.TestRunEvent.Truncated {
		lines = append(lines, "truncated: "+result.TestRunEvent.Note)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func renderGoTestFailure(failure testrun.Failure) string {
	location := failure.File
	if failure.Line > 0 {
		location += ":" + strconv.Itoa(failure.Line)
		if failure.Column > 0 {
			location += ":" + strconv.Itoa(failure.Column)
		}
	}
	message := strings.TrimSpace(failure.Message)
	if failure.Test != "" {
		if location != "" {
			return fmt.Sprintf("- %s\n  %s: %s", failure.Test, location, message)
		}
		return fmt.Sprintf("- %s\n  %s", failure.Test, message)
	}
	if location != "" {
		return location + ": " + message
	}
	if failure.Package != "" {
		return "- " + failure.Package + ": " + message
	}
	return "- " + message
}

func diagnosticsFromText(raw, code string) []language.Diagnostic {
	var diagnostics []language.Diagnostic
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		diagnostics = append(diagnostics, language.Diagnostic{Severity: "error", Code: code, Message: line})
	}
	return diagnostics
}

func diagnosticLine(diag language.Diagnostic) string {
	if diag.Path != "" {
		return diag.Path + ": " + diag.Message
	}
	return diag.Message
}

func trimVetPath(pos string) string {
	if pos == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(pos, "\\", "/")
	idx := strings.Index(cleaned, "/pkg/")
	if idx >= 0 {
		return strings.TrimPrefix(cleaned[idx+1:], "/")
	}
	return cleaned
}

func parseGoFmtFiles(raw string, dryRun bool) []string {
	seen := map[string]bool{}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if dryRun {
			fields := strings.Fields(line)
			for _, field := range fields {
				if strings.HasSuffix(field, ".go") {
					file := strings.Trim(field, "\"'")
					if !seen[file] {
						seen[file] = true
						files = append(files, file)
					}
				}
			}
			continue
		}
		if !seen[line] {
			seen[line] = true
			files = append(files, line)
		}
	}
	return files
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
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

func parseGoProxy(raw string) golang.GoProxyConfig {
	cfg := golang.GoProxyConfig{Raw: strings.TrimSpace(raw)}
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
			cfg.Groups = append(cfg.Groups, golang.GoProxyGroup{Entries: entries})
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

func parseGoVersionLines(raw string) []golang.GoVersionRecord {
	var records []golang.GoVersionRecord
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			records = append(records, golang.GoVersionRecord{Path: strings.TrimSuffix(fields[0], ":"), Version: fields[1], Raw: line})
		}
	}
	return records
}

func parseGoVersionJSON(raw string) ([]golang.GoVersionRecord, []language.Diagnostic) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	var records []golang.GoVersionRecord
	for {
		var obj map[string]any
		if err := decoder.Decode(&obj); err != nil {
			if err == io.EOF {
				break
			}
			return records, []language.Diagnostic{{Severity: "warning", Code: "go_version_parse_failed", Message: err.Error()}}
		}
		record := golang.GoVersionRecord{BuildInfo: obj}
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

func processCommand(result system.ProcessResult) string {
	if result.Command == "" {
		return ""
	}
	return strings.TrimSpace(strings.Join(append([]string{result.Command}, result.Args...), " "))
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
