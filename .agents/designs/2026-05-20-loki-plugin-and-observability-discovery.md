# DESIGN: Loki Plugin and Observability Discovery

## Status

Brainstorm/design proposal for `plugins/lokiplugin`, with follow-on implications
for `plugins/kubernetesplugin`, future `plugins/prometheusplugin`,
`plugins/grafanaplugin`, and other observability integrations.

## Summary

Add a first-party Loki plugin that exposes logs as both:

- a live datasource (`loki.stream`, `loki.log_entry`, `loki.label`, possibly
  `loki.detected_endpoint`); and
- model-facing operations for query, label discovery, connection testing,
  context-window retrieval, and endpoint discovery.

The plugin should work with explicitly configured Loki URLs, but it should also
be able to auto-detect Loki from the current Kubernetes cluster. This detection
should not become Loki-only one-off code. Loki, Prometheus, Grafana,
Alertmanager, Tempo, Mimir, OpenTelemetry Collector, and similar tools all need
roughly the same discovery path:

1. inspect Kubernetes services, pods, endpoints, ingresses, and common labels in
   likely namespaces;
2. rank candidates by product-specific heuristics;
3. test candidate protocol endpoints with product-specific probes;
4. return a stable discovered endpoint URL plus metadata/provenance.

The preferred shape may need a small core concept for **endpoint descriptors**
and **endpoint discovery**. Kubernetes would then be one discovery contributor,
not the owner of the concept. Product plugins such as Loki, Prometheus, Grafana,
and Alertmanager would contribute product-specific endpoint matchers/probes and
consume discovered endpoints through a neutral contract. Direct reusable code can
live under the Kubernetes plugin initially, but the design should avoid making
Kubernetes the semantic home for all discovery.

## Cluster reconnaissance: dev EKS monitoring namespace

I checked the current dev EKS cluster:

```text
context:   arn:aws:eks:eu-central-1:523757638725:cluster/dev-eu-central-1
namespace: latest
monitoring namespace: monitoring
```

`dex loki discover -n monitoring` successfully found and probed Loki:

```text
monitoring/loki-0 (10.10.0.139) ✓ connected
Loki URL: http://10.10.0.139:3100
```

Useful Kubernetes objects in `monitoring` include:

| Kind | Name | Product labels | Ports | Notes |
|---|---|---|---|---|
| Service | `loki` | `app.kubernetes.io/name=loki`, `instance=loki`, `version=3.5.7` | `3100/http-metrics`, `9095/grpc` | Best Loki service candidate. |
| Pod | `loki-0` | `name=loki`, `component=single-binary`, `instance=loki` | `3100`, `9095`, `7946` | Direct Pod IP probe works over VPN/cluster network. |
| Service | `monitoring-grafana` | `name=grafana`, `instance=monitoring`, `version=12.3.0` | `80/http-web` | Future Grafana plugin candidate. |
| Pod | `monitoring-grafana-...` | `name=grafana`, `instance=monitoring` | `3000`, sidecars | Service port maps to pod HTTP. |
| Service | `monitoring-kube-prometheus-prometheus` | selector `app.kubernetes.io/name=prometheus`, `operator.prometheus.io/name=...` | `9090/http-web`, `8080/reloader-web` | Future Prometheus plugin candidate. |
| Pod | `prometheus-monitoring-kube-prometheus-prometheus-0` | `name=prometheus`, `instance=...` | `9090`, `8080` | Direct Pod IP probe likely works. |
| Service | `prometheus-operated` | selector `app.kubernetes.io/name=prometheus` | `9090/http-web` | Operator-managed headless service. |
| Service | `monitoring-kube-prometheus-alertmanager` | `name=alertmanager` via selector | `9093/http-web`, `8080/reloader-web` | Future Alertmanager candidate. |
| Service | `alertmanager-operated` | selector `app.kubernetes.io/name=alertmanager` | `9093`, mesh ports | Operator-managed headless service. |
| Service | `otel-collector` | `name=otel-collector`, `part-of=opentelemetry` | `4317/otlp-grpc`, `4318/otlp-http` | Future OTel plugin candidate. |
| Service | `promtail-metrics` | `name=promtail`, `version=3.5.1` | `3101/http-metrics` | Agent/collector, not query API. |

This validates that product discovery can rely on standard Kubernetes app labels,
service port names, known default ports, and HTTP health/API probes.

## Problems to solve

