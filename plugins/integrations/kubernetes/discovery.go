package kubernetes

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	corediscovery "github.com/fluxplane/engine/core/discovery"
	coreendpoint "github.com/fluxplane/engine/core/endpoint"
	coresecret "github.com/fluxplane/engine/core/secret"
	runtimediscovery "github.com/fluxplane/engine/runtime/discovery"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EndpointDiscoveryOptions bounds Kubernetes endpoint candidate discovery.
type EndpointDiscoveryOptions struct {
	Product       string
	Namespaces    []string
	Limit         int
	AllowPodIP    bool
	PreferService bool
}

type kubernetesEndpointDiscoveryProvider struct {
	plugin Plugin
}

func (p kubernetesEndpointDiscoveryProvider) Spec() runtimediscovery.ProviderSpec {
	return runtimediscovery.ProviderSpec{
		Name:        Name,
		Source:      "kubernetes",
		Products:    []string{"loki", "grafana", "prometheus", "database", "postgres", "mysql", "redis", "mongodb", "http"},
		Description: "Kubernetes Service, Pod, and workload-wired endpoint candidates.",
	}
}

func (p kubernetesEndpointDiscoveryProvider) Discover(ctx context.Context, req corediscovery.Request) (corediscovery.Result, error) {
	opts := EndpointDiscoveryOptions{
		Product:    req.Product,
		Limit:      req.Limit,
		Namespaces: queryList(req.Query, "namespace", "namespaces"),
		AllowPodIP: parseBool(req.Query["allow_pod_ip"]),
	}
	if len(opts.Namespaces) == 0 && !p.plugin.cfg.AllNamespaces {
		opts.Namespaces = p.plugin.cfg.Namespaces
	}
	candidates, err := DiscoverEndpointCandidates(ctx, p.plugin, opts)
	if err != nil {
		return corediscovery.Result{}, err
	}
	return corediscovery.Result{Candidates: candidates}, nil
}

// DiscoverEndpointCandidates discovers product endpoint candidates from
// Kubernetes Services and Pods.
func DiscoverEndpointCandidates(ctx context.Context, p Plugin, opts EndpointDiscoveryOptions) ([]corediscovery.Candidate, error) {
	product := strings.TrimSpace(opts.Product)
	client, err := p.clientset(ctx)
	if err != nil {
		return nil, err
	}
	namespaces := endpointDiscoveryNamespaces(p.cfg, opts.Namespaces)
	limit := opts.Limit
	var candidates []corediscovery.Candidate
	for _, namespace := range namespaces {
		if namespace == "" && namespace != metav1.NamespaceAll {
			continue
		}
		services, err := client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for _, service := range services.Items {
			candidates = append(candidates, serviceEndpointCandidates(product, service)...)
		}
		if pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{}); err == nil {
			for _, pod := range pods.Items {
				if opts.AllowPodIP {
					candidates = append(candidates, podEndpointCandidates(product, pod, true)...)
				}
				candidates = append(candidates, containerEnvEndpointCandidates(product, "pod", pod.Namespace, pod.Name, string(pod.UID), pod.Spec.Containers, pod.Labels, pod.Annotations)...)
			}
		}
		if deployments, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{}); err == nil {
			for _, deployment := range deployments.Items {
				candidates = append(candidates, deploymentEnvEndpointCandidates(product, deployment)...)
			}
		}
	}
	candidates = rankedCandidates(candidates, limit)
	for i := range candidates {
		annotateKubernetesCandidate(&candidates[i], p.cfg)
	}
	return candidates, nil
}

func annotateKubernetesCandidate(candidate *corediscovery.Candidate, cfg Config) {
	if candidate == nil {
		return
	}
	attrs := candidate.Source.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}
	cfg = NormalizeConfig(cfg)
	if cfg.Context != "" {
		attrs["context"] = cfg.Context
	}
	if cfg.Kubeconfig != "" {
		attrs["kubeconfig"] = cfg.Kubeconfig
	}
	if cfg.KubeconfigEnv != "" {
		attrs["kubeconfig_env"] = cfg.KubeconfigEnv
	}
	candidate.Source.Attributes = attrs
}

func queryList(query map[string]string, keys ...string) []string {
	for _, key := range keys {
		value := strings.TrimSpace(query[key])
		if value == "" {
			continue
		}
		return normalizeNamespaces(strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\n' || r == '\t'
		}))
	}
	return nil
}

