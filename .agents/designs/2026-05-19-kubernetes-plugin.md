# Kubernetes Plugin Datasource

## Status

Design proposal for `plugins/kubernetesplugin`.

## Summary

Add a first-party `plugins/kubernetesplugin` that exposes basic Kubernetes
cluster read access as a native datasource provider. The first slice is read-only
and live-only: it uses the official Kubernetes Go client directly and does not
participate in datasource mirror/index ingestion.

The plugin should follow the Slack, Jira, and GitLab plugin shape:

- plugin-owned `Name`, manifest, config, auth/session resolution, and datasource
  provider;
- `core/datasource` entity specs with typed raw records;
- runtime datasource `Search`, `List`, and `Get` methods backed by provider API
  calls;
- no new core concepts and no Kubernetes-specific logic outside the plugin;
- automatic live datasource availability when the Kubernetes plugin is enabled,
  without requiring app authors to declare a datasource block for normal use.

## Goals

- Provide model/tool access to common Kubernetes inventory:
  - namespaces;
  - pods;
  - services;
  - deployments;
  - containers.
- Use the native Kubernetes Go client:
  - `k8s.io/client-go/kubernetes`;
  - `k8s.io/client-go/rest`;
  - `k8s.io/client-go/tools/clientcmd`.
- Support local developer kubeconfig and in-cluster service account auth,
  including the important deployment case where an app running inside
  Kubernetes connects to the same cluster it is deployed to.
- Keep the first slice read-only and side-effect free.
- Return compact, model-useful records instead of raw full Kubernetes API
  objects.
- Avoid datasource mirror/index work in the first implementation.

## Non-goals

- No create/update/delete/exec/log streaming operations in this slice.
- No watch/informer cache in this slice.
- No datasource mirror, semantic index, or corpus provider in this slice.
- No Helm, CRD, ingress, node, event, secret, configmap, job, or statefulset
  entities in this slice.
- No kubectl shelling out. The plugin must use client-go directly.
- No broad Kubernetes abstraction in `core`; Kubernetes remains a concrete
  plugin integration.

## Package Placement

Add:

```text
plugins/kubernetesplugin
```

Suggested files:

```text
plugins/kubernetesplugin/
  plugin.go        # pluginhost wiring, config normalization, contributions
  auth.go          # kubeconfig / in-cluster config resolution
  datasource.go    # datasource provider/accessor implementation
  models.go        # datasource entity raw structs and entity specs
  records.go       # Kubernetes API object -> datasource record mappers
  client.go        # small client interface and client-go adapter for tests
  plugin_test.go
  datasource_test.go
```

This belongs in `plugins` because it is an optional first-party capability
bundle contributed through pluginhost contracts. It may depend on runtime system
interfaces and concrete Kubernetes client-go packages as part of the plugin's
external integration boundary.

## Plugin Shape

```go
const Name = "kubernetes"

type Plugin struct {
    pluginhost.Configurable[Config]
    system system.System
    ref    resource.PluginRef
    cfg    Config
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
```

The first slice contributes datasource access by default. Enabling the plugin
should be enough for agents to see a `kubernetes` datasource; app authors should
not have to add repetitive datasource declarations for the common case.

```go
func (p Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
    return resource.ContributionBundle{
        DataSources: []coredata.SourceSpec{DefaultDataSourceSpec(p.ref, p.cfg)},
    }, nil
}

func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
    return []coredatasource.Provider{kubernetesDatasourceProvider{plugin: p}}, nil
}
```

If `coredata.SourceSpec` is still required for manifest-visible source catalog
entries, mirror-related hints should be omitted or marked live-only. The runtime
`coredatasource.Provider` is the important first-slice contract.

The default datasource should be named from the plugin instance:

- default instance: `kubernetes`;
- named instance: `kubernetes.<instance>` or the existing plugin-ref naming
  convention used by other contributed resources.

Manual datasource declarations remain supported for advanced cases such as
multiple cluster views, alternate entity subsets, or specialized selectors, but
they should not be required in ordinary app manifests.

## Configuration

Plugin config should be explicit but ergonomic:

```go
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
```

