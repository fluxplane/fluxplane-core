package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
	"github.com/fluxplane/agentruntime/runtime/system"
)

type datasourceGitLabCheckResult struct {
	Root       string                         `json:"root"`
	Env        datasourceGitLabEnvInfo        `json:"env"`
	Datasource datasourceGitLabDatasourceInfo `json:"datasource"`
	GitLab     gitlabplugin.AccessCheckResult `json:"gitlab"`
	Index      []datasourceGitLabIndexInfo    `json:"index"`
}

type datasourceGitLabEnvInfo struct {
	Files                []string `json:"files,omitempty"`
	AuthEnv              string   `json:"auth_env,omitempty"`
	Precedence           string   `json:"precedence,omitempty"`
	AuthEnvInFiles       bool     `json:"auth_env_in_files"`
	AuthEnvInHost        bool     `json:"auth_env_in_host"`
	HostOverridesEnvFile bool     `json:"host_overrides_env_file"`
}

type datasourceGitLabDatasourceInfo struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind,omitempty"`
	Instance string   `json:"instance,omitempty"`
	Entities []string `json:"entities,omitempty"`
}

type datasourceGitLabIndexInfo struct {
	Entity          string `json:"entity"`
	Configured      bool   `json:"configured"`
	ExpectedIndexed bool   `json:"expected_indexed"`
	Reason          string `json:"reason,omitempty"`
	Records         int    `json:"records"`
	Documents       int    `json:"documents"`
	Queued          int    `json:"queued"`
	Runs            int    `json:"runs"`
	TargetPresent   bool   `json:"target_present,omitempty"`
}

func runDatasourceGitLabCheck(ctx context.Context, opts datasourceGitLabCheckOptions, appDir string, out io.Writer) error {
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return err
	}
	ds, ref, cfg, err := gitLabDatasourceConfig(loaded.Distribution.Bundles, opts.datasource)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(loaded.Root)
	if err != nil {
		return err
	}
	envFiles := trimGitLabCheckStrings(opts.envFiles)
	loadedEnv, err := system.LoadEnvFiles(root, envFiles)
	if err != nil {
		return err
	}
	hostSystem, err := system.NewHost(system.Config{
		Root:                root,
		Workspace:           system.WorkspaceConfig{EnvFiles: envFiles},
		AllowPrivateNetwork: true,
	})
	if err != nil {
		return err
	}
	checkSystem := system.System(hostSystem)
	if !opts.hostEnv {
		checkSystem = envOverrideSystem{
			System: hostSystem,
			env: envFileOverrideEnvironment{
				base:   hostSystem.Environment(),
				values: loadedEnv.Values,
			},
		}
	}
	check, err := gitlabplugin.CheckAccess(ctx, checkSystem, ref, cfg, gitlabplugin.AccessCheckRequest{
		MergeRequest: opts.mergeRequest,
	})
	if err != nil {
		return err
	}
	indexInfo, err := gitLabIndexInfo(ctx, root, loaded.Distribution.Bundles, ds, opts, check.MergeRequest.RecordID)
	if err != nil {
		return err
	}
	result := datasourceGitLabCheckResult{
		Root: root,
		Env:  gitLabEnvInfo(loadedEnv, check.Auth.Env, opts.hostEnv),
		Datasource: datasourceGitLabDatasourceInfo{
			Name:     string(ds.Name),
			Kind:     ds.Kind,
			Instance: ds.Config["instance"],
			Entities: datasourceEntityNames(ds.Entities),
		},
		GitLab: check,
		Index:  indexInfo,
	}
	switch strings.ToLower(strings.TrimSpace(opts.output)) {
	case "", "text":
		printDatasourceGitLabCheck(out, result)
	case "json":
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	default:
		return fmt.Errorf("datasource gitlab check: unsupported output format %q", opts.output)
	}
	return nil
}

func gitLabDatasourceConfig(bundles []resource.ContributionBundle, datasourceName string) (coredatasource.Spec, resource.PluginRef, gitlabplugin.Config, error) {
	name := strings.TrimSpace(datasourceName)
	if name == "" {
		name = gitlabplugin.Name
	}
	var ds coredatasource.Spec
	for _, bundle := range bundles {
		for _, candidate := range bundle.Datasources {
			if strings.EqualFold(string(candidate.Name), name) {
				ds = candidate
				break
			}
		}
		if ds.Name != "" {
			break
		}
	}
	if ds.Name == "" {
		for _, bundle := range bundles {
			for _, candidate := range bundle.Datasources {
				if strings.EqualFold(strings.TrimSpace(candidate.Kind), gitlabplugin.Name) {
					ds = candidate
					break
				}
			}
			if ds.Name != "" {
				break
			}
		}
	}
	if ds.Name == "" {
		return coredatasource.Spec{}, resource.PluginRef{}, gitlabplugin.Config{}, fmt.Errorf("datasource gitlab check: no GitLab datasource named %q found", name)
	}
	if strings.TrimSpace(ds.Kind) != "" && !strings.EqualFold(strings.TrimSpace(ds.Kind), gitlabplugin.Name) {
		return coredatasource.Spec{}, resource.PluginRef{}, gitlabplugin.Config{}, fmt.Errorf("datasource gitlab check: datasource %q has kind %q, not gitlab", ds.Name, ds.Kind)
	}
	instance := strings.TrimSpace(ds.Config["instance"])
	plugins := gitLabPluginRefs(bundles)
	ref, ok := selectGitLabPluginRef(plugins, instance)
	if !ok {
		if instance != "" {
			return coredatasource.Spec{}, resource.PluginRef{}, gitlabplugin.Config{}, fmt.Errorf("datasource gitlab check: no GitLab plugin instance %q found", instance)
		}
		return coredatasource.Spec{}, resource.PluginRef{}, gitlabplugin.Config{}, fmt.Errorf("datasource gitlab check: no GitLab plugin declaration found")
	}
	cfg, err := pluginhost.DecodeConfig[gitlabplugin.Config](ref.Config)
	if err != nil {
		return coredatasource.Spec{}, resource.PluginRef{}, gitlabplugin.Config{}, fmt.Errorf("datasource gitlab check: decode GitLab plugin config: %w", err)
	}
	return ds, ref, cfg, nil
}

