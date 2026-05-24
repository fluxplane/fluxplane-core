package deploy

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/fluxplane/engine/orchestration/distribution"
)

func writeHelmChart(loaded distribution.Loaded, output, image, release, namespace, imagePullPolicy, envSecretName, runtimeSecretName, authPath string, appRuntime appRuntimeOptions, nodeSelectors []string, values map[string]string, dryRun, force bool, out io.Writer) ([]string, error) {
	name := kubernetesName(firstNonEmpty(release, loaded.Distribution.Spec.Name, "app"))
	namespace = kubernetesName(firstNonEmpty(namespace, name))
	repository, tag := splitImageTag(image)
	valuesImage := "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
	rendered, err := kubernetesContent(loaded, kubernetesRenderOptions{
		Name:              name,
		Namespace:         namespace,
		Image:             valuesImage,
		ImagePullPolicy:   imagePullPolicy,
		EnvSecretName:     "{{ .Values.envSecret.name }}",
		RuntimeSecretName: "{{ .Values.runtimeSecret.name }}",
		AuthPath:          authPath,
		AppRuntime:        appRuntime,
		NodeSelectors:     nodeSelectors,
		IncludeRegistry:   false,
		OmitNamespace:     true,
	})
	if err != nil {
		return nil, err
	}
	chartEnvSecretName := ""
	if rendered.SecretName != "" {
		chartEnvSecretName = kubernetesName(firstNonEmpty(envSecretName, name+"-env"))
	}
	chartRuntimeSecretName := ""
	if rendered.RuntimeSecretName != "" {
		chartRuntimeSecretName = kubernetesName(firstNonEmpty(runtimeSecretName, defaultRuntimeStack))
	}
	files := map[string]string{
		filepath.Join(output, "Chart.yaml"):            helmChartYAML(name),
		filepath.Join(output, "values.yaml"):           helmValuesYAML(repository, tag, namespace, chartEnvSecretName, rendered.SecretName != "", chartRuntimeSecretName, rendered.RuntimeSecretName != "", values),
		filepath.Join(output, "templates", "app.yaml"): helmTemplateAppYAML(rendered.Content, namespace, rendered.SecretName != "", rendered.RuntimeSecretName != ""),
	}
	paths := make([]string, 0, len(files))
	for _, path := range sortedKeys(files) {
		paths = append(paths, path)
		if err := maybeWriteFile(path, files[path], 0o600, dryRun, force, out); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func helmChartYAML(name string) string {
	return fmt.Sprintf("apiVersion: v2\nname: %s\ndescription: Fluxplane app deployment chart\ntype: application\nversion: 0.1.0\nappVersion: \"0.1.0\"\n", name)
}

func helmValuesYAML(repository, tag, namespace, envSecretName string, envSecretEnabled bool, runtimeSecretName string, runtimeSecretEnabled bool, values map[string]string) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "image:\n  repository: %s\n  tag: %s\nnamespace: %s\n", repository, tag, namespace)
	_, _ = fmt.Fprintf(&b, "envSecret:\n  enabled: %t\n", envSecretEnabled)
	if envSecretName != "" {
		_, _ = fmt.Fprintf(&b, "  name: %s\n", envSecretName)
	}
	_, _ = fmt.Fprintf(&b, "runtimeSecret:\n  enabled: %t\n", runtimeSecretEnabled)
	if runtimeSecretName != "" {
		_, _ = fmt.Fprintf(&b, "  name: %s\n", runtimeSecretName)
	}
	for _, key := range sortedKeys(values) {
		value := strings.TrimSpace(values[key])
		if strings.TrimSpace(key) == "" || value == "" {
			continue
		}
		_, _ = fmt.Fprintf(&b, "%s: %s\n", key, value)
	}
	return b.String()
}

func helmTemplateAppYAML(content, namespace string, hasEnvSecret, hasRuntimeSecret bool) string {
	content = helmTemplateNamespaceYAML(content, namespace)
	if !hasEnvSecret && !hasRuntimeSecret {
		return content
	}
	content = strings.Replace(content,
		"        - secretRef:\n            name: '{{ .Values.envSecret.name }}'\n",
		"        {{ if .Values.envSecret.enabled }}\n        - secretRef:\n            name: {{ .Values.envSecret.name | quote }}\n        {{ end }}\n",
		1)
	content = strings.Replace(content,
		"        - secretRef:\n            name: '{{ .Values.runtimeSecret.name }}'\n",
		"        {{ if .Values.runtimeSecret.enabled }}\n        - secretRef:\n            name: {{ .Values.runtimeSecret.name | quote }}\n        {{ end }}\n",
		1)
	return content
}

func helmTemplateNamespaceYAML(content, namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return content
	}
	return strings.ReplaceAll(content, "namespace: "+namespace+"\n", "namespace: {{ .Values.namespace | quote }}\n")
}

func splitImageTag(image string) (string, string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return defaultAppImage, "latest"
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return image[:lastColon], image[lastColon+1:]
	}
	return image, "latest"
}
