package kubernetesplugin

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
)

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
	containers := make([]string, 0, len(pod.Spec.Containers))
	images := make([]string, 0, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		containers = append(containers, container.Name)
		images = append(images, container.Image)
	}
	ready, total := podReady(pod)
	id := namespacedID(pod.Namespace, pod.Name)
	return coredatasource.Record{
		ID:         id,
		Datasource: a.spec.Name,
		Entity:     PodEntity,
		Title:      id,
		Content: strings.TrimSpace(strings.Join([]string{
			id,
			string(pod.Status.Phase),
			pod.Spec.NodeName,
			pod.Spec.ServiceAccountName,
			strings.Join(containers, " "),
			strings.Join(images, " "),
			labelsString(pod.Labels),
		}, " ")),
		Metadata: cleanMetadata(map[string]string{
			"namespace":       pod.Namespace,
			"name":            pod.Name,
			"phase":           string(pod.Status.Phase),
			"node":            pod.Spec.NodeName,
			"service_account": pod.Spec.ServiceAccountName,
			"pod_ip":          pod.Status.PodIP,
			"host_ip":         pod.Status.HostIP,
			"ready":           fmt.Sprintf("%d/%d", ready, total),
			"containers":      strings.Join(containers, ","),
			"images":          strings.Join(images, ","),
			"labels":          labelsString(pod.Labels),
		}),
		Raw: pod,
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
	statuses := map[string]corev1.ContainerStatus{}
	for _, status := range pod.Status.ContainerStatuses {
		statuses[status.Name] = status
	}
	out := make([]coredatasource.Record, 0, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		status := statuses[container.Name]
		id := namespacedID(pod.Namespace, pod.Name) + "/" + container.Name
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
				labelsString(pod.Labels),
			}, " ")),
			Metadata: cleanMetadata(map[string]string{
				"namespace":     pod.Namespace,
				"pod":           pod.Name,
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
