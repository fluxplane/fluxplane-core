package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	coreoperation "github.com/fluxplane/agentruntime/core/operation"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const defaultPortForwardAddress = "127.0.0.1"

type portForwardInput struct {
	Namespace  string `json:"namespace,omitempty" jsonschema:"description=Kubernetes namespace. Defaults to plugin configuration or kubeconfig namespace."`
	Kind       string `json:"kind,omitempty" jsonschema:"enum=pod,enum=service,enum=deployment,description=Target resource kind. Defaults to pod."`
	Name       string `json:"name" jsonschema:"description=Target resource name."`
	LocalPort  int    `json:"local_port,omitempty" jsonschema:"description=Local port to bind. Defaults to remote_port."`
	RemotePort int    `json:"remote_port,omitempty" jsonschema:"minimum=1,maximum=65535,description=Remote target port. If omitted, inferred only when the target exposes exactly one port."`
	Address    string `json:"address,omitempty" jsonschema:"description=Local bind address. Defaults to 127.0.0.1."`
	Context    string `json:"context,omitempty" jsonschema:"description=Optional kubectl context override."`
	Kubeconfig string `json:"kubeconfig,omitempty" jsonschema:"description=Optional kubeconfig path override."`
	TimeoutMS  int    `json:"timeout_ms,omitempty" jsonschema:"description=Optional process timeout in milliseconds. Omit or set 0 for no timeout."`
	Label      string `json:"label,omitempty" jsonschema:"description=Optional managed process label. Defaults to a stable Kubernetes port-forward label."`
}

func operationSpecs() []coreoperation.Spec {
	return []coreoperation.Spec{portForwardSpec()}
}

func operationRefs(specs []coreoperation.Spec) []coreoperation.Ref {
	refs := make([]coreoperation.Ref, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, spec.Ref)
	}
	return refs
}

func portForwardOperation(p Plugin) coreoperation.Operation {
	return operationruntime.NewTypedResult[portForwardInput, map[string]any](portForwardSpec(), p.portForward(), operationruntime.WithIntent(portForwardIntent))
}

func portForwardSpec() coreoperation.Spec {
	return operationruntime.WithTypedContract[portForwardInput, map[string]any](coreoperation.Spec{
		Ref:         coreoperation.Ref{Name: PortForwardOp},
		Description: "Start or reuse a managed kubectl port-forward background process for a pod, service, or deployment.",
		Semantics: coreoperation.Semantics{
			Determinism: coreoperation.DeterminismNonDeterministic,
			Idempotency: coreoperation.IdempotencyIdempotent,
			Effects: coreoperation.EffectSet{
				coreoperation.EffectProcess,
				coreoperation.EffectNetwork,
				coreoperation.EffectReadExternal,
			},
			Risk: coreoperation.RiskMedium,
		},
		Annotations: map[string]string{"sandbox.overlay": "bypasses-unless-process-sandboxed"},
	})
}

func (p Plugin) portForward() operationruntime.TypedResultHandler[portForwardInput, map[string]any] {
	return func(ctx coreoperation.Context, req portForwardInput) coreoperation.Result {
		resolved, err := p.resolvePortForward(ctx, req)
		if err != nil {
			return coreoperation.Failed("invalid_k8s_port_forward_input", err.Error(), nil)
		}
		handle, started, err := p.system.Process().Ensure(ctx, system.ProcessRequest{
			Command:   "kubectl",
			Args:      resolved.args,
			Label:     resolved.label,
			Tags:      []string{"kubernetes", "port-forward"},
			Metadata:  resolved.metadata,
			Timeout:   resolved.timeout,
			MaxStdout: 16 * 1024,
			MaxStderr: 16 * 1024,
		})
		if err != nil {
			return coreoperation.Failed("k8s_port_forward_start_failed", err.Error(), map[string]any{"args": resolved.args, "label": resolved.label})
		}
		info := handle.Info()
		data := map[string]any{
			"process_id":  info.ID,
			"label":       info.Label,
			"running":     info.Running,
			"started":     started,
			"command":     info.Command,
			"args":        info.Args,
			"namespace":   resolved.namespace,
			"kind":        resolved.kind,
			"name":        resolved.name,
			"resource":    resolved.resource,
			"address":     resolved.address,
			"local_port":  resolved.localPort,
			"remote_port": resolved.remotePort,
			"local_url":   resolved.localURL,
		}
		verb := "Started"
		if !started {
			verb = "Using existing"
		}
		text := fmt.Sprintf("%s Kubernetes port-forward %s/%s:%d -> %s as %s", verb, resolved.namespace, resolved.name, resolved.remotePort, resolved.localURL, info.ID)
		if info.Label != "" {
			text += " (" + info.Label + ")"
		}
		return coreoperation.OK(coreoperation.Rendered{Text: text, Data: data})
	}
}

