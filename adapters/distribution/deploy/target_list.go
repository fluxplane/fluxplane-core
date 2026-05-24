package deploy

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
	coredistribution "github.com/fluxplane/engine/core/distribution"
)

// TargetListOptions configures build/deploy target discovery.
type TargetListOptions struct {
	AppDir   string
	Profile  string
	Profiles []string
}

// TargetListResult describes available distribution targets.
type TargetListResult struct {
	AppDir  string             `json:"app_dir"`
	Profile string             `json:"profile,omitempty"`
	Build   []BuildTargetInfo  `json:"build,omitempty"`
	Deploy  []DeployTargetInfo `json:"deploy,omitempty"`
	Index   string             `json:"index,omitempty"`
}

// BuildTargetInfo is the display form of one build target.
type BuildTargetInfo struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Description string   `json:"description,omitempty"`
	Output      string   `json:"output,omitempty"`
	Image       string   `json:"image,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Artifact    string   `json:"artifact,omitempty"`
	Status      string   `json:"status,omitempty"`
}

// DeployTargetInfo is the display form of one deploy target.
type DeployTargetInfo struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Description string   `json:"description,omitempty"`
	Build       []string `json:"build,omitempty"`
	Artifact    string   `json:"artifact,omitempty"`
	Status      string   `json:"status,omitempty"`
}

// ListTargets resolves available build and deploy targets without running a
// build or deployment.
func ListTargets(ctx context.Context, opts TargetListOptions) (TargetListResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.LoadWithOptions(ctx, appDir, distlocal.LoadOptions{Profile: opts.Profile, Profiles: opts.Profiles})
	if err != nil {
		return TargetListResult{}, err
	}
	buildTargets, err := resolveBuildTargets(loaded.Distribution.Spec, []string{"all"})
	if err != nil {
		return TargetListResult{}, err
	}
	index, indexErr := readArtifactIndex(loaded.Root)
	result := TargetListResult{
		AppDir:  loaded.Root,
		Profile: loaded.Profile,
		Index:   artifactIndexPath(loaded.Root),
		Build:   make([]BuildTargetInfo, 0, len(buildTargets)),
	}
	for _, target := range buildTargets {
		info := buildTargetInfo(loaded.Root, loaded.Distribution.Spec, target)
		if indexErr == nil {
			if artifact, ok := artifactByTarget(index, target.Name); ok {
				info.Artifact = displayTargetPath(loaded.Root, firstNonEmpty(artifact.Path, firstNonEmpty(artifact.Paths...)))
				if artifactFilesExist(loaded.Root, artifact) {
					info.Status = "built"
				} else {
					info.Status = "missing"
				}
			}
		}
		result.Build = append(result.Build, info)
	}
	deployTargets := configuredDeployTargets(loaded.Distribution.Spec)
	result.Deploy = make([]DeployTargetInfo, 0, len(deployTargets))
	for _, target := range deployTargets {
		info := deployTargetInfo(target)
		if indexErr == nil {
			info.Status = deployArtifactStatus(loaded.Root, index, target.Spec.Build)
		}
		result.Deploy = append(result.Deploy, info)
	}
	return result, nil
}

func buildTargetInfo(root string, spec coredistribution.Spec, target namedBuildTarget) BuildTargetInfo {
	kind := normalizedBuildKind(target.Spec.Kind)
	info := BuildTargetInfo{
		Name:        target.Name,
		Kind:        kind,
		Description: target.Spec.Description,
		Output:      displayTargetPath(root, buildTargetOutput(root, spec, target.Spec)),
	}
	if buildTargetUsesImage(kind) {
		info.Tags = resolveTargetTags(spec, target.Spec, AppBuildOptions{})
		info.Image = firstTag(info.Tags)
	}
	return info
}

func buildTargetUsesImage(kind string) bool {
	switch kind {
	case buildKindDockerImage, buildKindDockerCompose, buildKindKubernetesManifest, buildKindHelmChart:
		return true
	default:
		return false
	}
}

func deployTargetInfo(target namedDeployTarget) DeployTargetInfo {
	return DeployTargetInfo{
		Name:        target.Name,
		Kind:        target.Spec.Kind,
		Description: target.Spec.Description,
		Build:       append([]string(nil), target.Spec.Build...),
		Artifact:    deployTargetArtifact(target.Spec),
	}
}

func configuredDeployTargets(spec coredistribution.Spec) []namedDeployTarget {
	names := make([]string, 0, len(spec.Deploy.Targets))
	for name := range spec.Deploy.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]namedDeployTarget, 0, len(names))
	for _, name := range names {
		target := spec.Deploy.Targets[name]
		if strings.TrimSpace(target.Kind) == "" || !isDeployKind(target.Kind) {
			continue
		}
		out = append(out, namedDeployTarget{Name: name, Spec: target})
	}
	return out
}

func buildTargetOutput(root string, spec coredistribution.Spec, target coredistribution.BuildTargetSpec) string {
	name := distributionName(spec)
	switch normalizedBuildKind(target.Kind) {
	case buildKindBinary:
		return targetOutput(root, target.Output, filepath.Join("bin", composeServiceName(name)))
	case buildKindDockerfile:
		return targetOutput(root, target.Output, "Dockerfile")
	case buildKindDockerImage:
		return targetOutput(root, target.Dockerfile, "Dockerfile")
	case buildKindDockerCompose:
		return targetOutput(root, target.Output, "docker-compose.yaml")
	case buildKindKubernetesManifest:
		return targetOutput(root, target.Output, filepath.Join(".deploy", "kubernetes.yaml"))
	case buildKindHelmChart:
		return targetOutput(root, target.Output, filepath.Join("charts", composeServiceName(name)))
	case buildKindDocumentation:
		return targetOutput(root, target.Output, composeServiceName(name)+".md")
	default:
		return strings.TrimSpace(target.Output)
	}
}

func deployTargetArtifact(target coredistribution.DeployTargetSpec) string {
	switch target.Kind {
	case deployKindDockerCompose:
		return target.ComposeFile
	case deployKindKubectl:
		return target.Manifest
	case deployKindHelm:
		return target.Chart
	default:
		return ""
	}
}

func deployArtifactStatus(root string, index ArtifactIndex, targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	for _, target := range targets {
		artifact, ok := artifactByTarget(index, target)
		if !ok || !artifactFilesExist(root, artifact) {
			return "missing"
		}
	}
	return "built"
}

func displayTargetPath(root, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return filepath.ToSlash(rel)
}