func gitLabPluginRefs(bundles []resource.ContributionBundle) []resource.PluginRef {
	var refs []resource.PluginRef
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			if strings.EqualFold(strings.TrimSpace(ref.Name), gitlabplugin.Name) {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func selectGitLabPluginRef(refs []resource.PluginRef, instance string) (resource.PluginRef, bool) {
	if len(refs) == 0 {
		return resource.PluginRef{}, false
	}
	if instance != "" {
		for _, ref := range refs {
			if strings.EqualFold(ref.InstanceName(), instance) {
				return ref, true
			}
		}
		return resource.PluginRef{}, false
	}
	return refs[0], true
}

func gitLabIndexInfo(ctx context.Context, root string, bundles []resource.ContributionBundle, ds coredatasource.Spec, opts datasourceGitLabCheckOptions, targetMR string) ([]datasourceGitLabIndexInfo, error) {
	index, err := newSemanticIndex(root, bundles, opts.storePath, opts.provider, opts.model)
	if err != nil {
		return nil, err
	}
	defer func() { _ = index.Close() }()
	entities := []coredatasource.EntityType{gitlabplugin.MergeRequestEntity, gitlabplugin.MergeRequestDiffEntity}
	out := make([]datasourceGitLabIndexInfo, 0, len(entities))
	for _, entity := range entities {
		info := datasourceGitLabIndexInfo{
			Entity:          string(entity),
			Configured:      datasourceHasEntity(ds, entity),
			ExpectedIndexed: false,
			Reason:          "GitLab merge request and diff entities are live lookup entities; they do not currently advertise index capability.",
		}
		status, err := index.Status(ctx, semantic.StatusRequest{
			Datasource: ds.Name,
			Entity:     entity,
		})
		if err != nil {
			return nil, err
		}
		info.Records = len(status.Records)
		info.Documents = len(status.Documents)
		info.Queued = len(status.Queue)
		info.Runs = len(status.Runs)
		if targetMR != "" {
			info.TargetPresent = gitLabTargetPresent(entity, targetMR, status)
		}
		out = append(out, info)
	}
	return out, nil
}

func gitLabTargetPresent(entity coredatasource.EntityType, targetMR string, status semantic.StatusResult) bool {
	for _, record := range status.Records {
		if gitLabStatusRefMatches(entity, targetMR, record.Ref.ID) {
			return true
		}
	}
	for _, doc := range status.Documents {
		if gitLabStatusRefMatches(entity, targetMR, doc.Ref.ID) {
			return true
		}
	}
	for _, job := range status.Queue {
		if gitLabStatusRefMatches(entity, targetMR, job.Ref.ID) {
			return true
		}
	}
	return false
}

func gitLabStatusRefMatches(entity coredatasource.EntityType, targetMR, id string) bool {
	if entity == gitlabplugin.MergeRequestDiffEntity {
		return strings.HasPrefix(id, targetMR+"!")
	}
	return id == targetMR
}

func printDatasourceGitLabCheck(out io.Writer, result datasourceGitLabCheckResult) {
	_, _ = fmt.Fprintf(out, "root=%s\n", result.Root)
	_, _ = fmt.Fprintf(out, "env files=%s auth_env=%s precedence=%s auth_env_in_files=%t auth_env_in_host=%t host_overrides_env_file=%t\n", strings.Join(result.Env.Files, ","), result.Env.AuthEnv, result.Env.Precedence, result.Env.AuthEnvInFiles, result.Env.AuthEnvInHost, result.Env.HostOverridesEnvFile)
	_, _ = fmt.Fprintf(out, "datasource=%s kind=%s instance=%s\n", result.Datasource.Name, result.Datasource.Kind, result.Datasource.Instance)
	_, _ = fmt.Fprintf(out, "gitlab base_url=%s auth_method=%s auth_kind=%s auth_env=%s\n", result.GitLab.BaseURL, result.GitLab.Auth.Method, result.GitLab.Auth.Kind, result.GitLab.Auth.Env)
	printAPIStatus(out, "user", result.GitLab.User.APICheck)
	if result.GitLab.User.OK {
		_, _ = fmt.Fprintf(out, "user id=%d username=%s name=%s state=%s is_admin=%t\n", result.GitLab.User.ID, result.GitLab.User.Username, result.GitLab.User.Name, result.GitLab.User.State, result.GitLab.User.IsAdmin)
	}
	printAPIStatus(out, "token", result.GitLab.Token.APICheck)
	if result.GitLab.Token.OK {
		_, _ = fmt.Fprintf(out, "token id=%d name=%s active=%t revoked=%t expires_at=%s scopes=%s\n", result.GitLab.Token.ID, result.GitLab.Token.Name, result.GitLab.Token.Active, result.GitLab.Token.Revoked, result.GitLab.Token.ExpiresAt, strings.Join(result.GitLab.Token.Scopes, ","))
	}
	if result.GitLab.MergeRequest.Requested != "" {
		mr := result.GitLab.MergeRequest
		_, _ = fmt.Fprintf(out, "mr requested=%s project=%s iid=%d record_id=%s\n", mr.Requested, mr.ProjectRef, mr.IID, mr.RecordID)
		if mr.Error != "" {
			_, _ = fmt.Fprintf(out, "mr error=%s\n", mr.Error)
		} else {
			printAPIStatus(out, "mr.project", mr.Project.APICheck)
			if mr.Project.OK {
				_, _ = fmt.Fprintf(out, "mr.project id=%d path=%s url=%s\n", mr.Project.ID, mr.Project.PathWithNamespace, mr.Project.WebURL)
			}
			printAPIStatus(out, "mr.lookup", mr.Lookup.APICheck)
			if mr.Lookup.OK {
				_, _ = fmt.Fprintf(out, "mr.lookup id=%d project_id=%d state=%s title=%s url=%s\n", mr.Lookup.ID, mr.Lookup.ProjectID, mr.Lookup.State, mr.Lookup.Title, mr.Lookup.WebURL)
			}
			printAPIStatus(out, "mr.diffs", mr.Diffs.APICheck)
			if mr.Diffs.OK {
				_, _ = fmt.Fprintf(out, "mr.diffs count=%d collapsed=%d too_large=%d\n", mr.Diffs.Count, mr.Diffs.Collapsed, mr.Diffs.TooLarge)
				for _, file := range mr.Diffs.Files {
					_, _ = fmt.Fprintf(out, "mr.diff path=%s has_diff=%t collapsed=%t too_large=%t\n", file.Path, file.HasDiff, file.Collapsed, file.TooLarge)
				}
			}
		}
	}
	for _, idx := range result.Index {
		_, _ = fmt.Fprintf(out, "index entity=%s configured=%t expected_indexed=%t records=%d documents=%d queued=%d runs=%d target_present=%t reason=%s\n", idx.Entity, idx.Configured, idx.ExpectedIndexed, idx.Records, idx.Documents, idx.Queued, idx.Runs, idx.TargetPresent, idx.Reason)
	}
}

func printAPIStatus(out io.Writer, label string, check gitlabplugin.APICheck) {
	if check.OK {
		_, _ = fmt.Fprintf(out, "%s ok status=%d\n", label, check.Status)
		return
	}
	_, _ = fmt.Fprintf(out, "%s failed status=%d error=%s\n", label, check.Status, check.Error)
}

func datasourceHasEntity(ds coredatasource.Spec, entity coredatasource.EntityType) bool {
	for _, candidate := range ds.Entities {
		if candidate == entity {
			return true
		}
	}
	return false
}

func datasourceEntityNames(entities []coredatasource.EntityType) []string {
	out := make([]string, 0, len(entities))
	for _, entity := range entities {
		out = append(out, string(entity))
	}
	return out
}

func gitLabEnvInfo(loaded system.EnvFileSet, authEnv string, hostEnv bool) datasourceGitLabEnvInfo {
	info := datasourceGitLabEnvInfo{
		AuthEnv:    strings.TrimSpace(authEnv),
		Files:      append([]string(nil), loaded.Files...),
		Precedence: "env-file",
	}
	if hostEnv {
		info.Precedence = "host"
	}
	if info.AuthEnv != "" {
		_, info.AuthEnvInFiles = loaded.Values[info.AuthEnv]
	}
	if info.AuthEnv != "" {
		_, info.AuthEnvInHost = os.LookupEnv(info.AuthEnv)
	}
	info.HostOverridesEnvFile = info.AuthEnvInFiles && info.AuthEnvInHost
	return info
}

type envOverrideSystem struct {
	system.System
	env system.Environment
}

func (s envOverrideSystem) Environment() system.Environment {
	return s.env
}

type envFileOverrideEnvironment struct {
	base   system.Environment
	values map[string]string
}

func (e envFileOverrideEnvironment) Lookup(ctx context.Context, key string) (string, bool, error) {
	key = strings.TrimSpace(key)
	if value, ok := e.values[key]; ok {
		return value, true, nil
	}
	if e.base == nil {
		return "", false, nil
	}
	return e.base.Lookup(ctx, key)
}

func trimGitLabCheckStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
