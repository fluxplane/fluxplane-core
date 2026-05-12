package resourcefs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
)

const DefaultManifestName = "agentruntime.json"

// LoadDir loads the default resource manifest from dir.
func LoadDir(ctx context.Context, dir string) (resource.ContributionBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return resource.ContributionBundle{}, err
	}
	path := filepath.Join(dir, DefaultManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		return resource.ContributionBundle{}, fmt.Errorf("resourcefs: read manifest: %w", err)
	}
	return DecodeManifest(path, data)
}

// DecodeManifest decodes one local manifest.
func DecodeManifest(path string, data []byte) (resource.ContributionBundle, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return resource.ContributionBundle{}, fmt.Errorf("resourcefs: decode manifest: %w", err)
	}
	source := resource.SourceRef{
		ID:       "resourcefs:" + filepath.Clean(path),
		Scope:    resource.ScopeProject,
		Location: filepath.Clean(path),
		Trust: policy.Trust{
			Kind:  policy.TrustSource,
			Level: policy.TrustVerified,
		},
	}
	bundle := resource.ContributionBundle{
		Source:     source,
		Operations: append([]operation.Spec(nil), manifest.Operations...),
		Plugins:    append([]resource.PluginRef(nil), manifest.Plugins...),
	}
	for _, raw := range manifest.Commands {
		spec, err := raw.Spec()
		if err != nil {
			return resource.ContributionBundle{}, err
		}
		bundle.Commands = append(bundle.Commands, spec)
	}
	return bundle, nil
}

// Manifest is the first filesystem resource format for the rewrite. It is
// intentionally small and maps to core contribution types.
type Manifest struct {
	Operations []operation.Spec     `json:"operations,omitempty"`
	Commands   []Command            `json:"commands,omitempty"`
	Plugins    []resource.PluginRef `json:"plugins,omitempty"`
}

// Command is an ergonomic command declaration for filesystem manifests.
type Command struct {
	Path        []string                `json:"path"`
	Description string                  `json:"description,omitempty"`
	Operation   string                  `json:"operation"`
	Policy      policy.InvocationPolicy `json:"policy,omitempty"`
	Annotations map[string]string       `json:"annotations,omitempty"`
}

// Spec converts the filesystem command shape to a core command spec.
func (c Command) Spec() (command.Spec, error) {
	path := normalizePath(c.Path)
	if len(path) == 0 {
		return command.Spec{}, fmt.Errorf("resourcefs: command path is empty")
	}
	if c.Operation == "" {
		return command.Spec{}, fmt.Errorf("resourcefs: command %q operation is empty", strings.Join(path, "/"))
	}
	return command.Spec{
		Path:        command.Path(path),
		Description: c.Description,
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: operation.Name(c.Operation)},
		},
		Policy:      c.Policy,
		Annotations: c.Annotations,
	}, nil
}

func normalizePath(path []string) []string {
	out := make([]string, 0, len(path))
	for _, part := range path {
		part = strings.Trim(strings.TrimSpace(part), "/")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
