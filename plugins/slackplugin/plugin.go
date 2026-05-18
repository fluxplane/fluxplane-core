package slackplugin

import (
	"context"
	"fmt"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
	"github.com/slack-go/slack"
)

const (
	Name          = "slack"
	ChannelSendOp = "channel_send"
)

type slackClientFactory func(token, appToken string) *slack.Client

type Plugin struct {
	pluginhost.Configurable[Config]
	system        system.System
	store         runtimesecret.FileStore
	ref           resource.PluginRef
	cfg           Config
	dispatcher    *Dispatcher
	clientFactory slackClientFactory
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.AuthMethodContributor = Plugin{}

func New(sys system.System, stores ...runtimesecret.FileStore) Plugin {
	return NewWithDispatcher(sys, nil, stores...)
}

func NewWithDispatcher(sys system.System, dispatcher *Dispatcher, stores ...runtimesecret.FileStore) Plugin {
	if dispatcher == nil {
		dispatcher = NewDispatcher()
	}
	store := runtimesecret.NewFileStore(DefaultAuthStorePath)
	if len(stores) > 0 {
		store = stores[0]
	}
	return Plugin{system: sys, store: store, dispatcher: dispatcher}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Slack channel and datasource integration."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[Config](ctx)
	if err != nil {
		return nil, err
	}
	p.ref = ctx.Ref
	p.cfg = NormalizeConfig(cfg)
	if p.dispatcher == nil {
		p.dispatcher = NewDispatcher()
	}
	return p, nil
}

func (p Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	p = p.withRef(ctx.Ref)
	return resource.ContributionBundle{Operations: []operation.Spec{p.channelSendSpec()}}, nil
}

func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	p = p.withRef(ctx.Ref)
	return []operation.Operation{
		operationruntime.NewTypedResult[channelSendInput, channelSendOutput](p.channelSendSpec(), p.channelSend),
	}, nil
}

func (p Plugin) DatasourceProviders(_ context.Context, ctx pluginhost.Context) ([]coredatasource.Provider, error) {
	p = p.withRef(ctx.Ref)
	return []coredatasource.Provider{slackDatasourceProvider{plugin: p}}, nil
}

func (p Plugin) AuthMethods(_ context.Context, ctx pluginhost.Context) ([]coresecret.AuthMethodSpec, error) {
	p = p.withRef(ctx.Ref)
	return AuthMethods(p.ref, p.cfg), nil
}

func (p Plugin) withRef(ref resource.PluginRef) Plugin {
	if p.ref.Name == "" && ref.Name != "" {
		p.ref = ref
	}
	if p.ref.Name == "" {
		p.ref.Name = Name
	}
	if p.dispatcher == nil {
		p.dispatcher = NewDispatcher()
	}
	return p
}

func (p Plugin) session(ctx context.Context) (Session, error) {
	return Resolve(ctx, p.system, p.store, p.ref, p.cfg)
}

func (p Plugin) api(ctx context.Context) (slackAPI, error) {
	session, err := p.session(ctx)
	if err != nil {
		return nil, err
	}
	token := firstNonEmpty(session.UserToken, session.BotToken)
	if token == "" {
		return nil, fmt.Errorf("slackplugin: bot token is not configured")
	}
	return p.newClient(token, session.AppToken), nil
}

func (p Plugin) newClient(token, appToken string) *slack.Client {
	factory := p.clientFactory
	if factory != nil {
		return factory(token, appToken)
	}
	options := []slack.Option{}
	if appToken != "" {
		options = append(options, slack.OptionAppLevelToken(appToken))
	}
	if p.system != nil && p.system.Network() != nil {
		options = append(options, slack.OptionHTTPClient(system.NewHTTPClient(p.system.Network())))
	}
	return slack.New(token, options...)
}

func (p Plugin) channelSendSpec() operation.Spec {
	return operationruntime.WithTypedContract[channelSendInput, channelSendOutput](operation.Spec{
		Ref:         operation.Ref{Name: ChannelSendOp},
		Description: "Send an intermediate message to the current Slack thread.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectWriteExternal},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

type channelSendInput struct {
	Text string `json:"text" jsonschema:"description=Message text to post to the current Slack thread.,required"`
}

type channelSendOutput struct {
	Channel string `json:"channel,omitempty"`
	Thread  string `json:"thread,omitempty"`
	Ts      string `json:"ts,omitempty"`
}

func (p Plugin) channelSend(ctx operation.Context, input channelSendInput) operation.Result {
	target, ok := TargetFromContext(ctx)
	if !ok {
		return operation.Failed("slack_channel_missing", "channel_send requires an active Slack channel turn", nil)
	}
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return operation.Failed("invalid_channel_send_input", "text is required", nil)
	}
	ts, err := p.dispatcher.Post(ctx, target, text)
	if err != nil {
		return operation.Failed("slack_channel_send_failed", err.Error(), nil)
	}
	return operation.OK(channelSendOutput{Channel: target.ChannelID, Thread: target.ThreadTS, Ts: ts})
}

func InvocationTarget() invocation.Target {
	return invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: ChannelSendOp}}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