### Loki access is more than a datasource

Logs are naturally queryable, but users also ask operational questions such as:

- “find Loki in this cluster”;
- “test the Loki connection”;
- “show labels in namespace latest”;
- “tail recent `app=backend` logs”;
- “find errors around this request id”;
- “summarize logs for these pods since the deployment rolled out”.

A datasource alone is awkward for discovery, connection tests, and bounded log
windows. Operations make those actions explicit and safer.

### Discovery belongs near Kubernetes, but probes belong near products

Kubernetes can generically discover services and endpoints. It cannot know how
to prove that an endpoint is Loki versus Prometheus beyond labels and ports.
Loki-specific validation requires Loki API probes such as:

- `GET /ready`;
- `GET /loki/api/v1/status/buildinfo`;
- `GET /loki/api/v1/labels` or a tiny `query_range`.

Prometheus validation will use `/api/v1/status/buildinfo`, `/-/ready`, or
`/api/v1/labels`; Grafana uses `/api/health`; Alertmanager uses `/-/ready` and
`/api/v2/status`; Tempo/Mimir have their own probes.

Therefore discovery should be split:

- `core/endpoint`: inert endpoint specs, endpoint references, resolved endpoint
  handles, source refs, and endpoint metadata;
- `core/discovery`: inert discovery requests, detector specs, candidate specs,
  probe specs/results, and registry contribution contracts;
- runtime/orchestration endpoint discovery service: receives discovery signals,
  keeps the discovered endpoint registry, resolves endpoint refs, and ensures
  endpoints are usable;
- Kubernetes plugin: contributes Kubernetes endpoint candidates from cluster
  state;
- product plugins: contribute product detector/probe specs and call operations
  using either raw URLs or endpoint refs.

### Endpoint and discovery both deserve core concepts

Yes: this likely needs both `core/endpoint` and `core/discovery`, with different
responsibilities.

`core/endpoint` should model the stable connection target:

- `endpoint.Spec`: an explicitly configured endpoint, such as a Loki URL or
  Grafana base URL;
- `endpoint.Ref`: an opaque endpoint reference such as `@endpoint/<id>` used by
  operations and agents without exposing transport details;
- `endpoint.Resolved`: a runtime-ready endpoint handle containing an effective
  URL, headers/auth material references, TLS/proxy details, expiry, and
  provenance;
- `endpoint.SourceRef`: where the endpoint came from, such as Kubernetes Service,
  Kubernetes Pod, DNS-SD, static config, connector metadata, or a port-forward;
- endpoint metadata: cluster, namespace, product, protocol, labels,
  annotations, trust, and freshness.

`core/discovery` should model the process that finds and validates endpoints:

- `discovery.Request`: list/find/resolve/ensure request with query filters;
- `discovery.Candidate`: possible endpoint found by a discovery source;
- `discovery.DetectorSpec`: product-neutral matching hints, such as names,
  labels, ports, schemes, protocols, and required tags;
- `discovery.ProbeSpec`: inert declaration of a safe readiness/product probe;
- `discovery.ProbeResult`: result metadata from an executed probe;
- `discovery.Result`: selected endpoint refs plus candidates/probe provenance.

Core should not import Kubernetes, HTTP clients, Loki, Grafana, or Prometheus.
It should only define serializable contracts that plugins, runtime, and
orchestration can move around.

This concept is broader than observability. It can later cover:

- service endpoints discovered from Kubernetes;
- endpoints declared in app manifests;
- endpoints discovered from DNS-SD, Consul, AWS Cloud Map, or connector config;
- local development port-forwards or tunnels;
- externally exposed URLs from ingresses/gateways.

### Plugin coupling needs a clean shape

It is tempting for `lokiplugin` to import helper code from
`plugins/kubernetesplugin`. That is acceptable inside the `plugins` layer from an
architecture-rule perspective, but it risks making Loki pull in Kubernetes even
when configured with a plain URL, and it can create a pattern where every
observability plugin imports Kubernetes internals.

A cleaner boundary is one of:

1. **Core endpoint contracts + orchestration registry**: product plugins
   contribute endpoint detector/probe specs; discovery plugins contribute
   candidate sources; orchestration/runtime combines candidates and probes into
   endpoint results. This is the clean long-term shape.
