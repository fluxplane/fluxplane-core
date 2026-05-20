package kubernetes

import (
	"context"
	"strings"
	"time"

	coreevidence "github.com/fluxplane/agentruntime/core/evidence"
	"github.com/fluxplane/agentruntime/core/resource"
	runtimeevidence "github.com/fluxplane/agentruntime/runtime/evidence"
)

const (
	kubernetesObservationKind = "kubernetes.context"
	kubernetesObserverName    = "kubernetes.context"
	kubernetesDeriverName     = "kubernetes.assertions"
)

type kubernetesObserver struct {
	plugin Plugin
}

func (o kubernetesObserver) Spec() coreevidence.ObserverSpec {
	return kubernetesObserverSpec(o.plugin.ref)
}

func (o kubernetesObserver) Observe(ctx context.Context, _ runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	content := map[string]any{
		"configured":          false,
		"available":           false,
		"context":             o.plugin.cfg.Context,
		"namespaces":          o.plugin.cfg.Namespaces,
		"all_namespaces":      o.plugin.cfg.AllNamespaces,
		"in_cluster":          o.plugin.cfg.InCluster,
		"kubeconfig_selected": o.plugin.cfg.Kubeconfig != "" || o.plugin.cfg.KubeconfigEnv != "",
	}
	namespace := ""
	if o.plugin.client != nil {
		content["configured"] = true
		content["available"] = true
		namespace = firstNamespace(o.plugin.cfg)
	} else if cfg, discoveredNamespace, err := o.plugin.restConfig(ctx); err == nil && cfg != nil {
		content["configured"] = true
		content["available"] = true
		namespace = discoveredNamespace
	}
	if namespace == "" {
		namespace = firstNamespace(o.plugin.cfg)
	}
	if namespace != "" {
		content["namespace"] = namespace
	}
	return []coreevidence.Observation{{
		ID:          "integration:kubernetes:" + observerInstance(o.plugin.ref),
		Environment: coreevidence.Ref{Name: coreevidence.Name(Name)},
		Kind:        kubernetesObservationKind,
		Scope:       kubernetesScope(o.plugin.ref),
		Content:     content,
		At:          time.Now().UTC(),
	}}, nil
}

type kubernetesAssertionDeriver struct{}

func (kubernetesAssertionDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return kubernetesAssertionDeriverSpec()
}

func (kubernetesAssertionDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if observation.Kind != kubernetesObservationKind {
			continue
		}
		content, _ := observation.Content.(map[string]any)
		if boolContent(content, "configured") {
			out = append(out, coreevidence.Assertion{
				Kind:           "integration.configured",
				Target:         Name,
				Subject:        coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: Name},
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: []string{observation.ID},
			})
		}
		if boolContent(content, "available") {
			out = append(out, coreevidence.Assertion{
				Kind:           "integration.available",
				Target:         Name,
				Subject:        coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: Name},
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: []string{observation.ID},
			})
		}
	}
	return out, nil
}

func kubernetesObserverSpec(ref resource.PluginRef) coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:        kubernetesObserverName,
		Description: "Reports non-secret Kubernetes context and namespace availability for a selected Kubernetes plugin instance.",
		Environment: coreevidence.Ref{
			Name: coreevidence.Name(Name),
		},
		Phase:           coreevidence.PhaseTurn,
		ObservableKinds: []string{kubernetesObservationKind},
		Dynamic:         true,
		Annotations: map[string]string{
			"plugin":   Name,
			"instance": observerInstance(ref),
		},
	}
}

func kubernetesAssertionDeriverSpec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             kubernetesDeriverName,
		Description:      "Derives Kubernetes integration configured/available assertions from Kubernetes context observations.",
		ObservationKinds: []string{kubernetesObservationKind},
		Assertions: []coreevidence.AssertionTemplate{
			{Kind: "integration.configured", Target: Name, Subject: coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: Name}},
			{Kind: "integration.available", Target: Name, Subject: coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: Name}},
		},
	}
}

func firstNamespace(cfg Config) string {
	cfg = NormalizeConfig(cfg)
	if len(cfg.Namespaces) > 0 {
		return cfg.Namespaces[0]
	}
	if cfg.AllNamespaces {
		return ""
	}
	return "default"
}

func observerInstance(ref resource.PluginRef) string {
	if instance := strings.TrimSpace(ref.InstanceName()); instance != "" {
		return instance
	}
	return Name
}

func kubernetesScope(ref resource.PluginRef) string {
	return "integration:kubernetes:" + observerInstance(ref)
}

func boolContent(content map[string]any, key string) bool {
	value, _ := content[key].(bool)
	return value
}
