package slack

import (
	"context"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-system/systemkit"
	"github.com/slack-go/slack"
)

const (
	// Name is the pluginhost registration name for the native slack
	// channel adapter. It is intentionally NOT "slack" so the dex slack
	// marketplace plugin (which registers under "slack" with its richer
	// op surface — slack.message.send, slack.reaction.add, …) can be
	// loaded alongside without colliding. Apps that need both the
	// channel-adapter side (this plugin) and the messaging-op side (dex
	// slack) reference them by these distinct names.
	Name = "slack_channel"
	// IdentityProvider is the user.Identity Provider value emitted for
	// Slack-resolved identities. It stays "slack" so identity records
	// and admin allowlists tied to the Slack workspace keep matching
	// across the plugin rename.
	IdentityProvider = "slack"
	OperationSet     = Name + ".channel"
	ChannelSendOp    = "channel_send"
	ChannelPostOp    = "channel_post"
	ReportProgressOp = "slack_report_progress"
)

type slackClientFactory func(token, appToken string) *slack.Client

type Plugin struct {
	pluginhost.Configurable[Config]
	network       fpsystem.Network
	environment   fpsystem.Environment
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
var _ pluginhost.AuthMethodContributor = Plugin{}
var _ pluginhost.AuthTestContributor = Plugin{}

func New(sys fpsystem.System, stores ...runtimesecret.FileStore) Plugin {
	return NewWithDispatcher(sys, nil, stores...)
}

func NewWithDispatcher(sys fpsystem.System, dispatcher *Dispatcher, stores ...runtimesecret.FileStore) Plugin {
	return NewWithResolver(sys, dispatcher, nil, stores...)
}

func NewWithResolver(sys fpsystem.System, dispatcher *Dispatcher, resolver runtimesecret.Resolver, stores ...runtimesecret.FileStore) Plugin {
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
	network, environment := boundariesFromSystem(sys)
	return Plugin{network: network, environment: environment, store: store, secrets: resolver, dispatcher: dispatcher}
}

func boundariesFromSystem(sys fpsystem.System) (fpsystem.Network, fpsystem.Environment) {
	if sys == nil {
		return nil, nil
	}
	return sys.Network(), sys.Environment()
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
		Operations: []operation.Spec{p.channelSendSpec(), p.channelPostSpec(), p.reportProgressSpec()},
		OperationSets: []operation.Set{{
			Name:        operationSetName,
			Description: "Slack active-channel reply and progress operations.",
			Operations: []operation.Ref{
				{Name: ChannelSendOp},
				{Name: ReportProgressOp},
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
	}, nil
}

func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	p = p.withRef(ctx.Ref)
	return []operation.Operation{
		operationruntime.NewTypedResult[channelSendInput, channelSendOutput](p.channelSendSpec(), p.channelSend),
		operationruntime.NewTypedResult[channelPostInput, channelSendOutput](p.channelPostSpec(), p.channelPost),
		operationruntime.NewTypedResult[reportProgressInput, reportProgressOutput](p.reportProgressSpec(), p.reportProgress),
	}, nil
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
	session, err := ResolveWithEnvironment(ctx, p.environment, req.Secrets, p.ref, cfg)
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

func (p Plugin) newClient(token, appToken string) *slack.Client {
	factory := p.clientFactory
	if factory != nil {
		return factory(token, appToken)
	}
	options := []slack.Option{}
	if appToken != "" {
		options = append(options, slack.OptionAppLevelToken(appToken))
	}
	if p.network != nil {
		options = append(options, slack.OptionHTTPClient(systemkit.NewHTTPClient(p.network)))
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

// channelPostSpec describes the unsolicited-post entry point. Unlike
// channel_send, channel_post does not require an active Slack channel
// turn — callers (background agents, scheduled triggers, …) supply the
// destination channel_id directly, and may target a thread by passing
// thread_ts. Use channel_send inside channel turns to keep replies
// glued to the active thread; reach for channel_post when posting
// somewhere the bot was not invoked from.
func (p Plugin) channelPostSpec() operation.Spec {
	return operationruntime.WithTypedContract[channelPostInput, channelSendOutput](operation.Spec{
		Ref:         operation.Ref{Name: ChannelPostOp},
		Description: "Post a message to an explicit Slack channel id without requiring an active Slack turn. Use channel_send when replying inside the current thread; use channel_post for unsolicited messages from background agents and scheduled triggers.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectWriteExternal},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

type channelPostInput struct {
	ChannelID string `json:"channel_id" jsonschema:"description=Slack channel id (e.g. C012ABCDEF) to post into.,required"`
	Text      string `json:"text" jsonschema:"description=Message text to post.,required"`
	ThreadTS  string `json:"thread_ts,omitempty" jsonschema:"description=Optional Slack thread timestamp; when set the post lands inside that thread instead of the channel root."`
}

func (p Plugin) channelPost(ctx operation.Context, input channelPostInput) operation.Result {
	channelID := strings.TrimSpace(input.ChannelID)
	if channelID == "" {
		return operation.Failed("invalid_channel_post_input", "channel_id is required", nil)
	}
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return operation.Failed("invalid_channel_post_input", "text is required", nil)
	}
	target := Target{ChannelID: channelID, ThreadTS: strings.TrimSpace(input.ThreadTS)}
	if existing, ok := TargetFromContext(ctx); ok {
		// Carry over the channel name from any active turn so the
		// dispatcher can prefer the same workspace's poster.
		target.ChannelName = existing.ChannelName
		target.TeamID = existing.TeamID
	}
	ts, err := p.dispatcher.Post(ctx, target, text)
	if err != nil {
		return operation.Failed("slack_channel_post_failed", err.Error(), nil)
	}
	return operation.OK(channelSendOutput{Channel: target.ChannelID, Thread: target.ThreadTS, Ts: ts})
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