2. **Runtime discovery operation**: runtime exposes `discover_endpoints` and
   `discovery_endpoint_ensure`; Kubernetes is one candidate source behind that
   operation. Requests/results are shaped by `core/discovery` and endpoint refs
   by `core/endpoint`. This is the practical interim shape.
3. **Small shared detector package**: a package under `plugins/kubernetesplugin`
   or a future `plugins/observabilitydiscovery` contains only generic candidate
   structs and client-go enumeration. Product plugins pass matcher/probe
   callbacks. This is simplest for v1, but more compile-time coupled.
4. **Pluginhost discovery signals**: plugins contribute discovery sources,
   detector specs, and probe specs. Kubernetes plugin emits candidates; product
   plugins contribute product detectors/probes; runtime/orchestration resolves
   and stores endpoint records. This has the least coupling between product
   plugins and infrastructure plugins.

Recommended path: introduce the `core/endpoint` and `core/discovery` vocabulary
first, then implement discovery signals and a runtime endpoint registry. Avoid
Loki importing Kubernetes helpers directly except possibly as a short-lived
prototype.

## Goals

- Provide live Loki access through first-party plugin resources.
- Support explicit URL configuration and Kubernetes auto-discovery.
- Keep all network/Kubernetes/process access inside runtime/system and operation
  safety boundaries.
- Use typed operation input/output structs and generated JSON Schema.
- Provide model-useful, bounded, structured log results.
- Allow namespace-scoped queries by default to avoid accidental broad log reads.
- Preserve useful labels and timestamps without dumping unbounded raw payloads.
- Make discovery reusable for Prometheus, Grafana, Alertmanager, OTel, and other
  future plugins.
- Keep Kubernetes discovery read-only.

## Non-goals

- No log ingestion or long-term indexing in the first slice. Loki remains the
  source of truth.
- No writes to Loki.
- No streaming tail in v1 unless managed-process/operation streaming is already
  available; prefer bounded query windows first.
- No broad `core/observability` domain in v1. Narrow `core/endpoint` and
  `core/discovery` vocabularies are acceptable because they describe generic
  connection targets and discovery flows, not observability semantics.
- No shelling out to `kubectl` or `dex` in the implementation; use Go clients.
- No secret/token scraping from Kubernetes Secrets in v1. Discovery may find
  endpoints, but auth configuration should be explicit.

## Package placement

Initial packages:

```text
core/endpoint                      # inert endpoint specs, refs, resolved handles
core/discovery                     # inert discovery requests/candidates/probes
runtime/endpoint                   # registry, resolver, ensure lifecycle
orchestration/discovery            # optional coordination of plugin signals
plugins/lokiplugin                 # Loki datasource, operations, probes
plugins/kubernetesplugin/discovery # Kubernetes candidate source
```

Possible later extraction if multiple product plugins use shared non-Kubernetes
matching code:

```text
plugins/observabilitydiscovery     # optional helper package, not a core concept
```

`core/endpoint` is justified only if it remains generic and inert. It should
hold configured endpoint specs, endpoint refs, resolved endpoint handles, source
refs, and endpoint metadata. `core/discovery` holds request/candidate/probe/result
shapes and contribution contracts. Neither package may know about Kubernetes
clients, HTTP execution, Loki LogQL, Prometheus queries, Grafana API paths, or
cluster RBAC.

Concrete discovery stays in plugins because it is optional first-party
capability. Kubernetes enumeration belongs to `plugins/kubernetesplugin`; Loki
probing and query access belongs to `plugins/lokiplugin`. The runtime endpoint
service owns the registry of discovered endpoints and the ensure lifecycle. It
can turn `@endpoint/<id>` into an effective URL plus required headers/auth/proxy
or tunnel details for a bounded period.

## Detector/probe contribution options

Endpoint detector and probe specs can enter the system through two paths. Both
are useful, but they optimize for different authors.

### Pluginhost contributions

Product plugins contribute built-in detector/probe specs in Go:

- Loki plugin contributes `product=loki` detector hints and Loki HTTP probes;
- Prometheus plugin contributes `product=prometheus` detector hints and probes;
- Grafana plugin contributes `product=grafana` detector hints and probes;
- Kubernetes plugin contributes candidate sources, not Loki-specific detectors.

Pros:

- best default UX: enabling a plugin teaches the runtime how to find that product;
- least YAML for normal apps;
- detectors/probes are versioned with the plugin implementation;
- product-specific probe interpretation stays near product code;
- avoids app authors needing to know Kubernetes label/port conventions.

