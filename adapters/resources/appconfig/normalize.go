package appconfig

import (
	"fmt"
	"strings"

	"github.com/fluxplane/engine/core/activation"
	"github.com/fluxplane/engine/core/agent"
	corecontext "github.com/fluxplane/engine/core/context"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/resource"
	coreskill "github.com/fluxplane/engine/core/skill"
)

// NormalizeOptions supplies already-resolved contribution metadata used to
// expand compact appconfig authoring forms.
type NormalizeOptions struct {
	ContributionBundles []resource.ContributionBundle
}

// NormalizeBundle expands compact authored intent in one app bundle using
// supplied contribution metadata. Plugin semantics must be represented as
// resource specs such as activation sets; this package does not know concrete
// plugin implementations.
func NormalizeBundle(bundle resource.ContributionBundle, opts NormalizeOptions) (resource.ContributionBundle, error) {
	all := append([]resource.ContributionBundle{bundle}, opts.ContributionBundles...)
	index, err := newNormalizeIndex(all)
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	out := cloneBundleAgents(bundle)
	if err := normalizeBundleAgents(&out, index); err != nil {
		return resource.ContributionBundle{}, err
	}
	return out, nil
}

// NormalizeBundles expands compact authored intent across a composed bundle
// list. Every bundle is used as contribution metadata for every other bundle.
func NormalizeBundles(bundles []resource.ContributionBundle) ([]resource.ContributionBundle, error) {
	index, err := newNormalizeIndex(bundles)
	if err != nil {
		return nil, err
	}
	out := make([]resource.ContributionBundle, len(bundles))
	for i, bundle := range bundles {
		out[i] = cloneBundleAgents(bundle)
		if err := normalizeBundleAgents(&out[i], index); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type normalizeIndex struct {
	activationSets map[string][]activation.Set
	operationSets  map[string]operation.Set
	datasources    []coredatasource.Name
}

func newNormalizeIndex(bundles []resource.ContributionBundle) (normalizeIndex, error) {
	index := normalizeIndex{
		activationSets: map[string][]activation.Set{},
		operationSets:  map[string]operation.Set{},
	}
	seenDatasources := map[coredatasource.Name]bool{}
	for _, bundle := range bundles {
		for _, set := range bundle.ActivationSets {
			if err := registerActivationSet(index.activationSets, set); err != nil {
				return normalizeIndex{}, err
			}
		}
		for _, set := range bundle.OperationSets {
			name := strings.TrimSpace(set.Name)
			if name == "" {
				continue
			}
			if _, exists := index.operationSets[name]; !exists {
				index.operationSets[name] = set
			}
		}
		for _, spec := range bundle.Datasources {
			name := coredatasource.Name(strings.TrimSpace(string(spec.Name)))
			if name == "" || seenDatasources[name] {
				continue
			}
			seenDatasources[name] = true
			index.datasources = append(index.datasources, name)
		}
	}
	return index, nil
}

func registerActivationSet(sets map[string][]activation.Set, set activation.Set) error {
	names := append([]string{set.Name}, set.Aliases...)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		sets[name] = append(sets[name], set)
	}
	return nil
}

func cloneBundleAgents(bundle resource.ContributionBundle) resource.ContributionBundle {
	out := bundle
	out.Agents = append([]agent.Spec(nil), bundle.Agents...)
	return out
}

func normalizeBundleAgents(bundle *resource.ContributionBundle, index normalizeIndex) error {
	for i := range bundle.Agents {
		spec := cloneAgentSpec(bundle.Agents[i])
		for _, use := range spec.ActivationSets {
			name := strings.TrimSpace(use)
			if name == "" {
				continue
			}
			set, err := resolveActivationSet(index.activationSets, name)
			if err != nil {
				return err
			}
			if strings.TrimSpace(set.Name) == "" {
				return fmt.Errorf("appconfig: agent %q uses unknown activation set %q", spec.Name, name)
			}
			if err := applyActivationSetToAgent(&spec, set, index); err != nil {
				return fmt.Errorf("appconfig: agent %q uses %q: %w", spec.Name, name, err)
			}
		}
		bundle.Agents[i] = spec
	}
	return nil
}

func resolveActivationSet(sets map[string][]activation.Set, name string) (activation.Set, error) {
	matches := sets[name]
	if len(matches) == 0 {
		return activation.Set{}, nil
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.Name)
	}
	return activation.Set{}, fmt.Errorf("activation set reference %q is ambiguous between %s", name, strings.Join(sortedUnique(names), ", "))
}

func cloneAgentSpec(spec agent.Spec) agent.Spec {
	spec.Operations = append([]operation.Ref(nil), spec.Operations...)
	spec.ActivationSets = append([]string(nil), spec.ActivationSets...)
	spec.Tools = append([]agent.ToolRef(nil), spec.Tools...)
	spec.Commands = append([]agent.CommandRef(nil), spec.Commands...)
	spec.Datasources = append([]coredatasource.Ref(nil), spec.Datasources...)
	spec.Skills = append([]coreskill.Ref(nil), spec.Skills...)
	spec.Context = append([]corecontext.ProviderRef(nil), spec.Context...)
	return spec
}

func applyActivationSetToAgent(spec *agent.Spec, set activation.Set, index normalizeIndex) error {
	for _, target := range set.Targets {
		switch target.Kind {
		case activation.TargetOperation:
			appendOperationRef(&spec.Operations, target.Operation)
		case activation.TargetOperationSet:
			operationSet, ok := index.operationSets[strings.TrimSpace(target.OperationSet)]
			if !ok {
				return fmt.Errorf("operation set %q is unknown", target.OperationSet)
			}
			for _, ref := range operationSet.Operations {
				appendOperationRef(&spec.Operations, ref)
			}
		case activation.TargetContextProvider:
			appendContextProviderRef(&spec.Context, target.ContextProvider)
		case activation.TargetDatasource:
			appendDatasourceRef(&spec.Datasources, target.Datasource.Name)
		case activation.TargetSkill:
			appendSkillRef(&spec.Skills, target.Skill)
		}
	}
	if set.Annotations[activation.AnnotationIncludeConfiguredDatasources] == "true" {
		for _, name := range index.datasources {
			appendDatasourceRef(&spec.Datasources, name)
		}
	}
	return nil
}

func appendOperationRef(refs *[]operation.Ref, ref operation.Ref) {
	if strings.TrimSpace(string(ref.Name)) == "" {
		return
	}
	for _, existing := range *refs {
		if existing == ref {
			return
		}
	}
	*refs = append(*refs, ref)
}

func appendContextProviderRef(refs *[]corecontext.ProviderRef, ref corecontext.ProviderRef) {
	if strings.TrimSpace(string(ref.Name)) == "" {
		return
	}
	for _, existing := range *refs {
		if existing == ref {
			return
		}
	}
	*refs = append(*refs, ref)
}

func appendDatasourceRef(refs *[]coredatasource.Ref, name coredatasource.Name) {
	if strings.TrimSpace(string(name)) == "" {
		return
	}
	ref := coredatasource.Ref{Name: name}
	for _, existing := range *refs {
		if existing == ref {
			return
		}
	}
	*refs = append(*refs, ref)
}

func appendSkillRef(refs *[]coreskill.Ref, ref coreskill.Ref) {
	if strings.TrimSpace(string(ref.Name)) == "" {
		return
	}
	for _, existing := range *refs {
		if existing == ref {
			return
		}
	}
	*refs = append(*refs, ref)
}