type resolvedPortForward struct {
	args       []string
	metadata   map[string]string
	label      string
	namespace  string
	kind       string
	name       string
	resource   string
	address    string
	localPort  int
	remotePort int
	timeout    time.Duration
	localURL   string
}

func (p Plugin) resolvePortForward(ctx context.Context, req portForwardInput) (resolvedPortForward, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return resolvedPortForward{}, fmt.Errorf("name is required")
	}
	kind, err := normalizePortForwardKind(req.Kind)
	if err != nil {
		return resolvedPortForward{}, err
	}
	address := strings.TrimSpace(req.Address)
	if address == "" {
		address = defaultPortForwardAddress
	}
	if strings.ContainsAny(address, " \t\n\r") {
		return resolvedPortForward{}, fmt.Errorf("address must not contain whitespace")
	}
	fallback, _ := defaultNamespace(ctx, p.cfg)
	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = firstNamespace(p.cfg)
	}
	if namespace == "" {
		namespace = strings.TrimSpace(fallback)
	}
	if namespace == "" {
		namespace = "default"
	}
	policy := policyFromConfig(p.cfg, fallback)
	if err := policy.AuthorizeNamespace(namespace); err != nil {
		return resolvedPortForward{}, err
	}
	remotePort := req.RemotePort
	if remotePort == 0 {
		remotePort, err = p.inferPortForwardRemotePort(ctx, namespace, kind, name)
		if err != nil {
			return resolvedPortForward{}, err
		}
	} else {
		remotePort, err = validPort(remotePort, "remote_port")
		if err != nil {
			return resolvedPortForward{}, err
		}
	}
	localPort := req.LocalPort
	if localPort == 0 {
		localPort = remotePort
	}
	localPort, err = validPort(localPort, "local_port")
	if err != nil {
		return resolvedPortForward{}, err
	}
	contextName := strings.TrimSpace(req.Context)
	if contextName == "" {
		contextName = p.cfg.Context
	}
	kubeconfig := strings.TrimSpace(req.Kubeconfig)
	if kubeconfig == "" {
		kubeconfig = p.cfg.Kubeconfig
	}
	resource := kind + "/" + name
	ports := strconv.Itoa(localPort) + ":" + strconv.Itoa(remotePort)
	args := []string{}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	args = append(args, "-n", namespace, "port-forward", "--address", address, resource, ports)
	if req.TimeoutMS < 0 {
		return resolvedPortForward{}, fmt.Errorf("timeout_ms must be non-negative")
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = portForwardLabel(namespace, kind, name, address, localPort, remotePort)
	}
	metadata := map[string]string{
		"namespace":   namespace,
		"kind":        kind,
		"name":        name,
		"resource":    resource,
		"address":     address,
		"local_port":  strconv.Itoa(localPort),
		"remote_port": strconv.Itoa(remotePort),
	}
	if contextName != "" {
		metadata["context"] = contextName
	}
	return resolvedPortForward{
		args:       args,
		metadata:   metadata,
		label:      label,
		namespace:  namespace,
		kind:       kind,
		name:       name,
		resource:   resource,
		address:    address,
		localPort:  localPort,
		remotePort: remotePort,
		timeout:    timeout,
		localURL:   "http://" + address + ":" + strconv.Itoa(localPort),
	}, nil
}

func (p Plugin) inferPortForwardRemotePort(ctx context.Context, namespace, kind, name string) (int, error) {
	client, err := p.clientset(ctx)
	if err != nil {
		return 0, err
	}
	switch kind {
	case "service":
		service, err := client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return 0, err
		}
		return singlePort("service/"+name, servicePorts(service))
	case "pod":
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return 0, err
		}
		return singlePort("pod/"+name, podPorts(&pod.Spec))
	case "deployment":
		deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return 0, err
		}
		return singlePort("deployment/"+name, podPorts(&deployment.Spec.Template.Spec))
	default:
		return 0, fmt.Errorf("kind must be pod, service, or deployment")
	}
}

