package deploy

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/fluxplane/engine/orchestration/distribution"
)

func writeRuntimeStackHelmChart(loaded distribution.Loaded, output, release, namespace, runtimeSecretName string, values map[string]string, dryRun, force bool, out io.Writer) ([]string, error) {
	name := kubernetesName(firstNonEmpty(release, defaultRuntimeStack))
	namespace = kubernetesName(firstNonEmpty(namespace, name))
	secretName := kubernetesRuntimeSecretForLoaded(loaded, distributionName(loaded.Distribution.Spec), runtimeSecretName).Name
	if secretName == "" {
		secretName = kubernetesName(defaultRuntimeStack)
	}
	var docs []string
	if composeUsesMySQL(loaded.Launch) {
		docs = append(docs, splitYAMLDocuments(kubernetesMySQL(namespace, "{{ .Values.runtimeSecret.name }}"))...)
	}
	if composeUsesNATS(loaded.Launch) {
		docs = append(docs, splitYAMLDocuments(kubernetesNATS(namespace))...)
	}
	files := map[string]string{
		filepath.Join(output, "Chart.yaml"):                runtimeStackChartYAML(name),
		filepath.Join(output, "values.yaml"):               runtimeStackValuesYAML(namespace, secretName, values),
		filepath.Join(output, "templates", "runtime.yaml"): runtimeStackTemplateYAML(joinYAMLDocuments(docs), namespace),
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

func runtimeStackChartYAML(name string) string {
	return fmt.Sprintf("apiVersion: v2\nname: %s\ndescription: Fluxplane temporary runtime dependency chart\ntype: application\nversion: 0.1.0\nappVersion: \"0.1.0\"\n", name)
}

func runtimeStackValuesYAML(namespace, runtimeSecretName string, values map[string]string) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "namespace: %s\nruntimeSecret:\n  name: %s\n", namespace, runtimeSecretName)
	for _, key := range sortedKeys(values) {
		value := strings.TrimSpace(values[key])
		if strings.TrimSpace(key) == "" || value == "" {
			continue
		}
		_, _ = fmt.Fprintf(&b, "%s: %s\n", key, value)
	}
	return b.String()
}

func runtimeStackTemplateYAML(content, namespace string) string {
	content = helmTemplateNamespaceYAML(content, namespace)
	return strings.ReplaceAll(content, "name: '{{ .Values.runtimeSecret.name }}'", "name: {{ .Values.runtimeSecret.name | quote }}")
}
