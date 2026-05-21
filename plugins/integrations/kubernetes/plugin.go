package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	coredata "github.com/fluxplane/agentruntime/core/data"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreevidence "github.com/fluxplane/agentruntime/core/evidence"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
	runtimediscovery "github.com/fluxplane/agentruntime/runtime/discovery"
	runtimeevidence "github.com/fluxplane/agentruntime/runtime/evidence"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name = "kubernetes"

	PortForwardOp = "k8s_port_forward"

	ClusterEntity    coredatasource.EntityType = "kubernetes.cluster"
	NamespaceEntity  coredatasource.EntityType = "kubernetes.namespace"
	PodEntity        coredatasource.EntityType = "kubernetes.pod"
	ServiceEntity    coredatasource.EntityType = "kubernetes.service"
	DeploymentEntity coredatasource.EntityType = "kubernetes.deployment"
	ContainerEntity  coredatasource.EntityType = "kubernetes.container"
)

const defaultPageSize = 50

type Config struct {
	Context       string   `json:"context,omitempty" yaml:"context,omitempty"`
	Kubeconfig    string   `json:"kubeconfig,omitempty" yaml:"kubeconfig,omitempty"`
	KubeconfigEnv string   `json:"kubeconfig_env,omitempty" yaml:"kubeconfig_env,omitempty"`
	Namespaces    []string `json:"namespaces,omitempty" yaml:"namespaces,omitempty"`
	AllNamespaces bool     `json:"all_namespaces,omitempty" yaml:"all_namespaces,omitempty"`
	InCluster     bool     `json:"in_cluster,omitempty" yaml:"in_cluster,omitempty"`
	QPS           float32  `json:"qps,omitempty" yaml:"qps,omitempty"`
	Burst         int      `json:"burst,omitempty" yaml:"burst,omitempty"`
}

type namespacePolicy struct {
	Namespaces    []string
	AllNamespaces bool
}

func NormalizeConfig(cfg Config) Config {
	cfg.Context = strings.TrimSpace(cfg.Context)
	cfg.Kubeconfig = strings.TrimSpace(cfg.Kubeconfig)
	cfg.KubeconfigEnv = strings.TrimSpace(cfg.KubeconfigEnv)
	cfg.Namespaces = normalizeNamespaces(cfg.Namespaces)
	if cfg.AllNamespaces {
		cfg.Namespaces = nil
	}
	return cfg
}

func normalizeNamespaces(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

func (p namespacePolicy) AuthorizeNamespace(namespace string) error {
	namespace = strings.TrimSpace(namespace)
	if p.AllNamespaces {
		return nil
	}
	if namespace == "" {
		return fmt.Errorf("kubernetes namespace is required unless all_namespaces is enabled")
	}
	for _, allowed := range p.Namespaces {
		if namespace == allowed {
			return nil
		}
	}
	return fmt.Errorf("kubernetes namespace %q is not allowed", namespace)
}

func (p namespacePolicy) listNamespaces() []string {
	if p.AllNamespaces {
		return []string{metav1.NamespaceAll}
	}
	return append([]string(nil), p.Namespaces...)
}

func policyFromConfig(cfg Config, fallback string) namespacePolicy {
	cfg = NormalizeConfig(cfg)
	if cfg.AllNamespaces {
		return namespacePolicy{AllNamespaces: true}
	}
	namespaces := cfg.Namespaces
	if len(namespaces) == 0 && strings.TrimSpace(fallback) != "" {
		namespaces = []string{strings.TrimSpace(fallback)}
	}
	if len(namespaces) == 0 {
		namespaces = []string{"default"}
	}
	return namespacePolicy{Namespaces: normalizeNamespaces(namespaces)}
}

type Plugin struct {
	pluginhost.Configurable[Config]
	system system.System
	ref    resource.PluginRef
	cfg    Config
	client kubernetes.Interface
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.AssertionDeriverContributor = Plugin{}
var _ pluginhost.DiscoveryProviderContributor = Plugin{}
var _ pluginhost.SecretResolverContributor = Plugin{}

func New(sys system.System) Plugin {
	return Plugin{system: sys}
}

func NewWithClient(sys system.System, client kubernetes.Interface) Plugin {
	return Plugin{system: sys, client: client}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Kubernetes datasource integration."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[Config](ctx)
	if err != nil {
		return nil, err
	}
	p.ref = ctx.Ref
	p.cfg = NormalizeConfig(cfg)
	return p, nil
}

func (p Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	p.ref = ctx.Ref
	specs := operationSpecs()
	return resource.ContributionBundle{
		DataSources:       []coredata.SourceSpec{DataSourceSpec(p.ref)},
		OperationSets:     []operation.Set{{Name: Name, Description: "Kubernetes cluster operations.", Operations: operationRefs(specs)}},
		Operations:        specs,
		Observers:         []coreevidence.ObserverSpec{kubernetesObserverSpec(p.ref)},
		AssertionDerivers: []coreevidence.AssertionDeriverSpec{kubernetesAssertionDeriverSpec()},
	}, nil
}

func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil {
		return nil, fmt.Errorf("kubernetesplugin: system is nil")
	}
	return []operation.Operation{portForwardOperation(p)}, nil
}

func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
	return []coredatasource.Provider{kubernetesDatasourceProvider{plugin: p}}, nil
}

