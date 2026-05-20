package kubernetes

import (
	"context"
	"strings"
	"time"

	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/resource"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
)

const (
	kubernetesObservationKind = "kubernetes.context"
	kubernetesObserverName    = "kubernetes.context"
	kubernetesDeriverName     = "kubernetes.signals"
)

type kubernetesObserver struct {
	plugin Plugin
}

func (o kubernetesObserver) Spec() coreenvironment.ObserverSpec {
	return kubernetesObserverSpec(o.plugin.ref)
}

func (o kubernetesObserver) Observe(ctx context.Context, _ runtimeenvironment.ObservationRequest) ([]coreenvironment.Observation, error) {
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
	return []coreenvironment.Observation{{
		ID:          "integration:kubernetes:" + observerInstance(o.plugin.ref),
		Environment: coreenvironment.Ref{Name: coreenvironment.Name(Name)},
		Kind:        kubernetesObservationKind,
		Scope:       kubernetesScope(o.plugin.ref),
		Content:     content,
		At:          time.Now().UTC(),
	}}, nil
}

type kubernetesSignalDeriver struct{}

func (kubernetesSignalDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return kubernetesSignalDeriverSpec()
}

func (kubernetesSignalDeriver) Derive(_ context.Context, req runtimeenvironment.SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	var out []coreenvironment.Signal
	for _, observation := range req.Observations {
		if observation.Kind != kubernetesObservationKind {
			continue
		}
		content, _ := observation.Content.(map[string]any)
		if boolContent(content, "configured") {
			out = append(out, coreenvironment.Signal{
				Kind:           "integration.configured",
				Target:         Name,
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: []string{observation.ID},
			})
		}
		if boolContent(content, "available") {
			out = append(out, coreenvironment.Signal{
				Kind:           "integration.available",
				Target:         Name,
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: []string{observation.ID},
			})
		}
	}
	return out, nil
}

func kubernetesObserverSpec(ref resource.PluginRef) coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{
		Name:        kubernetesObserverName,
		Description: "Reports non-secret Kubernetes context and namespace availability for a selected Kubernetes plugin instance.",
		Environment: coreenvironment.Ref{
			Name: coreenvironment.Name(Name),
		},
		Phase:           coreenvironment.PhaseTurn,
		ObservableKinds: []string{kubernetesObservationKind},
		Dynamic:         true,
		Annotations: map[string]string{
			"plugin":   Name,
			"instance": observerInstance(ref),
		},
	}
}

func kubernetesSignalDeriverSpec() coreenvironment.SignalDeriverSpec {
	return coreenvironment.SignalDeriverSpec{
		Name:             kubernetesDeriverName,
		Description:      "Derives Kubernetes integration configured/available signals from Kubernetes context observations.",
		ObservationKinds: []string{kubernetesObservationKind},
		Signals: []coreenvironment.SignalTemplate{
			{Kind: "integration.configured", Target: Name},
			{Kind: "integration.available", Target: Name},
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