Cons:

- less transparent unless discovery status explains which detector matched;
- overriding built-ins needs a policy/precedence model;
- plugin upgrades may subtly change discovery behavior.

### App manifest resources

Apps can declare endpoint specs and optionally detector/probe resources:

```yaml
kind: endpoint
name: dev-loki
product: loki
url: http://loki.monitoring.svc:3100
metadata:
  k8s.cluster: dev
  k8s.namespace: latest
---
kind: endpoint_detector
name: custom-loki
product: loki
labels:
  app: [custom-loki]
ports: [3100]
```

Pros:

- explicit and reviewable for production;
- supports custom labels, non-standard ports, mTLS/proxy/auth, and hosted SaaS;
- can pin behavior independent of plugin upgrades;
- good place for environment-specific metadata such as `k8s.cluster=dev`.

Cons:

- more app authoring burden;
- duplicates common defaults if used for everything;
- still needs runtime safety checks before probes or ensure actions run.

### Recommendation

Use both:

1. pluginhost contributions provide built-in detector/probe defaults;
2. app manifest resources add explicit endpoints and override/augment detector
   behavior;
3. runtime discovery records provenance and precedence, so agents can explain
   whether an endpoint came from config, Kubernetes, a tunnel, or a probe.

Precedence should be: explicit endpoint spec > app detector override > plugin
built-in detector > raw source candidate.


## Loki plugin resources

### Default datasource

When enabled, Loki plugin contributes a default datasource named `loki` unless an
app config declares one or more named Loki datasources.

```yaml
plugins:
  - loki

datasource:
  datasources:
    - name: loki
      kind: loki
      entities: [loki.log_entry, loki.stream, loki.label, loki.detected_endpoint]
      config:
        url: ${LOKI_URL}
        default_namespace: latest
        default_since: 1h
```

When `url` is missing and `endpoint_ref` is present, the plugin asks the runtime
endpoint service to ensure and resolve that endpoint before making Loki API
calls. When both are missing and `auto_discover.enabled` is true, the plugin asks
the discovery service for matching Loki endpoints.

### Config shape

```go
type Config struct {
    URL              string            `json:"url,omitempty" jsonschema:"description=Loki base URL"`
    URLEnv           string            `json:"url_env,omitempty"`
    EndpointRef      string            `json:"endpoint_ref,omitempty" jsonschema:"description=Endpoint ref such as @endpoint/dev-loki"`
    TenantID         string            `json:"tenant_id,omitempty"`
    TenantIDEnv      string            `json:"tenant_id_env,omitempty"`
    DefaultNamespace string            `json:"default_namespace,omitempty"`
    DefaultSince     string            `json:"default_since,omitempty"`
    DefaultLimit     int               `json:"default_limit,omitempty"`
    MaxLimit         int               `json:"max_limit,omitempty"`
    Labels           []string          `json:"labels,omitempty"`
    AutoDiscover     AutoDiscoverConfig `json:"auto_discover,omitempty"`
}

type AutoDiscoverConfig struct {
    Enabled          bool     `json:"enabled,omitempty"`
    Kubernetes       bool     `json:"kubernetes,omitempty"`
    Namespaces       []string `json:"namespaces,omitempty"` // default: monitoring,loki,observability,logging
    PreferService    bool     `json:"prefer_service,omitempty"`
    AllowPodIP       bool     `json:"allow_pod_ip,omitempty"`
    ProbeTimeout     string   `json:"probe_timeout,omitempty"`
}
```

Default posture:

- local/dev: `auto_discover.enabled=true`, `kubernetes=true`, `allow_pod_ip=true`;
- deployed apps: prefer service DNS or service ClusterIP over Pod IP;
- production/least privilege: explicit URL is preferred.

## Datasource entity model

### `loki.detected_endpoint`

Represents discovered and probed Loki candidates.

Fields:

- `id`: stable hash of cluster, namespace, resource kind/name, port, URL;
- `url`: candidate URL;

- `namespace`;
- `resource_kind`: Service, Pod, Ingress, EndpointSlice;
- `resource_name`;
- `port_name`, `port`, `scheme`;
- `labels`, `annotations`;
- `score`;
- `probe_status`: unknown, reachable, ready, failed;
- `probe_error`;
- `version` when buildinfo is available;
- `provenance`: why it matched.

### `loki.label`

Represents label names or label values.

Fields:

