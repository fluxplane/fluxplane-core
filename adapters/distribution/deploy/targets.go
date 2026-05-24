package deploy

import (
	"fmt"
	"sort"
	"strings"

	coredistribution "github.com/fluxplane/engine/core/distribution"
)

const (
	buildKindBinary             = "binary"
	buildKindDockerfile         = "dockerfile"
	buildKindDockerImage        = "docker-image"
	buildKindDockerCompose      = "docker-compose"
	buildKindKubernetesManifest = "kubernetes-manifest"
	buildKindHelmChart          = "helm-chart"
	buildKindDocumentation      = "documentation"
	buildKindRuntimeStack       = "runtime-stack"

	deployKindDockerCompose = "docker-compose"
	deployKindKubectl       = "kubectl"
	deployKindHelm          = "helm"
)

var defaultBuildKinds = []string{
	buildKindBinary,
	buildKindDockerfile,
	buildKindDockerCompose,
	buildKindDockerImage,
	buildKindDocumentation,
}

func resolveBuildTargets(spec coredistribution.Spec, values []string) ([]namedBuildTarget, error) {
	raw := cleanStrings(values)
	if len(raw) == 0 {
		raw = []string{"all"}
	}
	var names []string
	for _, value := range raw {
		if value == "all" {
			if len(spec.Build.Targets) > 0 {
				for name := range spec.Build.Targets {
					names = append(names, name)
				}
				sort.Strings(names)
			} else {
				names = append(names, defaultBuildKinds...)
			}
			continue
		}
		names = append(names, value)
	}
	seen := map[string]struct{}{}
	out := make([]namedBuildTarget, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		target, ok := spec.Build.Targets[name]
		if !ok {
			if !isBuildKind(name) {
				return nil, fmt.Errorf("distribution build: unknown build target %q", name)
			}
			target = coredistribution.BuildTargetSpec{Kind: name}
		}
		if strings.TrimSpace(target.Kind) == "" {
			return nil, fmt.Errorf("distribution build: target %q has no kind", name)
		}
		if !isBuildKind(target.Kind) {
			return nil, fmt.Errorf("distribution build: target %q has unsupported kind %q", name, target.Kind)
		}
		out = append(out, namedBuildTarget{Name: name, Spec: target})
	}
	return out, nil
}

func resolveDeployTarget(spec coredistribution.Spec, value string) (namedDeployTarget, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		name = "local"
	}
	if target, ok := spec.Deploy.Targets[name]; ok {
		if strings.TrimSpace(target.Kind) == "" {
			return namedDeployTarget{}, fmt.Errorf("distribution deploy: target %q has no kind", name)
		}
		if !isDeployKind(target.Kind) {
			return namedDeployTarget{}, fmt.Errorf("distribution deploy: target %q has unsupported kind %q", name, target.Kind)
		}
		return namedDeployTarget{Name: name, Spec: target}, nil
	}
	return namedDeployTarget{}, fmt.Errorf("distribution deploy: unknown deploy target %q; declare distribution.deploy.targets.%s in fluxplane.yaml", name, name)
}

func isBuildKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case buildKindBinary, buildKindDockerfile, buildKindDockerImage, buildKindDockerCompose, buildKindKubernetesManifest, "kubernetes", buildKindHelmChart, buildKindDocumentation, buildKindRuntimeStack, "docker-base":
		return true
	default:
		return false
	}
}

func isDeployKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case deployKindDockerCompose, deployKindKubectl, deployKindHelm:
		return true
	default:
		return false
	}
}

type namedBuildTarget struct {
	Name string
	Spec coredistribution.BuildTargetSpec
}

type namedDeployTarget struct {
	Name string
	Spec coredistribution.DeployTargetSpec
}
