package evaluator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/adapters/httpssechannel"
	"github.com/fluxplane/agentruntime/core/channel"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/support/eventcatalog"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const TargetSubmitOperation = "target_submit"

type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

func NewPlugin() Plugin { return Plugin{} }

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: AppName, Description: "Evaluator target channel operations."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{Operations: []operation.Spec{targetSubmitSpec()}}, nil
}

func (Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{operationruntime.NewTypedResult[TargetSubmitInput, TargetSubmitOutput](targetSubmitSpec(), targetSubmit, operationruntime.WithIntent(targetSubmitIntent))}, nil
}

func targetSubmitSpec() operation.Spec {
	return operationruntime.WithTypedContract[TargetSubmitInput, TargetSubmitOutput](operation.Spec{
		Ref:         operation.Ref{Name: TargetSubmitOperation},
		Description: "Submit one prompt to a target app through the HTTP/SSE channel protocol and return summarized run evidence.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal, operation.EffectWriteExternal},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskMedium,
		},
	})
}

type TargetSubmitInput struct {
	BaseURL      string `json:"base_url,omitempty" jsonschema:"description=HTTP base URL; use http://unix with unix_socket"`
	UnixSocket   string `json:"unix_socket,omitempty" jsonschema:"description=Unix socket path for local target"`
	BearerToken  string `json:"bearer_token,omitempty" jsonschema:"description=optional bearer token"`
	TargetKind   string `json:"target_kind,omitempty" jsonschema:"description=human label such as coder, slack-bot, support-bot"`
	Session      string `json:"session,omitempty" jsonschema:"description=target session name; omitted uses the target default"`
	Conversation string `json:"conversation,omitempty" jsonschema:"description=conversation id"`
	Prompt       string `json:"prompt" jsonschema:"description=message to submit to target,required"`
	Timeout      string `json:"timeout,omitempty" jsonschema:"description=maximum wait duration"`
	ReplayEvents bool   `json:"replay_events,omitempty" jsonschema:"description=include replayed target events when the target transport provides them"`
}

type TargetSubmitOutput struct {
	ThreadID     string         `json:"thread_id,omitempty"`
	RunID        string         `json:"run_id,omitempty"`
	OutboundText string         `json:"outbound_text,omitempty"`
	Events       []EventSummary `json:"events,omitempty"`
	Error        string         `json:"error,omitempty"`
}

type EventSummary struct {
	Kind       string `json:"kind"`
	RunID      string `json:"run_id,omitempty"`
	Replayed   bool   `json:"replayed,omitempty"`
	Outbound   string `json:"outbound,omitempty"`
	Runtime    string `json:"runtime,omitempty"`
	Operation  string `json:"operation,omitempty"`
	AgentState string `json:"agent_status,omitempty"`
}

func targetSubmitIntent(_ operation.Context, in TargetSubmitInput) (operation.IntentSet, error) {
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" && strings.TrimSpace(in.UnixSocket) != "" {
		baseURL = "http://unix"
	}
	if baseURL == "" {
		return operation.IntentSet{}, fmt.Errorf("base_url or unix_socket is required")
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{{
		Behavior:  operation.IntentNetworkWrite,
		Target:    operation.URLTarget{URL: operation.URL(baseURL)},
		Role:      operation.IntentRoleNetworkTarget,
		Certainty: operation.IntentCertain,
	}}}, nil
}

func targetSubmit(ctx operation.Context, in TargetSubmitInput) operation.Result {
	if strings.TrimSpace(in.Prompt) == "" {
		return operation.Failed("target_submit_invalid_input", "prompt is required", nil)
	}
	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" && strings.TrimSpace(in.UnixSocket) != "" {
		baseURL = "http://unix"
	}
	if baseURL == "" {
		return operation.Failed("target_submit_invalid_input", "base_url or unix_socket is required", nil)
	}
	timeout := 2 * time.Minute
	if strings.TrimSpace(in.Timeout) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(in.Timeout))
		if err != nil {
			return operation.Failed("target_submit_invalid_timeout", err.Error(), nil)
		}
		timeout = parsed
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := httpssechannel.NewClient(httpssechannel.ClientConfig{
		BaseURL:     baseURL,
		UnixSocket:  strings.TrimSpace(in.UnixSocket),
		BearerToken: strings.TrimSpace(in.BearerToken),
		Events:      targetEventRegistry(),
	})
	if err != nil {
		return operation.Failed("target_submit_client_failed", err.Error(), nil)
	}
	session, err := client.Open(runCtx, clientapi.OpenRequest{
		Session:      coresession.Ref{Name: coresession.Name(strings.TrimSpace(in.Session))},
		Conversation: channel.ConversationRef{ID: strings.TrimSpace(in.Conversation)},
		Metadata:     map[string]string{"evaluator_target_kind": strings.TrimSpace(in.TargetKind)},
	})
	if err != nil {
		return operation.Failed("target_submit_open_failed", err.Error(), nil)
	}
	defer func() { _ = session.Close(context.Background()) }()
	run, err := session.Submit(runCtx, clientapi.NewSubmission().WithText(in.Prompt))
	if err != nil {
		return operation.Failed("target_submit_failed", err.Error(), nil)
	}
	var summaries []EventSummary
	for event := range run.Events() {
		if !in.ReplayEvents && event.Replayed {
			continue
		}
		summaries = append(summaries, summarizeEvent(event))
	}
	result, err := run.Wait(runCtx)
	out := TargetSubmitOutput{
		ThreadID: string(run.Session().Thread.ID),
		RunID:    string(run.ID()),
		Events:   summaries,
	}
	if result.Session.Thread.ID != "" {
		out.ThreadID = string(result.Session.Thread.ID)
	}
	if result.RunID != "" {
		out.RunID = string(result.RunID)
	}
	if result.Outbound != nil && result.Outbound.Message != nil {
		out.OutboundText = fmt.Sprint(result.Outbound.Message.Content)
	}
	if err != nil {
		out.Error = err.Error()
		return operation.OK(out)
	}
	return operation.OK(out)
}

func summarizeEvent(event clientapi.Event) EventSummary {
	out := EventSummary{Kind: string(event.Kind), RunID: string(event.RunID), Replayed: event.Replayed}
	if event.Outbound != nil && event.Outbound.Message != nil {
		out.Outbound = fmt.Sprint(event.Outbound.Message.Content)
	}
	if event.Runtime != nil {
		out.Runtime = string(event.Runtime.Name)
	}
	if event.Operation != nil {
		out.Operation = event.Operation.Operation.String()
	}
	if event.Agent != nil {
		out.AgentState = string(event.Agent.Status)
	}
	return out
}

func targetEventRegistry() *coreevent.Registry {
	registry, err := eventregistry.New(eventregistry.Config{EventTypes: eventcatalog.All()})
	if err != nil {
		return coreevent.NewRegistry()
	}
	return registry
}
