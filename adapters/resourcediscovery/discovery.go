package resourcediscovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/agentdir"
	"github.com/fluxplane/agentruntime/adapters/appconfig"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
)

// Result is the set of resource bundles discovered from a local root.
type Result struct {
	Root            string                        `json:"root"`
	Bundles         []resource.ContributionBundle `json:"bundles"`
	Diagnostics     []resource.Diagnostic         `json:"diagnostics,omitempty"`
	ImplicitPlugins map[string]bool               `json:"implicit_plugins,omitempty"`
}

// Discover loads app config, root .agents resources, local app sources, and
// opted-in user resources.
func Discover(ctx context.Context, root string) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	result := Result{Root: filepath.Clean(abs)}
	cfgFile, hasConfig, err := loadAppConfig(ctx, abs)
	if err != nil {
		return Result{}, err
	}
	if hasConfig {
		result.Bundles = append(result.Bundles, cfgFile.Bundle)
		for _, app := range cfgFile.Bundle.Apps {
			for _, source := range app.Sources {
				bundle, ok, err := loadSource(ctx, abs, source)
				if err != nil {
					result.Diagnostics = append(result.Diagnostics, diagnostic(resource.SourceRef{Location: source.Location}, err))
					continue
				}
				if ok {
					result.Bundles = append(result.Bundles, bundle)
				}
			}
			if app.Discovery.IncludeGlobalUserResources {
				result.Bundles = append(result.Bundles, loadUserResources(ctx)...)
			}
		}
	}
	if bundle, ok, err := loadRootAgentDir(ctx, abs); err != nil {
		result.Diagnostics = append(result.Diagnostics, diagnostic(resource.SourceRef{Location: filepath.Join(abs, ".agents")}, err))
	} else if ok {
		result.Bundles = append(result.Bundles, bundle)
	}
	return result, nil
}

func loadAppConfig(ctx context.Context, root string) (appconfig.File, bool, error) {
	for _, name := range appconfig.DefaultManifestNames {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			file, err := appconfig.LoadDirFile(ctx, root)
			return file, true, err
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return appconfig.File{}, false, err
		}
	}
	return appconfig.File{}, false, nil
}

func loadRootAgentDir(ctx context.Context, root string) (resource.ContributionBundle, bool, error) {
	candidate := filepath.Join(root, ".agents")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		bundle, err := agentdir.LoadDir(ctx, candidate)
		return bundle, true, err
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return resource.ContributionBundle{}, false, err
	}
	return resource.ContributionBundle{}, false, nil
}

func loadSource(ctx context.Context, root string, source coreapp.SourceSpec) (resource.ContributionBundle, bool, error) {
	location := strings.TrimSpace(source.Location)
	if location == "" {
		return resource.ContributionBundle{}, false, fmt.Errorf("source location is empty")
	}
	if strings.Contains(location, "://") {
		return resource.ContributionBundle{}, false, fmt.Errorf("remote source %q is not supported by local discovery", location)
	}
	path := location
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, location)
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resource.ContributionBundle{}, false, nil
		}
		return resource.ContributionBundle{}, false, err
	}
	if !info.IsDir() {
		return resource.ContributionBundle{}, false, fmt.Errorf("source %q is not a directory", location)
	}
	bundle, err := agentdir.LoadDir(ctx, path)
	if err != nil {
		return resource.ContributionBundle{}, false, err
	}
	if source.Scope != "" || source.Ecosystem != "" {
		bundle.Source.Scope = resource.Scope(source.Scope)
		bundle.Source.Ecosystem = source.Ecosystem
		if bundle.Source.Scope == "" {
			bundle.Source.Scope = resource.ScopeProject
		}
	}
	return bundle, true, nil
}

func loadUserResources(ctx context.Context) []resource.ContributionBundle {
	var out []resource.ContributionBundle
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	for _, rel := range []string{".agents", ".claude"} {
		dir := filepath.Join(home, rel)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		bundle, err := agentdir.LoadDir(ctx, dir)
		if err != nil {
			continue
		}
		bundle.Source.Scope = resource.ScopeUser
		bundle.Source.Trust = policy.Trust{Kind: policy.TrustSource, Level: policy.TrustVerified}
		out = append(out, bundle)
	}
	return out
}

func diagnostic(source resource.SourceRef, err error) resource.Diagnostic {
	return resource.Diagnostic{Severity: resource.SeverityError, Source: source, Message: err.Error()}
}