The plugin-level `namespaces` setting is the primary access boundary. It applies
to the default datasource and to future Kubernetes operations such as pod restart
or deployment scale. Do not model namespace restrictions only as datasource
configuration because future operations must not depend on app authors manually
copying datasource boilerplate.

Datasource spec config can refine one datasource instance for advanced cases:

```yaml
datasources:
  - name: cluster
    kind: kubernetes
    entities:
      - kubernetes.namespace
      - kubernetes.pod
      - kubernetes.service
      - kubernetes.deployment
      - kubernetes.container
    config:
      namespaces: default,platform
      context: dev-cluster
```

Config precedence:

1. datasource `config` values;
2. plugin instance config;
3. kubeconfig current context / in-cluster defaults.

Supported datasource config keys:

- `instance`: must match plugin instance when set, following GitLab precedent;
- `context`: kubeconfig context override;
- `namespaces`: comma-separated namespace allowlist for this datasource view;
- `namespace`: accepted only as a convenience alias for a single namespace in
  manual datasource specs;
- `all_namespaces`: `true|false`, default false; this must be explicit and must
  not override a plugin-level namespace allowlist unless the plugin config also
  allows all namespaces;
- `label_selector`: optional list/search selector;
- `field_selector`: optional list/search selector.

Namespace policy rules:

- `Config.Namespaces` is an allowlist shared by datasources and future
  operations.
- `Config.AllNamespaces` means the plugin is allowed to access every namespace.
- When neither `namespaces` nor `all_namespaces` is configured, the default
  should be the current kubeconfig namespace or the pod's own namespace when
  in-cluster; if neither can be determined, use Kubernetes `default` rather than
  silently listing all namespaces.
- A manual datasource may narrow the plugin allowlist, but may not expand it.
- Future operations must call the same namespace authorization helper before
  acting on namespaced resources.

## Authentication and Client Resolution
The plugin should create a `*rest.Config` using this order:

1. If `in_cluster` is true, use `rest.InClusterConfig()`.
2. If `kubeconfig` is set, load that file through `clientcmd`.
3. If `kubeconfig_env` is set, read the env var and load that path.
4. If `KUBECONFIG` exists, use it.
5. Fall back to `${HOME}/.kube/config` for local development.
6. If no kubeconfig path exists and in-cluster config is available, use
   `rest.InClusterConfig()` automatically.

This automatic in-cluster fallback is required for deployed apps: when a coder or
agentruntime app is deployed into Kubernetes and the Kubernetes plugin is
enabled, the default behavior should be to connect to the same cluster using the
pod service account. The deployment target must document the RBAC required for
that service account, and examples should prefer Role/RoleBinding scoped to the
configured namespaces over ClusterRole/ClusterRoleBinding.

Implementation note: reusable plugin code should use `runtime/system.System` for
environment and filesystem access where practical. The direct client-go calls are
allowed inside this plugin boundary because client-go is the external protocol
implementation. Do not call `kubectl` or `os/exec`.

For tests, hide client-go behind a narrow interface:

```go
type kubernetesClient interface {
    ListNamespaces(ctx context.Context, opts metav1.ListOptions) (*corev1.NamespaceList, error)
    GetNamespace(ctx context.Context, name string) (*corev1.Namespace, error)
    ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error)
    GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error)
    ListServices(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.ServiceList, error)
    GetService(ctx context.Context, namespace, name string) (*corev1.Service, error)
    ListDeployments(ctx context.Context, namespace string, opts metav1.ListOptions) (*appsv1.DeploymentList, error)
    GetDeployment(ctx context.Context, namespace, name string) (*appsv1.Deployment, error)
}
```

The production adapter wraps `kubernetes.Interface`.

## Datasource Entities

Use stable entity names:

```go
const NamespaceEntity coredatasource.EntityType = "kubernetes.namespace"
const PodEntity       coredatasource.EntityType = "kubernetes.pod"
const ServiceEntity   coredatasource.EntityType = "kubernetes.service"
const DeploymentEntity coredatasource.EntityType = "kubernetes.deployment"
const ContainerEntity coredatasource.EntityType = "kubernetes.container"
```

Entity capabilities:

