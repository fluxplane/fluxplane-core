# Runtime Security And Resource Authorization

## Status

Design record plus implementation notes. The first implementation slice is now
present in the runtime: typed authorization policy, app-config propagation,
inbound identity/trust context, tool projection filtering, operation execution
enforcement, approval-required routing, local coder defaults, and typed
operation access descriptors for the first built-in operation set. Follow-up
hardening is still needed for approval audit records, broader non-operation
resource gates, and replacing the remaining fallback operation-name heuristics.

## Problem

The runtime has partial caller/trust plumbing today:

- inbound channel submissions carry `policy.Caller`, `policy.Trust`, and a
  resolved `core/user.Actor`;
- HTTP/SSE now derives authority from listener configuration and only accepts a
  typed trust downgrade;
- tool projection receives inbound caller/trust and hides disallowed tools;
- operation execution goes through `runtime/operation.SafetyEnvelope`;
- context providers receive user/trust identity metadata.

That is not enough for a complete security model. The runtime still needs a
coherent answer to:

- who is acting now;
- who triggered the work originally;
- who or what the actor is acting on behalf of;
- which canonical user and groups were resolved;
- which resource the action targets;
- which operation capability is being invoked;
- whether concrete execution is risky;
- who may approve risky but authorized work;
- how local coder remains ergonomic under default-deny authorization.

The goal is to make authorization, risk classification, approval, sandboxing,
and execution distinct layers that compose cleanly.

## Actor Inventory

Authority-bearing actors:

- **Canonical user**: a stable runtime/app user such as
  `timo@company.org` or `timo@localhost`.
- **Unresolved external user**: a channel-specific human identity that has not
  resolved to a canonical user yet, such as Slack user `U123`. This actor must
  default to low trust.
- **System/app/daemon actor**: the app or runtime acting from configuration or
  lifecycle, such as `system:daemon`, `system:scheduler`, `system:startup`,
  or `system:reconciler`.
- **Service account**: a non-human caller authenticated by local config, JWT,
  CI, webhook auth, or another machine-to-machine mechanism, such as
  `service:ci` or `service:incident-bot`.
- **Agent actor**: a configured agent or live agent instance, such as
  `agent:main`, `agent:planner`, or `agent:worker`.
- **Task/scheduler worker**: a runtime worker claiming or executing scheduled
  task work, such as `worker:scheduler-a`.
- **Connector credential subject**: the identity represented by an external
  credential, such as a Slack bot token, GitHub app installation, Jira service
  account, or user OAuth token.
- **Admin/operator actor**: a user or service controlling daemon lifecycle,
  configuration, scheduler state, connector setup, or other administrative
  surfaces.
- **Approver**: the actor who authorizes an approval-required action. The
  approver may differ from the requester.

Security-relevant evidence or components that should not be treated as primary
authority-bearing actors:

- **Transport evidence**: Unix socket peer, local process, TCP listener, JWT,
  mTLS certificate, or similar source evidence.
- **Channel identity**: Slack user ID, Discord user ID, email claim, OIDC
  subject, or provider-specific claims.
- **LLM/model**: an untrusted decision engine inside an agent runtime. The
  model may request tool calls, but it is never the source of authority.
- **Operation handler**: an execution component. It receives an effective
  security context but must not invent authority.
- **Resource owner metadata**: workspace owner, session owner, task owner, or
  datasource owner. These are important for policy decisions, but they are not
  always the actor currently making a request.

## Canonical Identity

Canonical user identity is independent of channel identity.

Example resolved Slack input:

```text
user=timo@company.org
identity={provider:"slack", provider_id:"U123", claims:{is_admin:"true"}}
groups=[engineering, docs_indexers]
trust=internal
```

Before resolution, a Slack input may only have:

```text
identity={provider:"slack", provider_id:"U123"}
user=<unresolved or synthetic public user>
trust=public/untrusted
```

The identity resolver may use channel claims, provider lookups, app config, or
directory mappings to resolve the canonical user and groups. If it cannot
resolve a known user, it should create or synthesize an untrusted actor rather
than silently escalating.

### Local Coder User

`apps/coder` currently hard-codes the local principal as:

```go
policy.Caller{
	Kind: policy.CallerUser,
	Principal: policy.Principal{
		Kind: "user",
		ID:   "agentsdk",
		Name: "agentsdk",
	},
}
```

The default resolver therefore produces a canonical user like:

```text
user.id=agentsdk
user.username=agentsdk
identity.provider=user
identity.provider_id=agentsdk
trust=internal
```