func endpointDiscoveryNamespaces(cfg Config, requested []string) []string {
	if len(requested) > 0 {
		return normalizeNamespaces(requested)
	}
	cfg = NormalizeConfig(cfg)
	if len(cfg.Namespaces) > 0 {
		return cfg.Namespaces
	}
	return []string{metav1.NamespaceAll}
}

func serviceEndpointCandidates(product string, service corev1.Service) []corediscovery.Candidate {
	if service.Spec.Type == corev1.ServiceTypeExternalName || len(service.Spec.Ports) == 0 {
		return nil
	}
	var out []corediscovery.Candidate
	for _, port := range service.Spec.Ports {
		productHint, _, _ := detectEndpointProduct(service.Name, port.Name, int(port.Port), service.Labels)
		scheme := schemeForEndpointProduct(productHint)
		url := fmt.Sprintf("%s://%s.%s.svc:%d", scheme, service.Name, service.Namespace, port.Port)
		candidate := scoredEndpointCandidate(product, url, scheme, service.Name+"."+service.Namespace+".svc", int(port.Port), port.Name, service.Labels, service.Annotations, coreendpoint.SourceRef{
			Kind:      "kubernetes.service",
			Name:      service.Name,
			Namespace: service.Namespace,
			UID:       string(service.UID),
			Attributes: map[string]string{
				"type":       string(service.Spec.Type),
				"cluster_ip": service.Spec.ClusterIP,
				"provider":   Name,
			},
		})
		if includeCandidate(product, candidate) {
			out = append(out, candidate)
		}
		if service.Spec.ClusterIP != "" && service.Spec.ClusterIP != corev1.ClusterIPNone {
			ipURL := fmt.Sprintf("%s://%s:%d", scheme, service.Spec.ClusterIP, port.Port)
			ipCandidate := scoredEndpointCandidate(product, ipURL, scheme, service.Spec.ClusterIP, int(port.Port), port.Name, service.Labels, service.Annotations, candidate.Source)
			if includeCandidate(product, ipCandidate) {
				ipCandidate.Reasons = append(ipCandidate.Reasons, "cluster_ip")
				out = append(out, ipCandidate)
			}
		}
	}
	return out
}