| Entity | Search | List | Get | Relation |
|---|---:|---:|---:|---:|
| namespace | yes | yes | yes | yes |
| pod | yes | yes | yes | yes |
| service | yes | yes | yes | yes |
| deployment | yes | yes | yes | yes |
| container | yes | yes | yes | no |

Search is provider-live but implemented as filtered list plus local matching over
names, labels, images, ports, selectors, and status fields. It should not require
a local index.

## Typed Raw Models

Return compact raw structs with JSON/datasource tags, similar to Slack and
GitLab. Avoid embedding full Kubernetes API objects.

```go
type Namespace struct {
    ID        string            `json:"id" datasource:"id,filterable"`
    Name      string            `json:"name" datasource:"searchable,filterable"`
    Phase     string            `json:"phase,omitempty" datasource:"filterable"`
    Labels    map[string]string `json:"labels,omitempty" datasource:"object"`
    CreatedAt string            `json:"created_at,omitempty" datasource:"filterable"`
}

type Pod struct {
    ID             string            `json:"id" datasource:"id,filterable"`
    Namespace      string            `json:"namespace" datasource:"filterable"`
    Name           string            `json:"name" datasource:"searchable,filterable"`
    Phase          string            `json:"phase,omitempty" datasource:"filterable"`
    NodeName       string            `json:"node_name,omitempty" datasource:"filterable"`
    PodIP          string            `json:"pod_ip,omitempty" datasource:"filterable"`
    HostIP         string            `json:"host_ip,omitempty" datasource:"filterable"`
    ServiceAccount string            `json:"service_account,omitempty" datasource:"filterable"`
    Ready          bool              `json:"ready" datasource:"filterable"`
    RestartCount   int32             `json:"restart_count,omitempty" datasource:"filterable"`
    Labels         map[string]string `json:"labels,omitempty" datasource:"object"`
    OwnerKind      string            `json:"owner_kind,omitempty" datasource:"filterable"`
    OwnerName      string            `json:"owner_name,omitempty" datasource:"searchable,filterable"`
    Containers     []Container       `json:"containers,omitempty" datasource:"array"`
}

type Service struct {
    ID          string            `json:"id" datasource:"id,filterable"`
    Namespace   string            `json:"namespace" datasource:"filterable"`
    Name        string            `json:"name" datasource:"searchable,filterable"`
    Type        string            `json:"type,omitempty" datasource:"filterable"`
    ClusterIP   string            `json:"cluster_ip,omitempty" datasource:"filterable"`
    ExternalIPs []string          `json:"external_ips,omitempty" datasource:"array"`
    Ports       []ServicePort     `json:"ports,omitempty" datasource:"array"`
    Selector    map[string]string `json:"selector,omitempty" datasource:"object"`
    Labels      map[string]string `json:"labels,omitempty" datasource:"object"`
}

type Deployment struct {
    ID                  string            `json:"id" datasource:"id,filterable"`
    Namespace           string            `json:"namespace" datasource:"filterable"`
    Name                string            `json:"name" datasource:"searchable,filterable"`
    Replicas            int32             `json:"replicas" datasource:"filterable"`
    ReadyReplicas       int32             `json:"ready_replicas" datasource:"filterable"`
    AvailableReplicas   int32             `json:"available_replicas" datasource:"filterable"`
    UpdatedReplicas     int32             `json:"updated_replicas" datasource:"filterable"`
    Selector            map[string]string `json:"selector,omitempty" datasource:"object"`
    Labels              map[string]string `json:"labels,omitempty" datasource:"object"`
    Containers          []Container       `json:"containers,omitempty" datasource:"array"`
}

type Container struct {
    ID           string   `json:"id" datasource:"id,filterable"`
    Namespace    string   `json:"namespace" datasource:"filterable"`
    Pod          string   `json:"pod,omitempty" datasource:"searchable,filterable"`
    Deployment   string   `json:"deployment,omitempty" datasource:"searchable,filterable"`
    Name         string   `json:"name" datasource:"searchable,filterable"`
    Image        string   `json:"image" datasource:"searchable,filterable"`
    Ready        bool     `json:"ready,omitempty" datasource:"filterable"`
    RestartCount int32    `json:"restart_count,omitempty" datasource:"filterable"`
    Ports        []string `json:"ports,omitempty" datasource:"array"`
}
```