That is stable but not meaningful. Local coder should instead synthesize:

```text
<os-username>@localhost
```

For example:

```text
timo@localhost
```

Suggested local username resolution order:

1. `os/user.Current().Username`
2. `$USER`
3. `$USERNAME`
4. `uid-<uid>@localhost`
5. `local@localhost`

Normalize domain-style OS usernames for the canonical user while preserving the
raw value as identity evidence:

```text
DOMAIN\timo -> timo@localhost
machine\timo -> timo@localhost
timo -> timo@localhost
```

Local coder should produce a user actor shaped like:

```go
policy.Caller{
	Kind: policy.CallerUser,
	Principal: policy.Principal{
		Kind: "local_user",
		ID:   "timo@localhost",
		Name: "timo",
	},
	Source: "local",
}
```

and a resolved actor like:

```go
user.Actor{
	User: user.User{
		ID:       "timo@localhost",
		Username: "timo@localhost",
		Groups:   []user.ID{"local_users", "local_operators"},
		Trust:    user.TrustOperator,
	},
	Identity: user.Identity{
		Provider:   "local",
		ProviderID: "<uid or raw os username>",
		Claims: map[string]string{
			"hostname": "...",
			"uid": "...",
		},
	},
}
```

For local coder, the invocation trust can be privileged with broad local scopes:

```go
policy.Trust{
	Kind:       policy.TrustInvocation,
	Level:      policy.TrustPrivileged,
	Scopes:     []policy.Scope{"*"},
	VerifiedBy: "local_process",
	Reason:     "local coder CLI",
}
```

This is a distribution default for local coder only. It is not a framework-wide
default.

### Direct, Socket, And HTTP Identities

Suggested defaults:

- Generic `directchannel` remains `user`/`untrusted` unless configured by the
  embedding app.
- Local direct coder configures a local canonical user and local grants.
- Unix socket listeners may derive trust from local peer authority or listener
  configuration. Socket clients may request a typed trust downgrade, but they
  cannot freely claim arbitrary caller/trust values.
- HTTP/SSE derives identity and trust from listener auth, JWT, mTLS, or other
  configured auth. It must not trust request body authority fields.

Trust downgrade should remain explicit:

```go
type TrustDowngrade struct {
	Level  policy.TrustLevel `json:"level,omitempty"`
	Scopes []policy.Scope    `json:"scopes,omitempty"`
	Reason string            `json:"reason,omitempty"`
}
```

Downgraded scopes must be a subset of the listener/transport authority.

## Invocation Security Context

The runtime needs a security context that is richer than `Caller` and `Trust`.
It should preserve provenance and delegation.

Conceptual shape:

```go
type ActorKind string

const (
	ActorUser      ActorKind = "user"
	ActorExternal  ActorKind = "external"
	ActorSystem    ActorKind = "system"
	ActorService   ActorKind = "service"
	ActorAgent     ActorKind = "agent"
	ActorWorker    ActorKind = "worker"
	ActorConnector ActorKind = "connector"
)

type ActorRef struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
	Name string    `json:"name,omitempty"`
}

type InvocationSubject struct {
	Root       ActorRef   `json:"root"`
	Current    ActorRef   `json:"current"`
	OnBehalfOf *ActorRef  `json:"on_behalf_of,omitempty"`
	Delegation []ActorRef `json:"delegation,omitempty"`
}

type SecurityContext struct {
	Subject InvocationSubject `json:"subject"`

	Caller policy.Caller `json:"caller"`
	Trust  policy.Trust  `json:"trust"`

	User     *user.User     `json:"user,omitempty"`
	Identity *user.Identity `json:"identity,omitempty"`
	Groups   []user.Group   `json:"groups,omitempty"`

	Transport *TransportEvidence `json:"transport,omitempty"`
	Channel   *ChannelEvidence   `json:"channel,omitempty"`
	Approval  *ApprovalEvidence  `json:"approval,omitempty"`
}
```

Agents and workers should usually act under delegation:

```text
root=user:timo@company.org via slack
current=agent:worker
on_behalf_of=user:timo@company.org
delegation=[agent:main, agent:worker]
```

Effective authorization subjects should include both delegated human/group
subjects and current actor subjects:

```text
user:timo@company.org
group:engineering
group:docs_indexers
agent:worker
```

The invocation trust/scopes still limit authority. Membership in a powerful
group must not bypass a Unix socket or remote-channel trust downgrade.

## Resource Authorization Model

Authorization answers:

> May this subject perform this action on this resource?

It must be separate from risk classification, approval, sandboxing, and
execution.