- `id`: datasource + label name + optional value + time window;
- `name`;
- `value`;
- `namespace` if scoped;
- `since`, `until`;
- `cardinality_estimate` if available cheaply later.

### `loki.stream`

Represents a stream returned from a query.

Fields:

- `id`: hash of label set;
- `labels`: full stream labels;
- `namespace`, `app`, `pod`, `container` convenience fields;
- `first_timestamp`, `last_timestamp` in the result window;
- `entry_count` in the bounded response.

### `loki.log_entry`

Represents a bounded individual log entry.

Fields:

- `id`: hash of timestamp + stream labels + line hash;
- `timestamp`;
- `labels`;
- `namespace`, `app`, `pod`, `container` convenience fields;
- `line` raw log line;
- optional parsed fields for JSON/logfmt when requested;
- optional `level`, `logger`, `message`, `request_id`, `trace_id` when extracted.

The datasource should not silently fetch huge windows. `Search` and `List` must
require or default a time window and limit.

## Operations

All Loki operations that accept a `url` should also accept an `endpoint_ref` or
allow `url` values in the form `@endpoint/<id>`. Before executing the HTTP call,
the operation resolves the endpoint through the runtime endpoint service. This
keeps Loki operations simple while allowing endpoint refs to represent service
DNS, Pod IPs, port-forwards, tunnels, auth headers, or other runtime connection
details.

### `loki_discover`

Find Loki endpoints.

Input:

```go
type DiscoverInput struct {
    Kubernetes bool     `json:"kubernetes,omitempty"`
    Namespaces []string `json:"namespaces,omitempty"`
    Probe      bool     `json:"probe,omitempty"`
    Limit      int      `json:"limit,omitempty"`
}
```

Output: ordered candidate endpoints with scores, probe results, selected URL.

### `loki_test`

Test an explicit or discovered Loki URL.

Input: URL override, tenant ID, timeout.
Output: reachable, ready, version/buildinfo, latency, error.

### `loki_labels`

List label names or values.

Input: label name optional, selector optional, namespace optional, since/until,
limit.
Output: label names/values.

### `loki_query`

Run bounded LogQL instant or range query.

Input:

- `query`;
- `namespace` or `all_namespaces`;
- `since`, `until`;
- `limit`;
- `direction`;
- `labels` display/extraction list;
- `parse`: none/json/logfmt/auto;
- `url` optional override.

Output:

- selected URL;
- normalized query;
- streams and log entries;
- statistics if Loki returns them;
- truncation flags.

### `loki_recent_logs`

Convenience operation for the common agent/user request “show recent logs from
app X”. It builds a safe selector and calls `loki_query`.

Input:

- `app`, `namespace`, `pod`, `container`;
- `contains` or `regex` optional;
- `since`, `limit`;
- `levels` optional;
- `request_id`/`trace_id` optional.

Output: compact entries and summary.

### `loki_around`

Find log lines around an anchor timestamp or request/trace id.

Input:

- `selector` or convenience fields;
- `request_id`/`trace_id`/`contains`;
- `at` timestamp or anchor query;
- `before`, `after` durations;
- `limit`.

Output: context window.

### `loki_summarize_errors` (maybe later)

Optional higher-level operation that groups recent errors by message shape. This
can be useful, but it mixes retrieval and analysis. Prefer exposing structured
retrieval first and let the agent summarize.

## Endpoint discovery design

### Core endpoint and discovery model

The reusable shapes should be product-neutral and source-neutral. Kubernetes data
appears only inside `endpoint.SourceRef` or metadata, not as direct fields on the
top-level endpoint concept.

```go
package endpoint

type Ref string // e.g. "@endpoint/dev-loki"

type Spec struct {
    Name        string            `json:"name"`
    URL         string            `json:"url,omitempty"`
    Product     string            `json:"product,omitempty"`  // loki, prometheus, grafana, ...
    Protocol    string            `json:"protocol,omitempty"` // http, grpc, otlp, ...
    AuthRef     string            `json:"auth_ref,omitempty"`
    Labels      map[string]string `json:"labels,omitempty"`
    Annotations map[string]string `json:"annotations,omitempty"`
}

type SourceRef struct {
    Kind       string            `json:"kind"` // kubernetes.service, kubernetes.pod, static, dns, connector, port_forward, ...
    Name       string            `json:"name,omitempty"`
    Namespace  string            `json:"namespace,omitempty"`
    Cluster    string            `json:"cluster,omitempty"`
    UID        string            `json:"uid,omitempty"`
    Attributes map[string]string `json:"attributes,omitempty"`
}

type Resolved struct {
    Ref         Ref               `json:"ref"`
    URL         string            `json:"url"`
    HeadersRef  string            `json:"headers_ref,omitempty"` // runtime-owned, not model-visible secrets
    Headers     map[string]string `json:"headers,omitempty"`     // only non-secret headers
    ExpiresAt   string            `json:"expires_at,omitempty"`
    Source      SourceRef         `json:"source,omitempty"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}