Record IDs should be human-readable and stable:

- namespace: `<namespace>`;
- namespaced resource: `<namespace>/<name>`;
- pod container: `<namespace>/<pod>/containers/<container>`;
- deployment container: `<namespace>/deployments/<deployment>/containers/<container>`.

## Record Mapping

Records should be compact and useful in model context:

- `Title`: Kubernetes resource name, prefixed with namespace for namespaced
  entities when helpful.
- `Content`: concise status summary, for example:
  - pod: `phase=Running ready=true node=worker-1 restarts=0 images=nginx:1.27`;
  - deployment: `ready=3/3 available=3 updated=3 images=...`;
  - service: `type=ClusterIP cluster_ip=10.0.0.10 ports=http:80/TCP`.
- `Metadata`: include namespace, name, kind, labels flattened as needed,
  status, owner, selectors, image names, and port summaries.
- `Raw`: the typed compact struct.

Do not expose secret values, projected token contents, environment variable
values from container specs, or mounted secret/configmap data.

## Accessor Behavior

Provider/open behavior should follow GitLab/Jira patterns:

```go
type kubernetesDatasourceProvider struct {
    plugin Plugin
}

func (p kubernetesDatasourceProvider) Entities() []coredatasource.EntitySpec
func (p kubernetesDatasourceProvider) Open(ctx context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error)
```

`Open` should:

- verify `spec.Kind == kubernetes`;
- select and validate requested entities with `runtimedatasource.SelectEntities`;
- verify `instance` config if present;
- derive the effective namespace set by intersecting datasource config with the
  plugin namespace policy;
- reject datasource config that attempts to expand beyond the plugin's allowed
  namespaces;
- build or defer construction of a client session;
- return an accessor with the selected entities and namespace scoping.

`List` should:

- use `ListOptions{Limit: ...}` when feasible;
- support Kubernetes continue tokens as datasource cursors;
- respect namespace scope for every namespaced entity;
- iterate explicitly allowed namespaces when more than one namespace is allowed;
- use namespace `""` for all namespaces only when `Config.AllNamespaces` is true
  and the effective datasource view also allows all namespaces.

`Get` should parse stable IDs and call the native get endpoint for real
resources. For `kubernetes.container`, get should resolve the parent pod or
deployment and return the matching container.

`Search` should:

- default entity to `kubernetes.pod`;
- apply provider label/field selectors first;
- list a bounded page;
- apply local case-insensitive matching over the compact raw fields;
- return `runtimedatasource.SearchResult`.

Suggested matching fields:

- namespace/name;
- labels and selectors;
- pod phase, node, owner, service account, IPs;
- deployment replica status and images;
- service type, cluster IP, external IPs, ports;
- container name, image, parent pod/deployment.

## Relations

Add simple relation support where useful without mirror storage:

- namespace `pods` -> `kubernetes.pod` by namespace;
- namespace `services` -> `kubernetes.service` by namespace;
- namespace `deployments` -> `kubernetes.deployment` by namespace;
- pod `containers` -> `kubernetes.container` from pod spec/status;
- deployment `containers` -> `kubernetes.container` from pod template;
- service `pods` -> `kubernetes.pod` by service selector;
- deployment `pods` -> `kubernetes.pod` by deployment selector.

Relation requests should parse source IDs and call live Kubernetes list/get
APIs. They should not require a local datasource index.

## App Manifest Example

Ordinary app manifests should only enable and configure the plugin. The default
live datasource is contributed automatically:

```yaml
plugins:
  - name: kubernetes
    config:
      context: dev-cluster
      namespaces:
        - default
        - platform
```

An app deployed into Kubernetes can omit kubeconfig and context. The plugin then
uses the pod service account to connect to the same cluster:

```yaml
plugins:
  - name: kubernetes
    config:
      namespaces:
        - app-runtime
```

Manual datasource declarations are for advanced extra views only, for example a
narrow selector over the already allowed namespace set:

```yaml
datasources:
  - name: kube-platform-nginx
    kind: kubernetes
    entities: [kubernetes.pod, kubernetes.service]
    config:
      namespaces: platform
      label_selector: app=nginx
```

