# DESIGN: OpenAPI Plugin

## Status

Design proposal for `plugins/openapiplugin`.

This is the first slice for automatically turning OpenAPI specifications into
AgentRuntime resources. The plugin should load OpenAPI specs from configured
URLs or file URIs, dereference them, and contribute two artifacts:

- executable model-facing operations for selected API operations; and
- a static documentation datasource for searching and indexing the API
  contract.

The plugin also needs auth method declarations so generated operations can use
existing credential resolution instead of inventing a new credential channel.

## Summary

Add a first-party `openapi` plugin. Each plugin instance accepts one or more
OpenAPI 3.x specs through app manifest plugin config. For each spec, it loads
and validates the document, resolves `$ref` references, applies include/exclude
filters, and generates operation specs plus runtime operation implementations.

The same parsed document is converted into a documentation datasource. This
datasource is static: it exposes records about operations, schemas, parameters,
responses, and security schemes. It does not call the target API as a live data
source in v1.

Credentials are configured as an auth map keyed by OpenAPI security scheme
name. Generated operations look up the operation's OpenAPI security
requirements, resolve the matching configured auth method through
`runtime/secret.Broker`, and apply the secret material to the outgoing HTTP
request.

## Goals

- Support OpenAPI 3.x JSON and YAML specs.
- Accept specs from `https?://...`, `file://...`, and workspace-relative file
  paths.
- Dereference local and remote `$ref` references through a controlled loader.
- Generate `operation.Spec` resources and executable `operation.Operation`
  implementations for selected OpenAPI operations.
- Generate documentation datasource records suitable for datasource search and
  semantic indexing.
- Use existing `core/secret.AuthMethodSpec` and `runtime/secret.Broker` for
  credentials.
- Keep all filesystem and network access inside `runtime/system.System`.
- Keep plugin config inside the existing `plugins[].config` manifest shape.
- Preserve operation safety by declaring network and secret access through the
  existing operation runtime mechanisms.

## Non-goals

- No Swagger 2.0 support in the first implementation.
- No live API datasource entities in v1. The datasource documents the API
  contract; generated operations execute the API.
- No new credential provider abstraction outside `core/secret` and
  `runtime/secret`.
- No direct `os`, `net/http`, or filesystem calls from reusable plugin code
  except through `runtime/system.System`.
- No compatibility shim for stale `agentsdk` binary behavior.
- No generated Go client code. Operations are built dynamically from the parsed
  OpenAPI document.

## Configuration

The plugin is configured through the existing app manifest plugin list:

```yaml
plugins:
  - kind: openapi
    instance: users
    config:
      specs:
        - url: https://api.example.test/openapi.yaml
          operations:
            prefix: users
            include:
              - listUsers
              - getUser
            exclude:
              - deleteUser
            overrides:
              getUser:
                name: users_get
                description: Fetch one user by id.
          datasource:
            name: users_api_docs
            index:
              enabled: true
          auth:
            schemes:
              bearerAuth:
                method: env
                kind: bearer_token
                env:
                  name: TESTUSER_PASSWORD
                header:
                  name: Authorization
                  scheme: Bearer
```

The initial typed config should be:

```go
type Config struct {
    Specs []SpecConfig `json:"specs,omitempty" yaml:"specs,omitempty"`
}

type SpecConfig struct {
    URL        string           `json:"url,omitempty" yaml:"url,omitempty"`
    File       string           `json:"file,omitempty" yaml:"file,omitempty"`
    Operations OperationsConfig `json:"operations,omitempty" yaml:"operations,omitempty"`
    Datasource DatasourceConfig `json:"datasource,omitempty" yaml:"datasource,omitempty"`
    Auth       AuthConfig       `json:"auth,omitempty" yaml:"auth,omitempty"`
}
```

Exactly one of `url` or `file` should be required per spec. `file://...` is
accepted and normalized into `file`. Workspace-relative file paths are resolved
through `System.Workspace()`.

`include` and `exclude` selectors should match any of:

- generated operation name;
- original `operationId`;
- `<METHOD> <path>`;
- tag name.

Exclude wins over include. Empty include means all operations are candidates.

## Operation generation

Generate one `operation.Spec` and one executable operation per selected OpenAPI
operation.

Operation naming rules:

1. Use `operationId` when present.
2. Otherwise derive `<method>_<path slug>`, such as `get_users_id`.
3. Apply `operations.prefix` unless an override name is configured.
4. Reject duplicate generated names within one plugin instance.

The generated input contract should use a stable envelope instead of flattening
all parameters into one object:

```json
{
  "path": {},
  "query": {},
  "headers": {},
  "cookies": {},
  "body": {}
}
```

OpenAPI parameter schemas populate the relevant object fields. Required
parameters remain required in the generated JSON Schema. Request body schema is
placed under `body`.

The generated output contract should expose:

```json
{
  "status": 200,
  "headers": {},
  "body": {}
}
```

For JSON responses, `body` should be decoded into JSON-compatible values. For
non-JSON responses, `body` can be a string in v1.

Runtime execution:

- choose server URL from operation-level servers, spec-level servers, or a
  future explicit config override;
- substitute path parameters;
- encode query params, headers, cookies, and request body;
- apply matching auth material;
- execute through `system.NewHTTPClient(sys.Network())`;
- return structured status, headers, and body;
- surface non-2xx responses as successful HTTP results unless transport or
  encoding failed.

