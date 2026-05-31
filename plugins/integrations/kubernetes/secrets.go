package kubernetes

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sharedsecret "github.com/fluxplane/fluxplane-secret"
)

type kubernetesSecretResolver struct {
	plugin Plugin
}

func (r kubernetesSecretResolver) ResolveSecret(ctx context.Context, ref sharedsecret.Ref) (sharedsecret.Material, bool, error) {
	ref = ref.Normalize()
	if ref.Scheme != sharedsecret.SchemeKubernetes {
		return sharedsecret.Material{}, false, nil
	}
	namespace := ref.Plugin
	secretName := ref.Instance
	key := string(ref.Slot)
	if namespace == "" || secretName == "" || key == "" {
		return sharedsecret.Material{}, false, fmt.Errorf("kubernetes secret ref must include namespace, secret name, and key")
	}
	if err := r.plugin.policyFromConfigOnly().AuthorizeNamespace(namespace); err != nil {
		return sharedsecret.Material{}, false, err
	}
	client, err := r.plugin.clientset(ctx)
	if err != nil {
		return sharedsecret.Material{}, false, err
	}
	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return sharedsecret.Material{}, false, fmt.Errorf("kubernetes secret %s/%s: %w", namespace, secretName, err)
	}
	value, ok := secret.Data[key]
	if !ok {
		return sharedsecret.Material{}, false, nil
	}
	if len(value) == 0 {
		return sharedsecret.Material{}, false, nil
	}
	return sharedsecret.Material{Kind: sharedsecret.KindBasic, Value: value}, true, nil
}

func (p Plugin) policyFromConfigOnly() namespacePolicy {
	cfg := NormalizeConfig(p.cfg)
	if cfg.AllNamespaces {
		return namespacePolicy{AllNamespaces: true}
	}
	if len(cfg.Namespaces) > 0 {
		return namespacePolicy{Namespaces: cfg.Namespaces}
	}
	return namespacePolicy{AllNamespaces: true}
}