func (p Plugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeevidence.Observer, error) {
	return []runtimeevidence.Observer{kubernetesObserver{plugin: p}}, nil
}

func (Plugin) AssertionDerivers(context.Context, pluginhost.Context) ([]runtimeevidence.AssertionDeriver, error) {
	return []runtimeevidence.AssertionDeriver{kubernetesAssertionDeriver{}}, nil
}

func (p Plugin) DiscoveryProviders(context.Context, pluginhost.Context) ([]runtimediscovery.Provider, error) {
	return []runtimediscovery.Provider{kubernetesEndpointDiscoveryProvider{plugin: p}}, nil
}

func (p Plugin) SecretResolvers(context.Context, pluginhost.Context) ([]runtimesecret.Resolver, error) {
	return []runtimesecret.Resolver{kubernetesSecretResolver{plugin: p}}, nil
}

func DataSourceSpec(ref resource.PluginRef) coredata.SourceSpec {
	name := Name
	if instance := ref.InstanceName(); instance != "" && instance != Name {
		name = Name + "." + instance
	}
	return coredata.SourceSpec{
		Name:        coredata.SourceName(name),
		Kind:        Name,
		Description: "Live Kubernetes cluster inventory.",
		Entities: []coredata.EntitySpec{
			{Type: coredata.EntityType(ClusterEntity), Description: "Configured Kubernetes cluster/context target."},
			{Type: coredata.EntityType(NamespaceEntity), Description: "Kubernetes namespace."},
			{Type: coredata.EntityType(PodEntity), Description: "Kubernetes pod."},
			{Type: coredata.EntityType(ServiceEntity), Description: "Kubernetes service."},
			{Type: coredata.EntityType(DeploymentEntity), Description: "Kubernetes deployment."},
			{Type: coredata.EntityType(ContainerEntity), Description: "Kubernetes container."},
		},
	}
}

type kubernetesDatasourceProvider struct {
	plugin Plugin
}

func (p kubernetesDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return entitySpecs()
}

