package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	coredatasource "github.com/fluxplane/engine/core/datasource"
)

type clusterRecord struct {
	Context        string `json:"context,omitempty"`
	Cluster        string `json:"cluster,omitempty"`
	Server         string `json:"server,omitempty"`
	Namespace      string `json:"namespace,omitempty"`
	CurrentContext bool   `json:"current_context,omitempty"`
	InCluster      bool   `json:"in_cluster,omitempty"`
}

func (a *kubernetesAccessor) clusterRecords(ctx context.Context) ([]coredatasource.Record, error) {
	records, err := a.kubeconfigClusterRecords(ctx)
	if err == nil {
		return records, nil
	}
	if a.plugin.cfg.InCluster {
		cfg, inClusterErr := rest.InClusterConfig()
		if inClusterErr != nil {
			return nil, inClusterErr
		}
		return []coredatasource.Record{a.clusterRecord(clusterRecord{Context: "in-cluster", Cluster: "in-cluster", Server: cfg.Host, InCluster: true})}, nil
	}
	return nil, err
}

func (a *kubernetesAccessor) kubeconfigClusterRecords(ctx context.Context) ([]coredatasource.Record, error) {
	path, err := a.plugin.kubeconfigPath(ctx)
	if err != nil {
		return nil, err
	}
	config, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, err
	}
	contexts := make([]string, 0, len(config.Contexts))
	for contextName := range config.Contexts {
		contexts = append(contexts, contextName)
	}
	sort.Strings(contexts)
	records := make([]coredatasource.Record, 0, len(contexts))
	for _, contextName := range contexts {
		ctxSpec := config.Contexts[contextName]
		if ctxSpec == nil {
			continue
		}
		clusterName := ctxSpec.Cluster
		cluster := config.Clusters[clusterName]
		server := ""
		if cluster != nil {
			server = cluster.Server
		}
		records = append(records, a.clusterRecord(clusterRecord{
			Context:        contextName,
			Cluster:        clusterName,
			Server:         server,
			Namespace:      ctxSpec.Namespace,
			CurrentContext: contextName == effectiveCurrentContext(config.CurrentContext, a.plugin.cfg.Context),
		}))
	}
	return records, nil
}

func (a *kubernetesAccessor) clusterRecord(cluster clusterRecord) coredatasource.Record {
	id := cluster.Context
	if id == "" {
		id = cluster.Cluster
	}
	return coredatasource.Record{
		ID:         id,
		Datasource: a.spec.Name,
		Entity:     ClusterEntity,
		Title:      id,
		Content: strings.TrimSpace(strings.Join([]string{
			cluster.Context,
			cluster.Cluster,
			cluster.Server,
			cluster.Namespace,
			boolLabel("current-context", cluster.CurrentContext),
			boolLabel("in-cluster", cluster.InCluster),
		}, " ")),
		Metadata: cleanMetadata(map[string]string{
			"context":         cluster.Context,
			"cluster":         cluster.Cluster,
			"server":          cluster.Server,
			"namespace":       cluster.Namespace,
			"current_context": boolString(cluster.CurrentContext),
			"in_cluster":      boolString(cluster.InCluster),
		}),
		Raw: cluster,
	}
}

func effectiveCurrentContext(kubeconfigCurrent, override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	return kubeconfigCurrent
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return ""
}

func boolLabel(label string, value bool) string {
	if value {
		return label
	}
	return ""
}

func (a *kubernetesAccessor) namespaceRecord(namespace corev1.Namespace) coredatasource.Record {
	return coredatasource.Record{
		ID:         namespace.Name,
		Datasource: a.spec.Name,
		Entity:     NamespaceEntity,
		Title:      namespace.Name,
		Content:    strings.TrimSpace(strings.Join([]string{namespace.Name, string(namespace.Status.Phase), labelsString(namespace.Labels)}, " ")),
		Metadata: cleanMetadata(map[string]string{
			"name":   namespace.Name,
			"phase":  string(namespace.Status.Phase),
			"labels": labelsString(namespace.Labels),
		}),
		Raw: namespace,
	}
}

func (a *kubernetesAccessor) podRecord(pod corev1.Pod) coredatasource.Record {
	sanitizedPod := sanitizePodEnv(pod)
	containers := make([]string, 0, len(sanitizedPod.Spec.Containers))
	images := make([]string, 0, len(sanitizedPod.Spec.Containers))
	for _, container := range sanitizedPod.Spec.Containers {
		containers = append(containers, container.Name)
		images = append(images, container.Image)
	}
	ready, total := podReady(sanitizedPod)
	id := namespacedID(sanitizedPod.Namespace, sanitizedPod.Name)
	return coredatasource.Record{
		ID:         id,
		Datasource: a.spec.Name,
		Entity:     PodEntity,
		Title:      id,
		Content: strings.TrimSpace(strings.Join([]string{
			id,
			string(sanitizedPod.Status.Phase),
			sanitizedPod.Spec.NodeName,
			sanitizedPod.Spec.ServiceAccountName,
			strings.Join(containers, " "),
			strings.Join(images, " "),
			labelsString(sanitizedPod.Labels),
		}, " ")),
		Metadata: cleanMetadata(map[string]string{
			"namespace":       sanitizedPod.Namespace,
			"name":            sanitizedPod.Name,
			"phase":           string(sanitizedPod.Status.Phase),
			"node":            sanitizedPod.Spec.NodeName,
			"service_account": sanitizedPod.Spec.ServiceAccountName,
			"pod_ip":          sanitizedPod.Status.PodIP,
			"host_ip":         sanitizedPod.Status.HostIP,
			"ready":           fmt.Sprintf("%d/%d", ready, total),
			"containers":      strings.Join(containers, ","),
			"images":          strings.Join(images, ","),
			"labels":          labelsString(sanitizedPod.Labels),
		}),
		Raw: sanitizedPod,
	}
}