Two checks are needed:

- **Operation-level capability check**: can this actor invoke this kind of
  capability at all?
- **Resource-level target check**: can this actor perform this action on this
  specific target resource?

Examples:

```text
datasource_search -> requires datasource.search on datasource:<name>
datasource_get -> requires datasource.read on datasource:<name>
datasource_reindex -> requires datasource.index on datasource:<name>
file_read -> requires workspace.read / path read
file_edit -> requires workspace.write / path write
shell_exec -> requires process.exec
target_submit -> requires channel.send
```

Do not encode app policy as operation-specific group checks like:

```yaml
operation.datasource_reindex.required_groups: [docs_indexers]
```

Prefer resource grants:

```yaml
security:
  grants:
    - subjects:
        - kind: group
          id: docs_indexers
      resources:
        - kind: datasource
          name: local_docs
      actions:
        - datasource.index
```

Then `datasource_reindex` only declares that it requires
`datasource.index` on the target datasource.

## Typed Policy Shapes

Policy shapes should be typed. Avoid `map[string]any` for authorization
contracts.

Suggested `core/policy` primitives:

```go
type Action string

type SubjectKind string

const (
	SubjectUser    SubjectKind = "user"
	SubjectGroup   SubjectKind = "group"
	SubjectService SubjectKind = "service"
	SubjectSystem  SubjectKind = "system"
	SubjectAgent   SubjectKind = "agent"
)

type SubjectRef struct {
	Kind SubjectKind `json:"kind" yaml:"kind"`
	ID   string      `json:"id" yaml:"id"`
}

type ResourceKind string

const (
	ResourceDatasource ResourceKind = "datasource"
	ResourceWorkspace  ResourceKind = "workspace"
	ResourcePath       ResourceKind = "path"
	ResourceProcess    ResourceKind = "process"
	ResourceNetwork    ResourceKind = "network"
	ResourceConnector  ResourceKind = "connector"
	ResourceChannel    ResourceKind = "channel"
	ResourceTask       ResourceKind = "task"
	ResourceSession    ResourceKind = "session"
	ResourceAdmin      ResourceKind = "admin"
	ResourceModel      ResourceKind = "model"
	ResourceOperation  ResourceKind = "operation"
)

type ResourceRef struct {
	Kind ResourceKind `json:"kind" yaml:"kind"`
	ID   string       `json:"id,omitempty" yaml:"id,omitempty"`
	Name string       `json:"name,omitempty" yaml:"name,omitempty"`
	Path string       `json:"path,omitempty" yaml:"path,omitempty"`
}

type Grant struct {
	Subjects         []SubjectRef  `json:"subjects,omitempty" yaml:"subjects,omitempty"`
	Resources        []ResourceRef `json:"resources,omitempty" yaml:"resources,omitempty"`
	Actions          []Action      `json:"actions,omitempty" yaml:"actions,omitempty"`
	RequiredTrust    TrustLevel    `json:"required_trust,omitempty" yaml:"required_trust,omitempty"`
	RequiredScopes   []Scope       `json:"required_scopes,omitempty" yaml:"required_scopes,omitempty"`
	RequiresApproval bool          `json:"requires_approval,omitempty" yaml:"requires_approval,omitempty"`
}

type AuthorizationPolicy struct {
	Grants []Grant `json:"grants,omitempty" yaml:"grants,omitempty"`
}

type AuthorizationRequest struct {
	Subjects []SubjectRef `json:"subjects,omitempty"`
	Trust    Trust        `json:"trust,omitempty"`
	Resource ResourceRef  `json:"resource"`
	Action   Action       `json:"action"`
}
```

Built-in actions:

```text
datasource.read
datasource.search
datasource.index
datasource.admin

workspace.read
workspace.write
workspace.admin

process.exec
process.admin

network.fetch
network.connect

connector.use
connector.manage

channel.send
channel.admin

task.read
task.write
task.run
task.admin

session.read
session.write
session.admin

model.invoke
operation.invoke
approval.grant
```

Evaluation rules:

- default deny;
- subject must match at least one effective subject;
- resource must match the requested target;
- action must match exactly or by supported wildcard;
- required trust must be satisfied;
- required scopes must be present on the invocation trust;
- approval-required grants return an approval-required decision, not allow.

## Wildcard Grants

Default deny is only usable if local apps can express broad grants compactly.
Support simple typed wildcards in v1:

- exact action match: `datasource.read`;
- namespace action wildcard: `datasource.*`;
- all-name resource wildcard: `name: "*"`;
- path wildcard: `**`;
- path prefix wildcard: `docs/**`.