func (p kubernetesDatasourceProvider) Open(ctx context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if spec.Kind != Name {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	selected := entitySpecs()
	if len(spec.Entities) > 0 {
		var err error
		selected, err = runtimedatasource.SelectEntities(Name, entitySpecs(), spec.Entities)
		if err != nil {
			return nil, err
		}
	}
	instance := strings.TrimSpace(spec.Config["instance"])
	if instance != "" && instance != p.plugin.ref.InstanceName() {
		return nil, fmt.Errorf("kubernetes datasource instance %q does not match plugin instance %q", instance, p.plugin.ref.InstanceName())
	}
	policy, err := p.plugin.policy(ctx, spec.Config)
	if err != nil {
		return nil, err
	}
	return &kubernetesAccessor{spec: spec, plugin: p.plugin, entities: selected, policy: policy}, nil
}

func (p Plugin) policy(ctx context.Context, config map[string]string) (namespacePolicy, error) {
	fallback, _ := defaultNamespace(ctx, p.cfg)
	base := policyFromConfig(p.cfg, fallback)
	override, hasOverride, err := policyFromDatasourceConfig(config)
	if err != nil {
		return namespacePolicy{}, err
	}
	if !hasOverride {
		return base, nil
	}
	return intersectPolicy(base, override)
}

func policyFromDatasourceConfig(config map[string]string) (namespacePolicy, bool, error) {
	if len(config) == 0 {
		return namespacePolicy{}, false, nil
	}
	all := parseBool(config["all_namespaces"])
	namespaces := normalizeNamespaces([]string{config["namespaces"], config["namespace"]})
	if all && len(namespaces) > 0 {
		return namespacePolicy{}, true, fmt.Errorf("kubernetes datasource config cannot set both namespaces and all_namespaces")
	}
	if all {
		return namespacePolicy{AllNamespaces: true}, true, nil
	}
	if len(namespaces) > 0 {
		return namespacePolicy{Namespaces: namespaces}, true, nil
	}
	return namespacePolicy{}, false, nil
}

func intersectPolicy(base, override namespacePolicy) (namespacePolicy, error) {
	if override.AllNamespaces {
		if base.AllNamespaces {
			return override, nil
		}
		return namespacePolicy{}, fmt.Errorf("kubernetes datasource cannot expand plugin namespace policy to all namespaces")
	}
	if base.AllNamespaces {
		return override, nil
	}
	var out []string
	for _, namespace := range override.Namespaces {
		if err := base.AuthorizeNamespace(namespace); err != nil {
			return namespacePolicy{}, err
		}
		out = append(out, namespace)
	}
	if len(out) == 0 {
		return namespacePolicy{}, fmt.Errorf("kubernetes datasource namespace override is empty")
	}
	return namespacePolicy{Namespaces: out}, nil
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type kubernetesAccessor struct {
	spec     coredatasource.Spec
	plugin   Plugin
	entities []coredatasource.EntitySpec
	policy   namespacePolicy
}

func (a *kubernetesAccessor) Spec() coredatasource.Spec { return a.spec }

func (a *kubernetesAccessor) Entities() []coredatasource.EntitySpec {
	return append([]coredatasource.EntitySpec(nil), a.entities...)
}

func (a *kubernetesAccessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = PodEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	limit := normalizedLimit(req.Limit)
	switch entity {
	case ClusterEntity:
		records, err := a.clusterRecords(ctx)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		if len(records) > limit {
			records = records[:limit]
		}
		return runtimedatasource.ListResult(a.spec.Name, entity, records, len(records), ""), nil
	}
	client, err := a.plugin.clientset(ctx)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	opts := metav1.ListOptions{Limit: int64(limit), Continue: req.Cursor, LabelSelector: strings.TrimSpace(req.Filters["label_selector"])}
	switch entity {
	case NamespaceEntity:
		list, err := client.CoreV1().Namespaces().List(ctx, opts)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		records := runtimedatasource.NonEmptyRecordsFrom(list.Items, a.namespaceRecord)
		return runtimedatasource.ListResult(a.spec.Name, entity, records, len(records), list.Continue), nil
	case PodEntity:
		return a.listNamespaced(ctx, entity, limit, opts, func(ns string, opts metav1.ListOptions) ([]coredatasource.Record, string, error) {
			list, err := client.CoreV1().Pods(ns).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return runtimedatasource.NonEmptyRecordsFrom(list.Items, a.podRecord), list.Continue, nil
		})
	case ServiceEntity:
		return a.listNamespaced(ctx, entity, limit, opts, func(ns string, opts metav1.ListOptions) ([]coredatasource.Record, string, error) {
			list, err := client.CoreV1().Services(ns).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return runtimedatasource.NonEmptyRecordsFrom(list.Items, a.serviceRecord), list.Continue, nil
		})
	case ContainerEntity:
		result, err := a.List(ctx, coredatasource.ListRequest{Entity: PodEntity, Limit: limit, Cursor: req.Cursor, Filters: req.Filters})
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		var records []coredatasource.Record
		for _, pod := range result.Records {
			if raw, ok := pod.Raw.(corev1.Pod); ok {
				records = append(records, a.containerRecords(raw)...)
			}
		}
		return runtimedatasource.ListResult(a.spec.Name, entity, records, len(records), result.NextCursor), nil
	default:
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q entity %q does not support list yet", a.spec.Name, entity)
	}
}

func (a *kubernetesAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = PodEntity
	}
	result, err := a.List(ctx, coredatasource.ListRequest{Entity: entity, Limit: max(normalizedLimit(req.Limit), 100), Filters: req.Filters})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	records := result.Records
	if query != "" {
		records = records[:0]
		for _, record := range result.Records {
			if strings.Contains(strings.ToLower(record.ID+" "+record.Title+" "+record.Content), query) {
				records = append(records, record)
			}
		}
	}
	limit := normalizedLimit(req.Limit)
	if len(records) > limit {
		records = records[:limit]
	}
	return runtimedatasource.SearchResult(a.spec.Name, entity, records, len(records)), nil
}

func (a *kubernetesAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if req.Entity == ClusterEntity {
		records, err := a.clusterRecords(ctx)
		if err != nil {
			return coredatasource.Record{}, err
		}
		id := strings.TrimSpace(req.ID)
		for _, record := range records {
			if record.ID == id || record.Metadata["context"] == id || record.Metadata["cluster"] == id {
				return record, nil
			}
		}
		return coredatasource.Record{}, fmt.Errorf("kubernetes cluster %q not found", id)
	}
	client, err := a.plugin.clientset(ctx)
	if err != nil {
		return coredatasource.Record{}, err
	}
	ns, name, ok := strings.Cut(strings.TrimSpace(req.ID), "/")
	if !ok {
		name = ns
		ns = ""
	}
	switch req.Entity {
	case NamespaceEntity:
		namespace, err := client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.namespaceRecord(*namespace), nil
	case PodEntity:
		if err := a.policy.AuthorizeNamespace(ns); err != nil {
			return coredatasource.Record{}, err
		}
		pod, err := client.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.podRecord(*pod), nil
	case ServiceEntity:
		if err := a.policy.AuthorizeNamespace(ns); err != nil {
			return coredatasource.Record{}, err
		}
		service, err := client.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.serviceRecord(*service), nil
	default:
		return coredatasource.Record{}, fmt.Errorf("datasource %q entity %q does not support get yet", a.spec.Name, req.Entity)
	}
}

type listFunc func(namespace string, opts metav1.ListOptions) ([]coredatasource.Record, string, error)

func (a *kubernetesAccessor) listNamespaced(ctx context.Context, entity coredatasource.EntityType, limit int, opts metav1.ListOptions, list listFunc) (coredatasource.ListResult, error) {
	var records []coredatasource.Record
	var next string
	for _, namespace := range a.policy.listNamespaces() {
		if err := a.policy.AuthorizeNamespace(namespace); err != nil && namespace != metav1.NamespaceAll {
			return coredatasource.ListResult{}, err
		}
		page, cont, err := list(namespace, opts)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		records = append(records, page...)
		if cont != "" {
			next = cont
			break
		}
		if len(records) >= limit {
			break
		}
	}
	if len(records) > limit {
		records = records[:limit]
	}
	return runtimedatasource.ListResult(a.spec.Name, entity, records, len(records), next), nil
}

func (p Plugin) clientset(ctx context.Context) (kubernetes.Interface, error) {
	if p.client != nil {
		return p.client, nil
	}
	cfg, _, err := p.restConfig(ctx)
	if err != nil {
		return nil, err
	}
	client, err := p.httpClientForRestConfig(cfg)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfigAndClient(cfg, client)
}

func (p Plugin) httpClientForRestConfig(cfg *rest.Config) (*http.Client, error) {
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}
	if p.system == nil || p.system.Network() == nil {
		return client, nil
	}
	tlsConfig, err := rest.TLSConfigFor(cfg)
	if err != nil {
		return nil, err
	}
	boundaryTransport := system.NewRoundTripper(
		p.system.Network(),
		system.WithHTTPClientTimeout(30*time.Second),
		system.WithHTTPClientMaxBytes(10*1024*1024),
		system.WithHTTPClientTLSConfig(tlsConfig),
	)
	wrappedTransport, err := rest.HTTPWrappersForConfig(cfg, boundaryTransport)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: wrappedTransport, Timeout: client.Timeout}, nil
}