func (a *kubernetesAccessor) serviceRecord(service corev1.Service) coredatasource.Record {
	ports := make([]string, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		ports = append(ports, fmt.Sprintf("%s:%d/%s", port.Name, port.Port, port.Protocol))
	}
	id := namespacedID(service.Namespace, service.Name)
	return coredatasource.Record{
		ID:         id,
		Datasource: a.spec.Name,
		Entity:     ServiceEntity,
		Title:      id,
		Content: strings.TrimSpace(strings.Join([]string{
			id,
			string(service.Spec.Type),
			service.Spec.ClusterIP,
			strings.Join(ports, " "),
			labelsString(service.Labels),
		}, " ")),
		Metadata: cleanMetadata(map[string]string{
			"namespace":  service.Namespace,
			"name":       service.Name,
			"type":       string(service.Spec.Type),
			"cluster_ip": service.Spec.ClusterIP,
			"ports":      strings.Join(ports, ","),
			"selector":   labelsString(service.Spec.Selector),
			"labels":     labelsString(service.Labels),
		}),
		Raw: service,
	}
}

func (a *kubernetesAccessor) containerRecords(pod corev1.Pod) []coredatasource.Record {
	sanitizedPod := sanitizePodEnv(pod)
	statuses := map[string]corev1.ContainerStatus{}
	for _, status := range sanitizedPod.Status.ContainerStatuses {
		statuses[status.Name] = status
	}
	out := make([]coredatasource.Record, 0, len(sanitizedPod.Spec.Containers))
	for _, container := range sanitizedPod.Spec.Containers {
		status := statuses[container.Name]
		id := namespacedID(sanitizedPod.Namespace, sanitizedPod.Name) + "/" + container.Name
		state := containerState(status.State)
		out = append(out, coredatasource.Record{
			ID:         id,
			Datasource: a.spec.Name,
			Entity:     ContainerEntity,
			Title:      id,
			Content: strings.TrimSpace(strings.Join([]string{
				id,
				container.Image,
				state,
				labelsString(sanitizedPod.Labels),
			}, " ")),
			Metadata: cleanMetadata(map[string]string{
				"namespace":     sanitizedPod.Namespace,
				"pod":           sanitizedPod.Name,
				"name":          container.Name,
				"image":         container.Image,
				"ready":         fmt.Sprintf("%t", status.Ready),
				"restart_count": fmt.Sprintf("%d", status.RestartCount),
				"state":         state,
			}),
			Raw: container,
		})
	}
	return out
}

func sanitizePodEnv(pod corev1.Pod) corev1.Pod {
	pod.Spec.InitContainers = sanitizeContainersEnv(pod.Spec.InitContainers)
	pod.Spec.Containers = sanitizeContainersEnv(pod.Spec.Containers)
	pod.Spec.EphemeralContainers = sanitizeEphemeralContainersEnv(pod.Spec.EphemeralContainers)
	return pod
}

func sanitizeContainersEnv(containers []corev1.Container) []corev1.Container {
	if len(containers) == 0 {
		return containers
	}
	out := make([]corev1.Container, len(containers))
	copy(out, containers)
	for i := range out {
		out[i].Env = sanitizeEnvVars(out[i].Env)
	}
	return out
}

func sanitizeEphemeralContainersEnv(containers []corev1.EphemeralContainer) []corev1.EphemeralContainer {
	if len(containers) == 0 {
		return containers
	}
	out := make([]corev1.EphemeralContainer, len(containers))
	copy(out, containers)
	for i := range out {
		out[i].Env = sanitizeEnvVars(out[i].Env)
	}
	return out
}

func sanitizeEnvVars(env []corev1.EnvVar) []corev1.EnvVar {
	if len(env) == 0 {
		return env
	}
	out := make([]corev1.EnvVar, len(env))
	for i, value := range env {
		out[i] = corev1.EnvVar{Name: value.Name}
	}
	return out
}

func podReady(pod corev1.Pod) (int, int) {
	ready := 0
	for _, status := range pod.Status.ContainerStatuses {
		if status.Ready {
			ready++
		}
	}
	return ready, len(pod.Spec.Containers)
}

func containerState(state corev1.ContainerState) string {
	switch {
	case state.Running != nil:
		return "running"
	case state.Waiting != nil:
		return "waiting:" + state.Waiting.Reason
	case state.Terminated != nil:
		return "terminated:" + state.Terminated.Reason
	default:
		return "unknown"
	}
}

func namespacedID(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

func cleanMetadata(input map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range input {
		value = strings.TrimSpace(value)
		if value != "" {
			out[key] = value
		}
	}
	return out
}

func objectMeta(name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: namespace}
}