func podEndpointCandidates(product string, pod corev1.Pod, allowPodIP bool) []corediscovery.Candidate {
	if !allowPodIP || pod.Status.Phase != corev1.PodRunning || strings.TrimSpace(pod.Status.PodIP) == "" {
		return nil
	}
	ports := podHTTPPorts(pod)
	if len(ports) == 0 && lokiNameOrLabel(pod.Name, pod.Labels) {
		ports = []namedPort{{Name: "http-metrics", Port: 3100}}
	}
	var out []corediscovery.Candidate
	for _, port := range ports {
		url := fmt.Sprintf("http://%s:%d", pod.Status.PodIP, port.Port)
		candidate := scoredEndpointCandidate(product, url, "http", pod.Status.PodIP, port.Port, port.Name, pod.Labels, pod.Annotations, coreendpoint.SourceRef{
			Kind:      "kubernetes.pod",
			Name:      pod.Name,
			Namespace: pod.Namespace,
			UID:       string(pod.UID),
			Attributes: map[string]string{
				"pod_ip":   pod.Status.PodIP,
				"phase":    string(pod.Status.Phase),
				"provider": Name,
			},
		})
		if includeCandidate(product, candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

type namedPort struct {
	Name string
	Port int
}

func podHTTPPorts(pod corev1.Pod) []namedPort {
	seen := map[int]bool{}
	var ports []namedPort
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.ContainerPort <= 0 || seen[int(port.ContainerPort)] {
				continue
			}
			name := strings.TrimSpace(port.Name)
			seen[int(port.ContainerPort)] = true
			ports = append(ports, namedPort{Name: name, Port: int(port.ContainerPort)})
		}
	}
	return ports
}

func deploymentEnvEndpointCandidates(product string, deployment appsv1.Deployment) []corediscovery.Candidate {
	return containerEnvEndpointCandidates(product, "deployment", deployment.Namespace, deployment.Name, string(deployment.UID), deployment.Spec.Template.Spec.Containers, deployment.Labels, deployment.Annotations)
}

func containerEnvEndpointCandidates(product, workloadKind, namespace, workloadName, uid string, containers []corev1.Container, labels, annotations map[string]string) []corediscovery.Candidate {
	var out []corediscovery.Candidate
	for _, container := range containers {
		values := map[string]string{}
		var refs []envEndpointRef
		for _, env := range container.Env {
			name := strings.TrimSpace(env.Name)
			if name == "" {
				continue
			}
			if env.Value != "" {
				values[name] = env.Value
				continue
			}
			if env.ValueFrom != nil {
				if ref := envSecretOrConfigRef(name, env.ValueFrom); ref.Source != "" {
					refs = append(refs, ref)
				}
			}
		}
		out = append(out, envValueEndpointCandidates(product, workloadKind, namespace, workloadName, uid, container.Name, values, labels, annotations)...)
		out = append(out, envRefEndpointCandidates(product, workloadKind, namespace, workloadName, uid, container.Name, refs, labels, annotations)...)
	}
	return out
}

type envEndpointRef struct {
	Name   string
	Source string
	Ref    string
	Key    string
}

func envSecretOrConfigRef(name string, source *corev1.EnvVarSource) envEndpointRef {
	if source.SecretKeyRef != nil {
		return envEndpointRef{Name: name, Source: "secret", Ref: source.SecretKeyRef.Name, Key: source.SecretKeyRef.Key}
	}
	if source.ConfigMapKeyRef != nil {
		return envEndpointRef{Name: name, Source: "configmap", Ref: source.ConfigMapKeyRef.Name, Key: source.ConfigMapKeyRef.Key}
	}
	return envEndpointRef{}
}

func envValueEndpointCandidates(product, workloadKind, namespace, workloadName, uid, container string, values map[string]string, labels, annotations map[string]string) []corediscovery.Candidate {
	var out []corediscovery.Candidate
	for name, value := range values {
		if !looksLikeEndpointEnv(name) {
			continue
		}
		candidate, ok := envEndpointCandidate(product, workloadKind, namespace, workloadName, uid, container, name, value, labels, annotations)
		if ok {
			out = append(out, candidate)
		}
	}
	for name, value := range values {
		if !strings.HasSuffix(strings.ToUpper(name), "_HOST") && strings.ToUpper(name) != "DB_HOST" {
			continue
		}
		prefix := strings.TrimSuffix(name, "_HOST")
		host := strings.TrimSpace(value)
		if host == "" || strings.Contains(host, "://") {
			continue
		}
		port := parsePort(firstNonEmptyString(values[prefix+"_PORT"], values["DB_PORT"]))
		detected := productFromEnvName(prefix)
		endpointURL := endpointURLForProduct(detected, host, port)
		candidate, ok := envEndpointCandidate(product, workloadKind, namespace, workloadName, uid, "", name, endpointURL, labels, annotations)
		if ok {
			out = append(out, candidate)
		}
	}
	return out
}

func envRefEndpointCandidates(product, workloadKind, namespace, workloadName, uid, container string, refs []envEndpointRef, labels, annotations map[string]string) []corediscovery.Candidate {
	var out []corediscovery.Candidate
	for _, ref := range refs {
		if !looksLikeEndpointEnv(ref.Name) {
			continue
		}
		productHint := productFromEnvName(ref.Name)
		if product != "" && !productMatches(product, productHint) {
			continue
		}
		source := envSource(workloadKind, namespace, workloadName, uid, container, ref.Name)
		source.Attributes["env_source"] = ref.Source
		source.Attributes["env_ref"] = ref.Ref
		source.Attributes["env_key"] = ref.Key
		authRef := ""
		if ref.Source == "secret" {
			authRef = coresecret.Kubernetes(namespace, ref.Ref, ref.Key).ResourceName()
		}
		out = append(out, corediscovery.Candidate{
			ID:          endpointCandidateID(source, ref.Source+"|"+ref.Ref+"|"+ref.Key),
			ProductHint: productHint,
			Protocol:    productHint,
			AuthRef:     authRef,
			Labels:      cloneStringMap(labels),
			Annotations: cloneStringMap(annotations),
			Source:      source,
			Reasons:     []string{"workload_env_ref"},
			Score:       25,
		})
	}
	return out
}

func envEndpointCandidate(product, workloadKind, namespace, workloadName, uid, container, envName, value string, labels, annotations map[string]string) (corediscovery.Candidate, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return corediscovery.Candidate{}, false
	}
	productHint := productFromEnvName(envName)
	endpointURL, host, port, scheme, ok := sanitizeEndpointValue(productHint, value)
	if !ok {
		return corediscovery.Candidate{}, false
	}
	if productHint == "" {
		productHint = productFromSchemeOrPort(scheme, port)
	}
	if concrete := productFromSchemeOrPort(scheme, port); concrete != "" {
		productHint = concrete
	}
	if product != "" && !productMatches(product, productHint) {
		return corediscovery.Candidate{}, false
	}
	source := envSource(workloadKind, namespace, workloadName, uid, container, envName)
	return corediscovery.Candidate{
		ID:          endpointCandidateID(source, endpointURL),
		URL:         endpointURL,
		Scheme:      scheme,
		Host:        host,
		Port:        port,
		ProductHint: productHint,
		Protocol:    productHint,
		Labels:      cloneStringMap(labels),
		Annotations: cloneStringMap(annotations),
		Source:      source,
		Reasons:     []string{"workload_env"},
		Score:       45,
	}, true
}