Do not add arbitrary regex, CEL, Rego, or scriptable policy in the first pass.
The initial matcher should be boring and explainable.

Example local coder policy:

```yaml
security:
  grants:
    - subjects:
        - kind: user
          id: timo@localhost
        - kind: group
          id: local_operators
      resources:
        - kind: workspace
          name: "*"
        - kind: path
          path: "**"
        - kind: process
          name: "*"
        - kind: network
          name: "*"
        - kind: connector
          name: "*"
        - kind: task
          name: "*"
        - kind: session
          name: "*"
        - kind: datasource
          name: "*"
        - kind: model
          name: "*"
      actions:
        - workspace.*
        - process.*
        - network.*
        - connector.*
        - task.*
        - session.*
        - datasource.*
        - model.invoke
        - operation.invoke
        - approval.grant
```

Example Slack docs policy:

```yaml
security:
  grants:
    - subjects:
        - kind: group
          id: slack_users
      resources:
        - kind: datasource
          name: local_docs
      actions:
        - datasource.search
        - datasource.read

    - subjects:
        - kind: group
          id: docs_indexers
      resources:
        - kind: datasource
          name: local_docs
      actions:
        - datasource.*
```

## cmdrisk Placement

Keep `cmdrisk`, but do not treat it as authorization.

Authorization answers:

> Is this actor allowed to perform this action on this resource?

cmdrisk answers:

> Given this already-authorized concrete execution intent, how risky is it?

The layered order should be:

```text
1. Authorization
2. Secret guard
3. Dynamic risk classification via cmdrisk
4. Approval
5. Sandbox
6. Handler/System execution
```

The current `runtime/operation.SafetyEnvelope` already largely follows this
shape:

```text
ACL -> Secrets -> CommandRisk -> Approval -> Sandbox
```

Keep `cmdrisk` behind `runtime/operation.CommandRiskClassifier`.

Do not move cmdrisk only into `System.Process()` unless `System.Process()` can
be called outside the operation safety envelope. The preferred invariant is:

```text
operation handler -> SafetyEnvelope has passed -> runtime/system execution
```

Any process, shell, network, browser, or code-exec operation must expose typed
`operation.IntentSet` so the safety envelope can classify concrete intent
before execution.

Reusable plugins should continue using `runtime/system.System` for filesystem,
network, process, browser, and clarification access. They must not bypass the
operation safety envelope for side effects.

## Approval Model

Approval is distinct from authorization. Missing grants cannot be approved
around.

Denied and not approvable:

```text
user lacks datasource.index on datasource:local_docs
```

Potentially approvable:

```text
user has datasource.index on datasource:local_docs
grant, operation semantics, or cmdrisk requires approval
```

Approval may be required by:

- a matching grant with `requires_approval`;
- operation semantics, such as high/critical/destructive risk;
- cmdrisk returning `RequiresApproval`;
- risk exceeding `MaxCommandRisk` when the runtime is configured to ask for
  approval instead of failing immediately.

Approval requests should carry enough context for audit and UX:

```go
type ApprovalRequest struct {
	Subject  policy.InvocationSubject `json:"subject"`
	Resource policy.ResourceRef        `json:"resource"`
	Action   policy.Action             `json:"action"`

	Spec  operation.Spec  `json:"spec"`
	Input operation.Value `json:"input,omitempty"`
	Risk  CommandRisk     `json:"risk,omitempty"`

	Reason string `json:"reason,omitempty"`
}
```

Approvers need explicit authority, modeled as `approval.grant`.

Example:

```yaml
security:
  grants:
    - subjects:
        - kind: group
          id: operators
      resources:
        - kind: datasource
          name: local_docs
      actions:
        - approval.grant
```

Local coder:

- terminal approval remains the default approval UI;
- `--yolo` remains explicit local auto-approval;
- auto-approval must not bypass authorization, secret guard, or sandbox checks.

Remote/channel apps:

- if an action requires approval and no approval flow is configured, fail
  closed;
- Slack or HTTP approval UX can be added later as adapter-specific approval
  gates.

## Enforcement Layers

Enforce authorization in multiple places:

1. **Projection time**
   - Hide tools, commands, and datasource capabilities the effective security
     context cannot use.
   - Projection is a model-facing minimization layer, not the final gate.

2. **Operation execution time**
   - Re-check operation-level capability authorization in the safety envelope.
   - Direct tool-call attempts for unprojected or denied operations must fail.