func (p Plugin) restConfig(ctx context.Context) (*rest.Config, string, error) {
	cfg, namespace, err := p.restConfigFromKubeconfig(ctx)
	if err == nil {
		return cfg, namespace, nil
	}
	inCluster, inClusterErr := rest.InClusterConfig()
	if inClusterErr == nil {
		return inCluster, namespace, nil
	}
	if p.cfg.InCluster {
		return nil, "", inClusterErr
	}
	return nil, "", err
}

func (p Plugin) restConfigFromKubeconfig(ctx context.Context) (*rest.Config, string, error) {
	if p.cfg.InCluster {
		cfg, err := rest.InClusterConfig()
		return cfg, "", err
	}
	path, err := p.kubeconfigPath(ctx)
	if err != nil {
		return nil, "", err
	}
	rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: path}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: p.cfg.Context}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	cfg, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, "", err
	}
	if p.cfg.QPS > 0 {
		cfg.QPS = p.cfg.QPS
	}
	if p.cfg.Burst > 0 {
		cfg.Burst = p.cfg.Burst
	}
	namespace, _, _ := clientConfig.Namespace()
	return cfg, namespace, nil
}

func (p Plugin) kubeconfigPath(ctx context.Context) (string, error) {
	path := p.cfg.Kubeconfig
	if path == "" && p.cfg.KubeconfigEnv != "" {
		path, _, _ = lookupEnv(ctx, p.system, p.cfg.KubeconfigEnv)
	}
	if path == "" {
		path, _, _ = lookupEnv(ctx, p.system, "KUBECONFIG")
	}
	if path == "" {
		home, _, _ := lookupEnv(ctx, p.system, "HOME")
		if home != "" {
			path = filepath.Join(home, ".kube", "config")
		}
	}
	if path == "" {
		return "", fmt.Errorf("kubernetes kubeconfig path not found")
	}
	return path, nil
}