func schemeForEndpointProduct(product string) string {
	switch product {
	case "mysql", "postgres", "redis", "mongodb":
		return product
	default:
		return "http"
	}
}

func envSource(workloadKind, namespace, workloadName, uid, container, envName string) coreendpoint.SourceRef {
	attrs := map[string]string{
		"provider": Name,
		"workload": workloadKind,
		"env":      envName,
	}
	if container != "" {
		attrs["container"] = container
	}
	return coreendpoint.SourceRef{Kind: "kubernetes." + workloadKind + ".env", Name: workloadName, Namespace: namespace, UID: uid, Attributes: attrs}
}

func looksLikeEndpointEnv(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	return upper == "DB_HOST" ||
		upper == "DATABASE_URL" ||
		strings.HasSuffix(upper, "_URL") ||
		strings.HasSuffix(upper, "_DSN") ||
		strings.HasSuffix(upper, "_HOST")
}

func sanitizeEndpointValue(productHint, value string) (endpointURL, host string, port int, scheme string, ok bool) {
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Hostname() == "" {
			return "", "", 0, "", false
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		host = parsed.Hostname()
		port = parsePort(parsed.Port())
		scheme = parsed.Scheme
		return parsed.String(), host, port, scheme, true
	}
	if match := mysqlTCPDSN.FindStringSubmatch(value); len(match) == 2 {
		host, port = splitHostPort(match[1])
		return endpointURLForProduct("mysql", host, port), host, port, "mysql", host != ""
	}
	if strings.Contains(value, "@") {
		return "", "", 0, "", false
	}
	host, port = splitHostPort(value)
	scheme = firstNonEmptyString(productHint, productFromSchemeOrPort("", port))
	return endpointURLForProduct(scheme, host, port), host, port, scheme, host != ""
}

var mysqlTCPDSN = regexp.MustCompile(`@tcp\(([^)]+)\)`)

func splitHostPort(value string) (string, int) {
	value = strings.TrimSpace(value)
	host := value
	port := 0
	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		host = strings.Join(parts[:len(parts)-1], ":")
		port = parsePort(parts[len(parts)-1])
	}
	return strings.Trim(host, "[]"), port
}

func parsePort(value string) int {
	port, _ := strconv.Atoi(strings.TrimSpace(value))
	return port
}