3. **Resource target time**
   - Dynamic operations check the concrete target resource parsed from typed
     input.
   - Example: `datasource_get(name="local_docs")` authorizes against
     `datasource:local_docs`; `file_edit(path="docs/a.md")` authorizes against
     the relevant workspace/path resource.

4. **Risk, approval, and sandbox**
   - Secret guard, cmdrisk, approval, and sandbox remain independent gates.

Protected resource families for the first full pass:

- datasources: read, search, index, admin;
- workspace filesystem: read, write, admin on roots and paths;
- process/shell/background process: exec, admin;
- network/web/browser: fetch/connect with target classification;
- connectors: use/manage per connector instance;
- channels: send/admin per channel;
- tasks: read/write/run/admin;
- sessions/threads: read/write/admin;
- model/provider invocation;
- fallback `operation.invoke` for operations that are not yet
  resource-specialized.

## App And Distribution Config

Add app-level security policy to the inert app/distribution config path:

- `core/app.Spec` gets `Security policy.AuthorizationPolicy`.
- local app/distribution YAML decodes `security.grants` into the typed policy.
- `orchestration/distribution.LaunchConfig` carries the loaded policy to
  launch/runtime assembly as needed.
- existing daemon/channel `access` remains channel identity/trust resolution
  metadata, not resource authorization.

Channel access still answers:

```text
who may enter this channel and with which channel trust defaults?
```

Resource authorization answers:

```text
what may this resolved actor do to which resource?
```

Those should not collapse into one config shape.

## Coder Defaults

Default-deny should not require a huge manual YAML file for local coder.

Local coder should install compact distribution-local default grants when no
explicit app policy is provided:

- canonical local user: `<os-username>@localhost`;
- local groups: at least `local_users` and `local_operators`;
- invocation trust: privileged local invocation with broad local scopes;
- grants over local workspace, path, process, network, connector, task,
  session, datasource, model, operation, and approval actions.

These defaults only apply to local coder. They do not apply to:

- generic direct channel embeddings;
- Slack bot deployments;
- HTTP/SSE remote listeners;
- daemon apps unless the distribution opts in.

Remote coder over HTTP/SSE should derive identity from listener auth/JWT/socket
authority. It should not reuse `timo@localhost` unless the listener is actually
a local socket with local peer authority.

## Implementation Notes

Suggested placement:

- `core/policy`: pure typed authorization shapes, constants, wildcard matcher,
  and evaluation helper.
- `core/app`: inert `Security policy.AuthorizationPolicy` on app specs.
- `adapters/appconfig` and distribution local loader: YAML decode and launch
  propagation.
- `orchestration/identity`: canonical user/group resolution and local identity
  helper hooks.
- `orchestration/security` or equivalent: build effective security context and
  policy subjects for sessions, agents, workers, and system runs.
- `orchestration/toolprojection`: apply authorization during projection.
- `runtime/operation`: authorizer/ACL implementation in the safety envelope and
  richer approval requests.
- resource-specific plugins/adapters: call shared authorization helpers for
  dynamic target checks.

Typed operation inputs should be used for target extraction. Avoid spreading
ad hoc `map[string]any` parsing across handlers.

## Test And Acceptance Criteria

Required behavior:

- default-deny blocks datasource read/search/index without grants;
- Slack users can read/search `local_docs` when granted and cannot reindex
  without `datasource.index`;
- `docs_indexers` can reindex only when invocation trust/scopes also allow
  `datasource.index`;
- privileged Unix socket authority downgraded to read scopes cannot perform
  excluded write/index/admin actions;
- direct agent grants work, while delegated user/group subjects remain
  available;
- projection hides denied tools;
- direct denied tool calls fail at execution;
- workspace read/write grants distinguish file read from file edit;
- process, network, connector, channel, task, session, model, and operation
  fallback gates reject missing grants;
- cmdrisk approval does not bypass authorization;
- approval-required operations fail closed without an approval gate;
- local coder works with compact default local grants;
- canonical local coder user is no longer `agentsdk` and resolves to
  `<os-username>@localhost`;
- `task verify` passes. Use `env -u TAVILY_API_KEY task verify` if the local
  Tavily key makes web plugin tests nondeterministic.

## Non-Goals For The First Pass

- Do not add a general policy language such as Rego, CEL, or JavaScript.
- Do not make local coder grants a framework-wide default.
- Do not allow approval to override missing grants.
- Do not treat the LLM/model as an authority-bearing actor.
- Do not make operation handlers responsible for inventing authorization
  semantics.
- Do not collapse daemon/channel access metadata with resource authorization.