```

```go
package discovery

type Request struct {
    Op      string            `json:"op"` // list, find, resolve, ensure
    Product string            `json:"product,omitempty"`
    Query   map[string]string `json:"query,omitempty"` // k8s.namespace=latest, k8s.cluster=dev, ...
    Limit   int               `json:"limit,omitempty"`
}

type Candidate struct {
    ID          string            `json:"id"`
    URL         string            `json:"url,omitempty"`
    Scheme      string            `json:"scheme,omitempty"`
    Host        string            `json:"host,omitempty"`
    Port        int               `json:"port,omitempty"`
    PortName    string            `json:"port_name,omitempty"`
    ProductHint string            `json:"product_hint,omitempty"`
    Protocol    string            `json:"protocol,omitempty"`
    Labels      map[string]string `json:"labels,omitempty"`
    Annotations map[string]string `json:"annotations,omitempty"`
    Source      endpoint.SourceRef `json:"source"`
    Reasons     []string          `json:"reasons,omitempty"`
    Score       float64           `json:"score,omitempty"`
}

type DetectorSpec struct {
    Product      string              `json:"product"`
    Names        []string            `json:"names,omitempty"`
    Labels       map[string][]string `json:"labels,omitempty"`
    Ports        []int               `json:"ports,omitempty"`
    PortNames    []string            `json:"port_names,omitempty"`
    Schemes      []string            `json:"schemes,omitempty"`
    Protocols    []string            `json:"protocols,omitempty"`
    Sources      []string            `json:"sources,omitempty"`
    ExcludeNames []string            `json:"exclude_names,omitempty"`
}

type ProbeSpec struct {
    Product       string            `json:"product"`
    Method        string            `json:"method,omitempty"`
    Path          string            `json:"path"`
    ExpectedCodes []int             `json:"expected_codes,omitempty"`
    Timeout       string            `json:"timeout,omitempty"`
    Headers       map[string]string `json:"headers,omitempty"`
}