func defaultNamespace(ctx context.Context, cfg Config) (string, error) {
	plugin := Plugin{cfg: NormalizeConfig(cfg)}
	_, namespace, err := plugin.restConfigFromKubeconfig(ctx)
	if err != nil {
		return "", nil
	}
	return namespace, nil
}

func lookupEnv(ctx context.Context, sys system.System, key string) (string, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, nil
	}
	if sys != nil && sys.Environment() != nil {
		return sys.Environment().Lookup(ctx, key)
	}
	return "", false, errors.ErrUnsupported
}

func entitySpecs() []coredatasource.EntitySpec {
	capabilities := []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityList, coredatasource.EntityCapabilityGet}
	return []coredatasource.EntitySpec{
		{Type: ClusterEntity, Description: "Configured Kubernetes cluster/context target.", Capabilities: capabilities},
		{Type: NamespaceEntity, Description: "Kubernetes namespace.", Capabilities: capabilities},
		{Type: PodEntity, Description: "Kubernetes pod.", Capabilities: capabilities},
		{Type: ServiceEntity, Description: "Kubernetes service.", Capabilities: capabilities},
		{Type: DeploymentEntity, Description: "Kubernetes deployment.", Capabilities: capabilities},
		{Type: ContainerEntity, Description: "Kubernetes container.", Capabilities: capabilities},
	}
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func labelsString(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(values))
	for key, value := range values {
		pairs = append(pairs, key+"="+value)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

var _ = clientcmdapi.Config{}
