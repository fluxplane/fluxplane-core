// Package local loads filesystem paths as ephemeral distributions.
package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/engine/adapters/distribution/localruntime"
	"github.com/fluxplane/engine/adapters/resources/agentdir"
	"github.com/fluxplane/engine/adapters/resources/appconfig"
	coreapp "github.com/fluxplane/engine/core/app"
	"github.com/fluxplane/engine/core/channel"
	coredistribution "github.com/fluxplane/engine/core/distribution"
	"github.com/fluxplane/engine/core/policy"
	"github.com/fluxplane/engine/core/resource"
	coresession "github.com/fluxplane/engine/core/session"
	"github.com/fluxplane/engine/orchestration/distribution"
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
				userBundles, userDiagnostics := loadUserResources(ctx)
				loaded.Distribution.Bundles = append(loaded.Distribution.Bundles, userBundles...)
				loaded.Diagnostics = append(loaded.Diagnostics, userDiagnostics...)
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

// RequestedResources is the result of resolving app-declared local resource
// roots for already-authored bundles.
type RequestedResources struct {
	Root        string
	Bundles     []resource.ContributionBundle
	Diagnostics []resource.Diagnostic
}

// LoadRequestedResources resolves Sources and Discovery policy from bundled app
// specs using the same local/appconfig loading behavior as Load.
func LoadRequestedResources(ctx context.Context, path string, bundles []resource.ContributionBundle) (RequestedResources, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return RequestedResources{}, err
	}
	root = filepath.Clean(root)
	out := RequestedResources{
		Root:    root,
		Bundles: append([]resource.ContributionBundle(nil), bundles...),
	}
	for _, bundle := range bundles {
		for _, app := range bundle.Apps {
			for _, source := range app.Sources {
				sourceBundle, ok, err := loadSource(ctx, root, source)
				if err != nil {
					out.Diagnostics = append(out.Diagnostics, diagnostic(resource.SourceRef{Location: source.Location}, err))
					continue
				}
				if ok {
					out.Bundles = append(out.Bundles, sourceBundle)
				}
			}
			if app.Discovery.IncludeGlobalUserResources {
				userBundles, userDiagnostics := loadUserResources(ctx)
				out.Bundles = append(out.Bundles, userBundles...)
				out.Diagnostics = append(out.Diagnostics, userDiagnostics...)
			}
		}
	}
	return out, nil
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
	if override.Deploy.Model != "" {
		base.Deploy.Model = override.Deploy.Model
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
	for _, name := range appconfig.DeprecatedManifestNames {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			_, err := appconfig.LoadDirFile(ctx, root)
			return appconfig.File{}, false, err
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

func loadUserResources(ctx context.Context) ([]resource.ContributionBundle, []resource.Diagnostic) {
	var out []resource.ContributionBundle
	var diagnostics []resource.Diagnostic
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, nil
	}
	for _, rel := range []string{".agents", ".claude"} {
		dir := filepath.Join(home, rel)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		bundle, err := agentdir.LoadDir(ctx, dir)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{Location: dir}, err))
			continue
		}
		bundle.Source.Scope = resource.ScopeUser
		bundle.Source.Trust = policy.Trust{Kind: policy.TrustSource, Level: policy.TrustVerified}
		out = append(out, bundle)
	}
	return out, diagnostics
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
		spec.DefaultConversation = channel.ConversationRef{ID: "agentruntime-" + spec.Name}
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
		Data:       dataConfig(file.Runtime.Data),
		Events:     eventsConfig(file.Runtime.Events),
	}
}

func dataConfig(doc appconfig.RuntimeDataDoc) distribution.DataConfig {
	return distribution.DataConfig{
		Store: distribution.DataStoreConfig{
			Kind:   strings.TrimSpace(doc.Store.Kind),
			DSN:    strings.TrimSpace(doc.Store.DSN),
			DSNEnv: strings.TrimSpace(doc.Store.DSNEnv),
		},
	}
}

func eventsConfig(doc appconfig.RuntimeEventsDoc) distribution.EventsConfig {
	return distribution.EventsConfig{
		Store: distribution.EventStoreConfig{
			Kind:         strings.TrimSpace(doc.Store.Kind),
			DSN:          strings.TrimSpace(doc.Store.DSN),
			DSNEnv:       strings.TrimSpace(doc.Store.DSNEnv),
			Stream:       strings.TrimSpace(doc.Store.Stream),
			Subject:      strings.TrimSpace(doc.Store.Subject),
			CreateStream: doc.Store.CreateStream,
		},
	}
}

func workspace(doc appconfig.WorkspaceConfig) distribution.WorkspaceConfig {
	out := distribution.WorkspaceConfig{
		ScratchRoot: strings.TrimSpace(doc.ScratchRoot),
		EnvFiles:    trimStringSlice(doc.EnvFiles),
	}
	for _, root := range doc.Roots {
		out.Roots = append(out.Roots, distribution.WorkspaceRoot{
			Name:     strings.TrimSpace(root.Name),
			Path:     strings.TrimSpace(root.Path),
			Access:   strings.TrimSpace(root.Access),
			Create:   root.Create,
			EnvFiles: trimStringSlice(root.EnvFiles),
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
			Instance:  doc.Instance,
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

func trimStringSlice(input []string) []string {
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

func diagnostic(source resource.SourceRef, err error) resource.Diagnostic {
	return resource.Diagnostic{Severity: resource.SeverityError, Source: source, Message: err.Error()}
}