type ProbeResult struct {
    CandidateID string            `json:"candidate_id"`
    Probe       ProbeSpec         `json:"probe"`
    Status      string            `json:"status"` // unknown, reachable, ready, failed, wrong_product, auth_required
    LatencyMS   int64             `json:"latency_ms,omitempty"`
    Product     string            `json:"product,omitempty"`
    Version     string            `json:"version,omitempty"`
    Error       string            `json:"error,omitempty"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}

type Result struct {
    EndpointRefs []endpoint.Ref `json:"endpoint_refs,omitempty"`
    Candidates   []Candidate    `json:"candidates,omitempty"`
    Probes       []ProbeResult  `json:"probes,omitempty"`
}
```

These packages should not execute probes. Runtime/plugin code interprets
`DetectorSpec` and `ProbeSpec` through authorized systems.

### Runtime endpoint service

The runtime endpoint service owns the mutable registry and ensure lifecycle. It
is not a core concept; core only defines the inert records.

Responsibilities:

- store configured endpoints and discovered endpoints with freshness/provenance;
- answer discovery/list queries such as:
  `discover_endpoints(op=list, query={product=loki,k8s.cluster=dev,k8s.namespace=latest})`;
- merge candidates from Kubernetes, static config, DNS, tunnels, and future
  sources;
- execute authorized probes through the safety envelope;
- materialize endpoint refs such as `@endpoint/dev-loki`;
- ensure an endpoint is usable before an operation runs, for example by creating
  a temporary port-forward or selecting service DNS;
- return `endpoint.Resolved` with effective URL, non-secret headers, secret
  header references, expiry, and provenance;
- garbage-collect expired resolved handles and temporary access paths.

Example flow:

```text
user: connect to loki endpoint in dev cluster /ns=latest
agent/tool: discover_endpoints(op=list, query={product=loki,k8s.cluster=dev,k8s.namespace=latest})
runtime: returns endpoint refs/candidates, including @endpoint/dev-loki
agent/tool: discovery_endpoint_ensure(id=@endpoint/dev-loki)
runtime: creates/validates access path if needed and returns endpoint.Resolved
agent/tool: loki_query(endpoint_ref=@endpoint/dev-loki, query=..., since=15m)
```

`loki_query(url="@endpoint/dev-loki", ...)` may be accepted as syntactic sugar:
the operation treats endpoint refs as references, asks the endpoint service to
ensure them, then uses the resolved URL/headers internally.

### Kubernetes candidate enumeration

Kubernetes plugin can convert cluster resources into `discovery.Candidate` values.

### Kubernetes candidate enumeration rules

For namespaces `monitoring`, `loki`, `observability`, `logging`, plus configured
namespaces:

1. List Services:
   - include ClusterIP services with named or numbered ports;
   - include headless services when paired with endpoints/pods;
   - construct service DNS URLs for in-cluster execution:
     `http://service.namespace.svc:port`;
   - construct ClusterIP URLs when allowed and reachable from caller.
2. List Pods:
   - include running ready pods;
   - construct Pod IP URLs when `allow_pod_ip` is true;
   - use container ports and common defaults.
3. List Ingresses/Gateway routes later:
   - useful for Grafana externally exposed UI;
   - may require auth, TLS, and host routing.
4. List EndpointSlices later:
   - useful for service-to-ready-pod mapping and headless services.

### Product matcher examples

Loki matcher:

- labels: `app.kubernetes.io/name=loki`, `app=loki`, `name=loki`;
- service/pod name contains `loki` but not only `promtail`;
- port `3100` or port name `http-metrics`, `http`, `loki`, `read`, `write`;
- reject/low-score `promtail` and `grafana-agent` unless querying their metrics,
  not the Loki API.

Prometheus matcher:

- labels/name contains `prometheus`;
- port `9090` or `http-web`;
- prefer `prometheus-operated` or kube-prometheus service.

Grafana matcher:

- labels/name contains `grafana`;
- service port `80`, pod port `3000`, port name `http-web`;
- probe `/api/health`.

Alertmanager matcher:

- labels/name contains `alertmanager`;
- port `9093` or `http-web`;
- probe `/-/ready` or `/api/v2/status`.

OTel collector matcher:

- labels/name contains `otel-collector` or `opentelemetry-collector`;
- ports `4317`, `4318`, `8888`;
- product plugin should distinguish telemetry ingest endpoints from metrics
  endpoints.

### Probe strategy

Discovery should rank by labels and port first, then probe only the top bounded
set. Probes must be safe HTTP GETs with short timeouts.

Loki probes in order:

1. `GET /ready` expects a 2xx ready response;
2. `GET /loki/api/v1/status/buildinfo` extracts version/revision if available;
3. `GET /loki/api/v1/labels?start=...&end=...` confirms API shape.

The result should distinguish:

- label match but not probed;
- reachable but not ready;
- ready;
- wrong product;
- network/TLS/auth failure.

## Endpoint discovery registry / “signals” concept

A future pluginhost or orchestration-level registry could make discovery
composable:

```text
Kubernetes plugin contributes: endpoint candidates with labels/ports/provenance
Static config contributes: explicit endpoint specs from app manifests
Tunnel/port-forward plugin contributes: local forwarded endpoint candidates
Loki plugin contributes: loki detector/probe specs and consumes resolved endpoint
Prometheus plugin contributes: prometheus detector/probe specs
Grafana plugin contributes: grafana detector/probe specs
```

This is attractive because Kubernetes discovery becomes one endpoint source
rather than the discovery abstraction itself. However, it is probably premature
to build a full reactive signal system until at least Loki and Prometheus both
use the concept.

A lightweight interim version can be a `discover_endpoints` operation backed by
the runtime endpoint service, with Kubernetes as the first candidate source. It
takes `core/discovery.DetectorSpec`-shaped hints:

```json
{
  "namespaces": ["monitoring"],
  "labels": {"app.kubernetes.io/name": ["loki"]},
  "names": ["loki"],
  "ports": [3100],
  "port_names": ["http-metrics", "http"]
}
```

Product plugins can call that operation or duplicate the exact detector hints
through a small Go interface. If operation-to-operation calls are not yet a good
runtime pattern, keep the code internal but use the same `core/endpoint` and
`core/discovery` shapes for tests and future migration.

## Security and safety

- Log queries can expose sensitive data. Loki operations should be read-only but
  still safety-classified as data access, not harmless metadata.
- Default namespace scoping should use the current session/app namespace when
  known, not `-A`.
- All-namespace queries require an explicit `all_namespaces`/`scope_all` flag.
- Limits and time windows are mandatory after defaults are applied.
- Returned log lines should be bounded by entry count and max bytes.
- Optional redaction can be added later, but do not claim logs are safe by
  default.
- Kubernetes discovery requires RBAC for `get/list` on services, pods, endpoints,
  endpointslices, and ingresses only if enabled.
- Do not read Kubernetes Secrets for Grafana/Loki credentials in v1.
- Tenant IDs, bearer tokens, and basic auth credentials must come from config env
  references or runtime secret providers, not inline manifests.

## Implementation sequence

1. Add `core/endpoint` with inert `Spec`, `Ref`, `SourceRef`, and `Resolved`
   shapes. Keep it dependency-free and serializable.
2. Add `core/discovery` with inert `Request`, `Candidate`, `DetectorSpec`,
   `ProbeSpec`, `ProbeResult`, and `Result` shapes.
3. Add `runtime/endpoint` registry/resolver with explicit `discover_endpoints`
   and `discovery_endpoint_ensure` operations. The registry stores discovered
   endpoint metadata and resolved endpoint freshness.
4. Add discovery signal contribution contracts: source plugins contribute
   candidates; product plugins contribute detectors/probes.
5. Add Kubernetes candidate discovery as the first source contributor.
6. Add `plugins/lokiplugin` with explicit URL config, endpoint refs, typed Loki
   HTTP client, `loki_test`, `loki_labels`, and `loki_query` operations.
7. Add the Loki datasource over the same client: `loki.log_entry`,
   `loki.stream`, `loki.label`.
8. Add `loki_recent_logs` as a convenience wrapper with namespace/app/pod fields.
9. Add Prometheus plugin using the same endpoint/discovery model. If it fits
   cleanly, formalize discovery coordination; if not, revise before Grafana.
10. Add Grafana plugin and decide whether external ingress/UI discovery should be
    supported or whether only API health/datasource inspection matters.

## Open questions

Resolved direction from this design pass:

- Use both `core/endpoint` and `core/discovery`.
- Use both pluginhost contributions and app manifest resources for detector/probe
  specs; pluginhost provides defaults, app manifests provide explicit overrides.
- Prefer discovery signals/registry coordination over Go imports between product
  and infrastructure plugins.
- Discovery should be a runtime process that can be triggered manually or run
  automatically. Product operations accept raw URLs and endpoint refs.
- Runtime endpoint registry owns endpoint metadata, default namespace/cluster
  metadata, and ensure/resolve behavior.

Remaining open questions:

- What is the exact persistence model for discovered endpoint registry records:
  in-memory only, event-store backed, or data-store backed with TTL?
- Which discovery events should happen automatically, and which require explicit
  user/tool action under the safety policy?
- What is the exact operation naming: `discover_endpoints`,
  `endpoint_discover`, `discovery_endpoint_ensure`, or another command shape?
- How should auth-required but correctly identified endpoints be represented in
  `endpoint.Resolved` without exposing secrets?
- Should Pod IP discovery be disabled by default for deployed/in-cluster apps in
  favor of service DNS?
- Should Loki datasource `Search` accept natural-language terms, LogQL only, or
  structured filters only?
- How much JSON/logfmt parsing should be done by the plugin versus left to LogQL
  parsers (`| json`, `| logfmt`)?

## Acceptance criteria for first implementation

- Enabling Loki with an explicit URL allows `loki_test`, `loki_labels`, and
  bounded `loki_query`.
- In dev EKS, with no URL configured and Kubernetes auto-discovery enabled,
  `loki_discover` finds `monitoring/loki` or `monitoring/loki-0` and probes it
  successfully.
- `loki_recent_logs` can return recent `app=backend` logs from namespace
  `latest` with a limit and labels.
- Query results include truncation/limit metadata and preserve timestamps.
- No implementation shells out to `kubectl` or `dex`.
- Tests cover candidate ranking, probe success/failure, LogQL query construction,
  limit enforcement, and namespace scoping.
