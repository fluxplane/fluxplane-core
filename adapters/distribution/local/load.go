// Package local loads filesystem paths as ephemeral distributions.
package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/agentdir"
	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/fluxplane/agentruntime/adapters/distribution/localruntime"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
)

// Load loads a local path into an ephemeral runnable distribution.
func Load(ctx context.Context, path string) (distribution.Loaded, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return distribution.Loaded{}, err
	}
	root = filepath.Clean(root)
	loaded := distribution.Loaded{Root: root}
	var distributionConfig coredistribution.Spec
	cfgFile, hasConfig, err := loadAppConfig(ctx, root)
	if err != nil {
		return distribution.Loaded{}, err
	}
	if hasConfig {
		if err := cfgFile.Validate(); err != nil {
			return distribution.Loaded{}, err
		}
		loaded.Manifest = cfgFile.Path
		distributionConfig = cfgFile.Distribution
		loaded.Distribution.Bundles = append(loaded.Distribution.Bundles, cfgFile.Bundle)
		loaded.Launch = launchConfig(cfgFile)
		for _, app := range cfgFile.Bundle.Apps {
			for _, source := range app.Sources {
				bundle, ok, err := loadSource(ctx, root, source)
				if err != nil {
					loaded.Diagnostics = append(loaded.Diagnostics, diagnostic(resource.SourceRef{Location: source.Location}, err))
					continue
				}
				if ok {
					loaded.Distribution.Bundles = append(loaded.Distribution.Bundles, bundle)
				}
			}
			if app.Discovery.IncludeGlobalUserResources {
				loaded.Distribution.Bundles = append(loaded.Distribution.Bundles, loadUserResources(ctx)...)
			}
		}
	}
	if bundle, ok, err := loadRootAgentDir(ctx, root); err != nil {
		loaded.Diagnostics = append(loaded.Diagnostics, diagnostic(resource.SourceRef{Location: filepath.Join(root, ".agents")}, err))
	} else if ok {
		loaded.Distribution.Bundles = append(loaded.Distribution.Bundles, bundle)
	}
	loaded.Distribution.Spec = specFor(root, loaded.Distribution.Bundles)
	loaded.Distribution.Spec = mergeDistributionSpec(loaded.Distribution.Spec, distributionConfig)
	loaded.Distribution.Runtime = localruntime.Runtime{
		DefaultSession:      loaded.Distribution.Spec.DefaultSession,
		DefaultConversation: loaded.Distribution.Spec.DefaultConversation,
	}
	return loaded, nil
}

func mergeDistributionSpec(base, override coredistribution.Spec) coredistribution.Spec {
	if override.Name != "" {
		base.Name = override.Name
	}
	if override.Title != "" {
		base.Title = override.Title
	}
	if override.Description != "" {
		base.Description = override.Description
	}
	if override.Author != "" {
		base.Author = override.Author
	}
	if override.Version != "" {
		base.Version = override.Version
	}
	if override.DefaultSession.Name != "" {
		base.DefaultSession = override.DefaultSession
	}
	if override.DefaultConversation.ID != "" {
		base.DefaultConversation = override.DefaultConversation
	}
	if override.DefaultModel.Provider != "" {
		base.DefaultModel.Provider = override.DefaultModel.Provider
	}
	if override.DefaultModel.Model != "" {
		base.DefaultModel.Model = override.DefaultModel.Model
	}
	if override.DefaultModel.UseCase != "" {
		base.DefaultModel.UseCase = override.DefaultModel.UseCase
	}
	if hasSurfaceOverride(override.Surfaces) {
		base.Surfaces = override.Surfaces
	}
	if len(override.Build.Assets) > 0 || override.Build.Docker != nil {
		base.Build = override.Build
	}
	if len(override.Commands) > 0 {
		base.Commands = append([]coredistribution.Command(nil), override.Commands...)
	}
	if len(override.Metadata) > 0 {
		base.Metadata = cloneStringMap(override.Metadata)
	}
	return base
}