Access descriptors:

- generated operations declare network access for the resolved host;
- generated operations declare secret use for the logical plugin secret
  matching the selected OpenAPI security scheme;
- no operation should read environment variables directly.

## Documentation datasource

When `datasource.name` is configured, contribute a `coredatasource.Spec` with
kind `openapi` and a datasource provider that serves records from the parsed
spec.

Initial entities:

- `openapi.operation`
- `openapi.schema`
- `openapi.parameter`
- `openapi.response`
- `openapi.security_scheme`

Required accessor capabilities:

- `Search`
- `List`
- `Get`
- `Corpus`

Documentation records should be deterministic and stable across runs. Suggested
record IDs:

- operations: `operation:<operation-name>`
- schemas: `schema:<component-name>`
- parameters: `parameter:<operation-name>:<location>:<name>`
- responses: `response:<operation-name>:<status>`
- security schemes: `security:<scheme-name>`

Operation records should include method, path, tags, summary, description,
parameters, request body summary, response summary, and auth requirements.

Schema records should include component name, description, field names, required
fields, and enough type information for useful search results.

`Corpus` should produce natural text for semantic indexing and should not call
the target API.

## Credentials

Auth config is keyed by OpenAPI security scheme name:

```yaml
auth:
  schemes:
    bearerAuth:
      method: env
      kind: bearer_token
      env:
        name: TESTUSER_PASSWORD
      header:
        name: Authorization
        scheme: Bearer
```

Each configured scheme becomes a `coresecret.AuthMethodSpec` contributed through
`pluginhost.AuthMethodContributor`.

The logical secret resource is:

```text
plugin/openapi/<instance>/<scheme-name>
```

Generated operations build a `coresecret.AuthRequest` with:

- `Plugin: "openapi"`
- `Instance: ref.InstanceName()`
- `Purpose: <scheme-name>`
- `Methods: []coresecret.AuthMethodSpec{...}`

V1 auth support:

- OpenAPI `http` bearer: apply `Authorization: Bearer <value>`.
- OpenAPI `apiKey` in header/query/cookie: apply the value at the declared
  location and name.
- OpenAPI `http` basic: accept `coresecret.KindBasic`; encode as Basic auth
  inside the plugin.

Future credential sources such as `k8s/from-container/backend/env/KEY` should
be represented as future secret ref schemes or auth method extensions. V1 should
not special-case Kubernetes secret sourcing inside this plugin.

## Package placement

Initial implementation should live in:

```text
plugins/openapiplugin/
```

Expected dependencies:

- `core/operation` for inert operation specs and refs;
- `runtime/operation` for typed/dynamic operation wrappers and access
  descriptors;
- `core/datasource` and `runtime/datasource` helpers for datasource metadata;
- `core/data` plus `runtime/data.SourceFromDatasource` for generic data source
  projection;
- `core/secret` and `runtime/secret` for auth method declarations and secret
  resolution;
- `runtime/system` for filesystem, network, and environment boundaries;
- `orchestration/pluginhost` for config decoding and plugin contribution
  contracts.

Register the plugin in `apps/launch.availablePlugins`. Add it to
`AuthPluginRegistry` so distribution-level auth discovery can see generated auth
methods.

Use `github.com/getkin/kin-openapi/openapi3` for OpenAPI 3.x parsing,
validation, and reference resolution.

## Static inspection behavior

Static resource views may resolve plugin contributions without a fully available
runtime system. In that case:

- if the spec can be loaded without external IO, contribute generated resources;
- if the spec requires unavailable IO, emit a warning diagnostic and skip
  generated artifacts for that spec;
- do not fail the entire resource view because one remote OpenAPI URL cannot be
  fetched during inspection.

Runtime app launch should fail when a configured spec cannot be loaded, because
generated operations and datasource providers would be incomplete.

## Testing

Add focused tests under `plugins/openapiplugin`.

Config tests:

- decode manifest plugin config into typed `Config`;
- validate exactly one source per spec;
- validate include/exclude and operation overrides;
- validate auth scheme config.

Generation tests:

- load a local OpenAPI 3.x fixture;
- verify generated operation names, descriptions, input schemas, and operation
  refs;
- verify duplicate operation names are rejected;
- verify datasource spec and entity metadata.

Runtime operation tests:

- use a fake `system.Network`;
- verify path substitution, query encoding, headers, JSON body encoding, and
  response decoding;
- verify bearer/header API key/basic auth application;
- verify missing credentials produce an error without leaking secret values.

Datasource tests:

- verify `Search`, `List`, `Get`, and `Corpus`;
- verify stable IDs;
- verify corpus records include operation, schema, parameter, response, and
  security documentation.

Integration checks:

```bash
go test ./plugins/openapiplugin ./orchestration/pluginhost ./apps/launch
task verify
```

## Open questions

- Whether static plugin inspection should ever fetch remote URLs by default, or
  only use cached/local specs.
- Whether operation output should treat non-2xx responses as `operation.OK`
  HTTP results or `operation.Failed` tool failures. The recommended v1 behavior
  is `operation.OK` for completed HTTP responses.
- Whether future live API datasource generation should be a separate opt-in
  mode under `datasource.live`, not a change to the documentation datasource.