All-namespace access should be explicit in plugin config, not hidden in a
datasource override:

```yaml
plugins:
  - name: kubernetes
    config:
      all_namespaces: true
```

## Future Operations Namespace Policy

The same plugin config must gate future Kubernetes operations. Do not require app
authors to configure datasources just to constrain operations.

Future operations might expose shapes such as:

```go
type PodOperationInput struct {
    Namespace string `json:"namespace" jsonschema:"required"`
    Name      string `json:"name" jsonschema:"required"`
    Op        string `json:"op" jsonschema:"enum=restart"`
}

type DeploymentOperationInput struct {
    Namespace string `json:"namespace" jsonschema:"required"`
    Name      string `json:"name" jsonschema:"required"`
    Op        string `json:"op" jsonschema:"enum=scale,restart"`
    Replicas  *int32 `json:"replicas,omitempty"`
}
```

Before any side effect, the operation handler must call the plugin namespace
policy helper, for example `policy.AuthorizeNamespace(input.Namespace)`. The
helper should reject namespaces outside `Config.Namespaces` unless
`Config.AllNamespaces` is true. This keeps datasource visibility and operation
authorization aligned.

Those operations are explicitly out of scope for the first read-only datasource
slice, but the config and tests should be designed now so the same namespace
policy can be reused unchanged.
## Security and Safety

The first slice is read-only, but still accesses external cluster state.

- No mutation operations.
- No pod exec, port-forward, attach, delete, scale, rollout, patch, apply, or
  log streaming.
- No Kubernetes secret values. If secret/configmap entities are added later,
  redact data by default and require explicit risk-gated operations for content.
- namespace scoping should default narrow when configured or inferred.
- All-namespace listing must be explicit in plugin config and must be backed by
  matching Kubernetes RBAC.
- Errors should avoid leaking bearer tokens, kubeconfig contents, or certificate
  data.
- Client construction should honor context cancellation.

Future write operations, if added, must be `operation.Operation`s with explicit
semantics/effects/risk and must go through the runtime operation safety envelope.

## Testing Plan

Unit tests should use fake clients rather than a real cluster:

- entity specs expose expected capabilities and fields;
- provider rejects unsupported datasource kinds;
- provider enforces plugin instance match;
- list/search/get records for namespace, pod, service, deployment, and
  container;
- namespace scoping, namespace allowlist intersections, and all-namespace
  behavior;
- future operation policy helper rejects namespaces outside the plugin allowlist;
- Kubernetes continue token cursor propagation;
- relation queries for namespace pods, service pods, and deployment containers;
- secret/env redaction in record mapping.

Optional integration tests can use `envtest`, `kind`, or a configured kubeconfig,
but must be gated behind `TEST_INTEGRATION=1` and must not be required by normal
verification.

## Implementation Slices

### Slice 1: Plugin skeleton and entity specs

- Add `plugins/kubernetesplugin` package.
- Add config normalization, namespace policy helpers, and manifest.
- Add datasource provider contribution with an automatic default datasource.
- Add typed models and entity specs.
- Add tests for manifest, contribution, and entity capabilities.

### Slice 2: Native client session

- Add client-go dependency.
- Implement kubeconfig/in-cluster resolution, including automatic in-cluster
  fallback for apps deployed into Kubernetes.
- Add narrow client interface and production adapter.
- Add fake client tests for auth/config selection where possible.

### Slice 3: Live datasource list/get/search

- Implement list/get/search for namespace, pod, service, deployment, container.
- Add record mappers and redaction.
- Add unit tests with fake Kubernetes objects.

### Slice 4: Relations

- Add live relation handlers for namespace, pod, deployment, and service
  relations.
- Add tests for selectors and parent-child ID parsing.

## Open Questions

- Should `kubernetes.container` include both pod containers and deployment
  template containers by default, or should these become separate entities later
  if ambiguity becomes a problem?
- Should we add a separate `kubernetes.event` entity in the next slice for
  troubleshooting pods and deployments?
- Should generated Kubernetes deployment targets emit Role/RoleBinding manifests
  automatically from the plugin namespace policy, or only document the required
  RBAC at first?