func endpointURLForProduct(product, host string, port int) string {
	if host == "" {
		return ""
	}
	scheme := firstNonEmptyString(product, "tcp")
	if port > 0 {
		return fmt.Sprintf("%s://%s:%d", scheme, host, port)
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func productFromEnvName(name string) string {
	upper := strings.ToUpper(name)
	switch {
	case strings.Contains(upper, "POSTGRES") || strings.HasPrefix(upper, "PG_"):
		return "postgres"
	case strings.Contains(upper, "MYSQL") || strings.Contains(upper, "MARIADB"):
		return "mysql"
	case strings.Contains(upper, "REDIS"):
		return "redis"
	case strings.Contains(upper, "MONGO"):
		return "mongodb"
	case strings.Contains(upper, "DATABASE") || strings.HasPrefix(upper, "DB_") || strings.HasSuffix(upper, "_DSN"):
		return "database"
	default:
		return ""
	}
}

func productFromSchemeOrPort(scheme string, port int) string {
	switch strings.ToLower(scheme) {
	case "postgres", "postgresql":
		return "postgres"
	case "mysql", "mariadb":
		return "mysql"
	case "redis":
		return "redis"
	case "mongodb", "mongo":
		return "mongodb"
	}
	switch port {
	case 5432:
		return "postgres"
	case 3306:
		return "mysql"
	case 6379:
		return "redis"
	case 27017:
		return "mongodb"
	default:
		return ""
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func scoredEndpointCandidate(product, endpointURL, scheme, host string, port int, portName string, labels, annotations map[string]string, source coreendpoint.SourceRef) corediscovery.Candidate {
	productHint, score, reasons := endpointProductScore(product, source.Name, portName, port, labels)
	return corediscovery.Candidate{
		ID:          endpointCandidateID(source, endpointURL),
		URL:         endpointURL,
		Scheme:      scheme,
		Host:        host,
		Port:        port,
		PortName:    portName,
		ProductHint: productHint,
		Protocol:    scheme,
		Labels:      cloneStringMap(labels),
		Annotations: cloneStringMap(annotations),
		Source:      source,
		Reasons:     reasons,
		Score:       score,
	}
}

func endpointProductScore(product, name, portName string, port int, labels map[string]string) (string, float64, []string) {
	hint, baseScore, reasons := detectEndpointProduct(name, portName, port, labels)
	if product != "" && !productMatches(product, hint) {
		return hint, 0, reasons
	}
	return hint, baseScore, reasons
}

func detectEndpointProduct(name, portName string, port int, labels map[string]string) (string, float64, []string) {
	lowerName := strings.ToLower(name)
	if strings.Contains(lowerName, "promtail") || strings.Contains(lowerName, "grafana-agent") {
		return "", 0, nil
	}
	var score float64
	var reasons []string
	if lokiNameOrLabel(name, labels) {
		score += 60
		reasons = append(reasons, "loki_name_or_label")
	}
	if lokiPort(portName, port) {
		score += 30
		reasons = append(reasons, "loki_port")
	}
	if score > 0 {
		if likelyHTTPPort(portName, port) {
			score += 5
			reasons = append(reasons, "http_port")
		}
		if sourceIsServiceName(name) {
			score += 5
			reasons = append(reasons, "service_name")
		}
		return "loki", score, reasons
	}
	if nameOrLabelContains(name, labels, "grafana") || port == 3000 {
		return "grafana", 70, []string{"grafana_name_label_or_port"}
	}
	if nameOrLabelContains(name, labels, "prometheus") || port == 9090 {
		return "prometheus", 70, []string{"prometheus_name_label_or_port"}
	}
	if db := databaseProduct(name, portName, port); db != "" {
		return db, 65, []string{db + "_name_or_port"}
	}
	if likelyHTTPPort(portName, port) {
		return "http", 10, []string{"http_port"}
	}
	return "", 0, nil
}

func includeCandidate(product string, candidate corediscovery.Candidate) bool {
	if product == "" {
		return true
	}
	return productMatches(product, candidate.ProductHint) && candidate.Score > 0
}

func productMatches(requested, candidate string) bool {
	if requested == "" || requested == candidate {
		return true
	}
	return requested == "database" && (candidate == "postgres" || candidate == "mysql" || candidate == "redis" || candidate == "mongodb")
}

func nameOrLabelContains(name string, labels map[string]string, needle string) bool {
	if strings.Contains(strings.ToLower(name), needle) {
		return true
	}
	for _, value := range labels {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	return false
}

func databaseProduct(name, portName string, port int) string {
	value := strings.ToLower(name + " " + portName)
	switch {
	case strings.Contains(value, "postgres") || strings.Contains(value, "postgresql") || strings.Contains(value, "pg-") || port == 5432:
		return "postgres"
	case strings.Contains(value, "mysql") || strings.Contains(value, "mariadb") || port == 3306:
		return "mysql"
	case strings.Contains(value, "redis") || port == 6379:
		return "redis"
	case strings.Contains(value, "mongo") || port == 27017:
		return "mongodb"
	default:
		return ""
	}
}

func lokiNameOrLabel(name string, labels map[string]string) bool {
	if strings.Contains(strings.ToLower(name), "loki") {
		return true
	}
	for key, value := range labels {
		k := strings.ToLower(key)
		v := strings.ToLower(value)
		if (k == "app.kubernetes.io/name" || k == "app" || k == "name") && strings.Contains(v, "loki") {
			return true
		}
	}
	return false
}

func lokiPort(name string, port int) bool {
	n := strings.ToLower(name)
	return port == 3100 || strings.Contains(n, "loki") || n == "http-metrics"
}

func likelyHTTPPort(name string, port int) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "http") || port == 80 || port == 3000 || port == 3100 || port == 9090 || port == 9093
}

func sourceIsServiceName(name string) bool {
	return strings.TrimSpace(name) != ""
}

func rankedCandidates(in []corediscovery.Candidate, limit int) []corediscovery.Candidate {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Score == in[j].Score {
			return in[i].ID < in[j].ID
		}
		return in[i].Score > in[j].Score
	})
	if limit > 0 && len(in) > limit {
		in = in[:limit]
	}
	return in
}

func endpointCandidateID(source coreendpoint.SourceRef, url string) string {
	sum := sha1.Sum([]byte(source.Kind + "|" + source.Namespace + "|" + source.Name + "|" + url))
	return hex.EncodeToString(sum[:12])
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