func hasSurfaceOverride(s coredistribution.Surfaces) bool {
	return s.CLI || s.REPL || s.OneShot || s.Serve || s.Deploy || s.Validate || s.Status || s.Discover
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
		return resource.ContributionBundle{}, false, fmt.Errorf("remote source %q is not supported by local distribution loading", location)
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

func specFor(root string, bundles []resource.ContributionBundle) coredistribution.Spec {
	spec := coredistribution.Spec{
		Name: strings.TrimSpace(filepath.Base(root)),
		Surfaces: coredistribution.Surfaces{
			CLI:     true,
			REPL:    true,
			OneShot: true,
			Serve:   true,
		},
	}
	if spec.Name == "" || spec.Name == "." || spec.Name == string(filepath.Separator) {
		spec.Name = "local"
	}
	if appSpec, ok := firstApp(bundles); ok {
		if appSpec.Name != "" {
			spec.Name = string(appSpec.Name)
		}
		spec.Description = appSpec.Description
		spec.DefaultSession = appSpec.DefaultSession
		if spec.DefaultSession.Name == "" && appSpec.DefaultAgent.Name != "" {
			spec.DefaultSession = coresession.Ref{Name: "default"}
		}
		spec.DefaultModel = coredistribution.ModelDefault{
			Provider: appSpec.Model.Provider,
			Model:    appSpec.Model.Model,
			UseCase:  appSpec.Model.UseCase,
		}
	}
	if spec.DefaultSession.Name == "" {
		if session, ok := firstSession(bundles); ok {
			spec.DefaultSession = coresession.Ref{Name: session.Name}
		}
	}
	if spec.DefaultConversation.ID == "" && spec.Name != "" {
		spec.DefaultConversation = channel.ConversationRef{ID: "agentsdk-" + spec.Name}
	}
	if spec.DefaultModel.Provider == "" {
		spec.DefaultModel.Provider = "openai"
	}
	return spec
}

func firstApp(bundles []resource.ContributionBundle) (coreapp.Spec, bool) {
	for _, bundle := range bundles {
		if len(bundle.Apps) > 0 {
			return bundle.Apps[0], true
		}
	}
	return coreapp.Spec{}, false
}

func firstSession(bundles []resource.ContributionBundle) (coresession.Spec, bool) {
	for _, bundle := range bundles {
		if len(bundle.Sessions) > 0 {
			return bundle.Sessions[0], true
		}
	}
	return coresession.Spec{}, false
}

func launchConfig(file appconfig.File) distribution.LaunchConfig {
	connectors := map[string]distribution.Connector{}
	for name, connector := range file.Connectors {
		connectors[name] = distribution.Connector{Kind: connector.Kind}
	}
	return distribution.LaunchConfig{
		Connectors: connectors,
		Listeners:  listeners(file.Daemon.Listeners),
		Channels:   channels(file.Daemon.Channels),
		Workspace:  workspace(file.Runtime.Workspace),
	}
}

func workspace(doc appconfig.WorkspaceConfig) distribution.WorkspaceConfig {
	out := distribution.WorkspaceConfig{ScratchRoot: strings.TrimSpace(doc.ScratchRoot)}
	for _, root := range doc.Roots {
		out.Roots = append(out.Roots, distribution.WorkspaceRoot{
			Name:   strings.TrimSpace(root.Name),
			Path:   strings.TrimSpace(root.Path),
			Access: strings.TrimSpace(root.Access),
			Create: root.Create,
		})
	}
	return out
}

func listeners(docs []appconfig.ListenerDoc) []distribution.Listener {
	out := make([]distribution.Listener, 0, len(docs))
	for _, doc := range docs {
		out = append(out, distribution.Listener{
			Name: doc.Name,
			Type: doc.Type,
			Addr: doc.Addr,
			Auth: cloneMap(doc.Auth),
		})
	}
	return out
}

func channels(docs []appconfig.ChannelDoc) []distribution.Channel {
	out := make([]distribution.Channel, 0, len(docs))
	for _, doc := range docs {
		out = append(out, distribution.Channel{
			Name:      doc.Name,
			Type:      doc.Type,
			Connector: doc.Connector,
			Listener:  doc.Listener,
			Session:   doc.Session,
			Access: distribution.Access{
				Mode:             doc.Access.Mode,
				AllowUsers:       append([]string(nil), doc.Access.AllowUsers...),
				DenyUsers:        append([]string(nil), doc.Access.DenyUsers...),
				AllowChannels:    append([]string(nil), doc.Access.AllowChannels...),
				DenyChannels:     append([]string(nil), doc.Access.DenyChannels...),
				AllowKinds:       append([]string(nil), doc.Access.AllowKinds...),
				DefaultTrust:     doc.Access.DefaultTrust,
				Operators:        append([]string(nil), doc.Access.Operators...),
				InternalUsers:    append([]string(nil), doc.Access.InternalUsers...),
				InternalChannels: append([]string(nil), doc.Access.InternalChannels...),
				Sharing:          doc.Access.Sharing,
			},
		})
	}
	return out
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
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

func diagnostic(source resource.SourceRef, err error) resource.Diagnostic {
	return resource.Diagnostic{Severity: resource.SeverityError, Source: source, Message: err.Error()}
}