func servicePorts(service *corev1.Service) []int {
	if service == nil {
		return nil
	}
	ports := make([]int, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		if port.Port > 0 {
			ports = append(ports, int(port.Port))
		}
	}
	return uniqueSortedPorts(ports)
}

func podPorts(spec *corev1.PodSpec) []int {
	if spec == nil {
		return nil
	}
	var ports []int
	collectContainerPorts := func(containers []corev1.Container) {
		for _, container := range containers {
			for _, port := range container.Ports {
				if port.ContainerPort > 0 {
					ports = append(ports, int(port.ContainerPort))
				}
			}
		}
	}
	collectContainerPorts(spec.InitContainers)
	collectContainerPorts(spec.Containers)
	for _, container := range spec.EphemeralContainers {
		for _, port := range container.Ports {
			if port.ContainerPort > 0 {
				ports = append(ports, int(port.ContainerPort))
			}
		}
	}
	return uniqueSortedPorts(ports)
}

func singlePort(target string, ports []int) (int, error) {
	switch len(ports) {
	case 0:
		return 0, fmt.Errorf("remote_port is required because %s exposes no declared ports", target)
	case 1:
		return ports[0], nil
	default:
		parts := make([]string, 0, len(ports))
		for _, port := range ports {
			parts = append(parts, strconv.Itoa(port))
		}
		return 0, fmt.Errorf("remote_port is required because %s exposes multiple ports: %s", target, strings.Join(parts, ", "))
	}
}

func uniqueSortedPorts(ports []int) []int {
	if len(ports) == 0 {
		return nil
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

func portForwardIntent(_ coreoperation.Context, req portForwardInput) (coreoperation.IntentSet, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return coreoperation.IntentSet{}, fmt.Errorf("name is required")
	}
	kind, err := normalizePortForwardKind(req.Kind)
	if err != nil {
		return coreoperation.IntentSet{}, err
	}
	remotePort, err := validPort(req.RemotePort, "remote_port")
	if err != nil {
		return coreoperation.IntentSet{}, err
	}
	localPort := req.LocalPort
	if localPort == 0 {
		localPort = remotePort
	}
	localPort, err = validPort(localPort, "local_port")
	if err != nil {
		return coreoperation.IntentSet{}, err
	}
	address := strings.TrimSpace(req.Address)
	if address == "" {
		address = defaultPortForwardAddress
	}
	if strings.ContainsAny(address, " \t\n\r") {
		return coreoperation.IntentSet{}, fmt.Errorf("address must not contain whitespace")
	}
	args := []string{}
	if contextName := strings.TrimSpace(req.Context); contextName != "" {
		args = append(args, "--context", contextName)
	}
	if kubeconfig := strings.TrimSpace(req.Kubeconfig); kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	if namespace := strings.TrimSpace(req.Namespace); namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "port-forward", "--address", address, kind+"/"+name, strconv.Itoa(localPort)+":"+strconv.Itoa(remotePort))
	intentArgs := make([]coreoperation.Argument, 0, len(args))
	for _, arg := range args {
		intentArgs = append(intentArgs, coreoperation.Argument(arg))
	}
	return coreoperation.IntentSet{Operations: []coreoperation.IntentOperation{{
		Behavior:  coreoperation.IntentCommandExecution,
		Target:    coreoperation.ProcessTarget{Command: coreoperation.Command("kubectl"), Args: intentArgs},
		Role:      coreoperation.IntentRoleProcessCommand,
		Certainty: coreoperation.IntentCertain,
	}}}, nil
}

func normalizePortForwardKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "pod", "pods", "po":
		return "pod", nil
	case "service", "services", "svc":
		return "service", nil
	case "deployment", "deployments", "deploy":
		return "deployment", nil
	default:
		return "", fmt.Errorf("kind must be pod, service, or deployment")
	}
}

func validPort(port int, field string) (int, error) {
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", field)
	}
	return port, nil
}

func portForwardLabel(namespace, kind, name, address string, localPort, remotePort int) string {
	parts := []string{"k8s-port-forward", namespace, kind, name, address, strconv.Itoa(localPort), strconv.Itoa(remotePort)}
	return strings.Join(parts, ":")
}
