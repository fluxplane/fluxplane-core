package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/activation"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/slack-go/slack"
)

const (
	Name             = "slack"
	OperationSet     = Name + ".channel"
	ChannelSendOp    = "channel_send"
	ReportProgressOp = "slack_report_progress"
	ThreadReplyOp    = "slack_thread_reply"
)

type slackClientFactory func(token, appToken string) *slack.Client

type Plugin struct {
	pluginhost.Configurable[Config]
	system        system.System
	store         runtimesecret.FileStore
	secrets       runtimesecret.Resolver
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
var _ pluginhost.AuthTestContributor = Plugin{}

func New(sys system.System, stores ...runtimesecret.FileStore) Plugin {
	return NewWithDispatcher(sys, nil, stores...)
}

func NewWithDispatcher(sys system.System, dispatcher *Dispatcher, stores ...runtimesecret.FileStore) Plugin {
	return NewWithResolver(sys, dispatcher, nil, stores...)
}

func NewWithResolver(sys system.System, dispatcher *Dispatcher, resolver runtimesecret.Resolver, stores ...runtimesecret.FileStore) Plugin {
	if dispatcher == nil {
		dispatcher = NewDispatcher()
	}
	store := runtimesecret.NewFileStore(DefaultAuthStorePath)
	if len(stores) > 0 {
		store = stores[0]
	}
	if resolver == nil {
		resolver = store
	}
	return Plugin{system: sys, store: store, secrets: resolver, dispatcher: dispatcher}
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
	setName := p.ref.InstanceName()
	if setName == "" {
		setName = Name
	}
	operationSetName := setName + ".channel"
	if setName == Name {
		operationSetName = OperationSet
	}
	aliases := []string{setName + ".default", "channel"}
	return resource.ContributionBundle{
		Operations: []operation.Spec{p.channelSendSpec(), p.reportProgressSpec(), p.threadReplySpec()},
		OperationSets: []operation.Set{{
			Name:        operationSetName,
			Description: "Slack active-channel reply, explicit thread reply, and progress operations.",
			Operations: []operation.Ref{
				{Name: ChannelSendOp},
				{Name: ThreadReplyOp},
				{Name: "slack_*"},
			},
		}},
		ActivationSets: []activation.Set{{
			Name:        setName,
			Aliases:     aliases,
			Description: "Slack channel operations.",
			Targets: []activation.Target{{
				Kind:         activation.TargetOperationSet,
				OperationSet: operationSetName,
			}},
		}},
		DataSources: []coredata.SourceSpec{DataSourceSpec()},
	}, nil
}

func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	p = p.withRef(ctx.Ref)
	return []operation.Operation{
		operationruntime.NewTypedResult[channelSendInput, channelSendOutput](p.channelSendSpec(), p.channelSend),
		operationruntime.NewTypedResult[reportProgressInput, reportProgressOutput](p.reportProgressSpec(), p.reportProgress),
		operationruntime.NewTypedResult[threadReplyInput, threadReplyOutput](p.threadReplySpec(), p.threadReply),
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

func (p Plugin) TestConnection(ctx context.Context, pluginCtx pluginhost.Context, req pluginhost.AuthTestRequest, reports chan<- pluginhost.AuthTestReport) error {
	ref := req.Ref
	if ref.Name == "" {
		ref = pluginCtx.Ref
	}
	p = p.withRef(ref)
	cfg := p.cfg
	if method := strings.TrimSpace(req.Method); method != "" {
		cfg.Auth.Method = method
	}
	session, err := ResolveWithResolver(ctx, p.system, req.Secrets, p.ref, cfg)
	if err != nil {
		reports <- p.authTestReport(session.Method, "connection", "failed", err.Error(), nil)
		return nil
	}
	method := firstNonEmpty(session.Method, cfg.Auth.Method)
	p.testSlackAPIToken(ctx, reports, method, BotTokenPurpose, session.BotToken, session.AppToken)
	p.testSlackAPIToken(ctx, reports, method, UserTokenPurpose, session.UserToken, session.AppToken)
	if strings.TrimSpace(session.AppToken) != "" {
		reports <- p.authTestReport(method, AppTokenPurpose, "present", "", nil)
	}
	return nil
}

func (p Plugin) testSlackAPIToken(ctx context.Context, reports chan<- pluginhost.AuthTestReport, method, check, token, appToken string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	resp, err := p.newClient(token, appToken).AuthTestContext(ctx)
	if err != nil {
		reports <- p.authTestReport(method, check, "failed", err.Error(), nil)
		return
	}
	message := strings.TrimSpace(resp.Team)
	if user := strings.TrimSpace(resp.UserID); user != "" {
		if message != "" {
			message += ", "
		}
		message += "user " + user
	}
	reports <- p.authTestReport(method, check, "ok", message, map[string]string{
		"team":    strings.TrimSpace(resp.Team),
		"team_id": strings.TrimSpace(resp.TeamID),
		"user_id": strings.TrimSpace(resp.UserID),
		"bot_id":  strings.TrimSpace(resp.BotID),
	})
}

func (p Plugin) authTestReport(method, check, status, message string, details map[string]string) pluginhost.AuthTestReport {
	return pluginhost.AuthTestReport{
		Plugin:   Name,
		Instance: p.ref.InstanceName(),
		Method:   strings.TrimSpace(method),
		Check:    strings.TrimSpace(check),
		Status:   strings.TrimSpace(status),
		Message:  strings.TrimSpace(message),
		Details:  details,
	}
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
	if p.secrets != nil {
		return ResolveWithResolver(ctx, p.system, p.secrets, p.ref, p.cfg)
	}
	return Resolve(ctx, p.system, p.store, p.ref, p.cfg)
}

func (p Plugin) api(ctx context.Context) (slackAPI, error) {
	session, err := p.session(ctx)
	if err != nil {
		return nil, err
	}
	token := firstNonEmpty(session.UserToken, session.BotToken)
	if token == "" {
		return nil, fmt.Errorf("slackplugin: bot token or user token is not configured")
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
		Description: "Send a user-visible intermediate message to the current Slack thread. Prefer slack_report_progress for concise progress updates during long-running Slack requests.",
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

func (p Plugin) threadReplySpec() operation.Spec {
	return operationruntime.WithTypedContract[threadReplyInput, threadReplyOutput](operation.Spec{
		Ref:         operation.Ref{Name: ThreadReplyOp},
		Description: "Send a user-visible message to an explicit Slack thread target. Use channel_send instead when the current request came from Slack and already has an active Slack channel turn.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectWriteExternal},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

type threadReplyInput struct {
	Text      string `json:"text" jsonschema:"description=Message text to post.,required"`
	Permalink string `json:"permalink,omitempty" jsonschema:"description=Slack message permalink identifying the thread to reply to."`
	ChannelID string `json:"channel_id,omitempty" jsonschema:"description=Slack channel id. Required when permalink is omitted."`
	ThreadTS  string `json:"thread_ts,omitempty" jsonschema:"description=Slack thread timestamp. Required when permalink is omitted."`
}

type threadReplyOutput struct {
	Channel string `json:"channel,omitempty"`
	Thread  string `json:"thread,omitempty"`
	Ts      string `json:"ts,omitempty"`
}

func (p Plugin) threadReply(ctx operation.Context, input threadReplyInput) operation.Result {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return operation.Failed("invalid_slack_thread_reply_input", "text is required", nil)
	}
	channelID, threadTS, ok := explicitThreadTarget(input)
	if !ok {
		return operation.Failed("invalid_slack_thread_reply_input", "permalink or channel_id and thread_ts are required", nil)
	}
	api, err := p.api(ctx)
	if err != nil {
		return operation.Failed("slack_thread_reply_auth_failed", err.Error(), nil)
	}
	_, ts, err := api.PostMessageContext(ctx, channelID, slack.MsgOptionText(text, false), slack.MsgOptionTS(threadTS))
	if err != nil {
		return operation.Failed("slack_thread_reply_failed", err.Error(), nil)
	}
	return operation.OK(threadReplyOutput{Channel: channelID, Thread: threadTS, Ts: ts})
}

func explicitThreadTarget(input threadReplyInput) (string, string, bool) {
	if channelID, threadTS, ok := slackMessageTargetFromPermalink(input.Permalink); ok {
		return channelID, threadTS, true
	}
	channelID := strings.TrimSpace(input.ChannelID)
	threadTS := strings.TrimSpace(input.ThreadTS)
	return channelID, threadTS, channelID != "" && threadTS != ""
}

func (p Plugin) reportProgressSpec() operation.Spec {
	return operationruntime.WithTypedContract[reportProgressInput, reportProgressOutput](operation.Spec{
		Ref:         operation.Ref{Name: ReportProgressOp},
		Description: "Set concise, user-visible Slack assistant status for a long-running request. Use this in Slack turns when work takes more than a few seconds. Do not include secrets, raw tool inputs, or chain-of-thought.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectWriteExternal},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

type reportProgressInput struct {
	Status string `json:"status" jsonschema:"description=Short user-visible status to show in Slack.,required"`
	Detail string `json:"detail,omitempty" jsonschema:"description=Optional concise progress detail safe for the user to see."`
	Done   bool   `json:"done,omitempty" jsonschema:"description=Set true when this progress item is complete or the request is finishing."`
}

type reportProgressOutput struct {
	Channel string `json:"channel,omitempty"`
	Thread  string `json:"thread,omitempty"`
}

func (p Plugin) reportProgress(ctx operation.Context, input reportProgressInput) operation.Result {
	target, ok := TargetFromContext(ctx)
	if !ok {
		return operation.Failed("slack_channel_missing", "slack_report_progress requires an active Slack channel turn", nil)
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		return operation.Failed("invalid_slack_report_progress_input", "status is required", nil)
	}
	if observer, ok := RunObserverFromContext(ctx); ok && observer != nil {
		if input.Done {
			observer.setStatus(ctx, "")
		} else {
			observer.setStatusImmediate(ctx, status)
		}
	}
	return operation.OK(reportProgressOutput{Channel: target.ChannelID, Thread: target.ThreadTS})
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
