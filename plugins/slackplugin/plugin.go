package slackplugin

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/agentruntime/core/channel"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/channelruntime"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/connectorplugin"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

const (
	Name          = "slack"
	ChannelSendOp = "channel_send"
)

var requiredSlackBotEvents = []string{"app_mention", "message.im", "message.channels", "message.groups", "message.mpim"}

type Plugin struct {
	dispatcher *Dispatcher
	executor   connectorplugin.Executor
	instances  []connectorplugin.Instance
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ConnectorProviderContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}

func New(dispatcher *Dispatcher) Plugin {
	if dispatcher == nil {
		dispatcher = NewDispatcher()
	}
	return Plugin{dispatcher: dispatcher}
}

func NewWithConnectors(dispatcher *Dispatcher, executor connectorplugin.Executor, instances []connectorplugin.Instance) Plugin {
	plugin := New(dispatcher)
	plugin.executor = executor
	plugin.instances = append([]connectorplugin.Instance(nil), instances...)
	return plugin
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Slack channel integration."}
}

func (p Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	sendSpec := channelSendSpec()
	searchSpecs, err := connectorplugin.Specs(p.instances, slackConnectorActions())
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	specs := append([]operation.Spec{sendSpec}, searchSpecs...)
	return resource.ContributionBundle{
		Operations: specs,
	}, nil
}

func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	ops := []operation.Operation{
		operationruntime.NewTypedResult[channelSendInput, channelSendOutput](channelSendSpec(), p.channelSend),
	}
	searchOps, err := connectorplugin.Operations(p.executor, p.instances, slackConnectorActions())
	if err != nil {
		return nil, err
	}
	ops = append(ops, searchOps...)
	return ops, nil
}

func (Plugin) ConnectorProviders(context.Context, pluginhost.Context) ([]pluginhost.ConnectorProvider, error) {
	return []pluginhost.ConnectorProvider{{Name: Name}}, nil
}

func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
	userEntity := runtimedatasource.EntityOf[User](UserEntity, "Slack workspace user.")
	channelEntity := runtimedatasource.EntityOf[Channel](ChannelEntity, "Slack public or private channel.")
	channelEntity.Detectors = []coredatasource.DetectorSpec{
		{
			Name:       "slack_channel_ref",
			Kind:       coredatasource.DetectorRegex,
			Pattern:    `<#([A-Z0-9]+)(?:\|[^>]+)?>`,
			IDTemplate: "$1",
			Confidence: 0.9,
		},
		{
			Name:          "slack_channel_url",
			Kind:          coredatasource.DetectorURL,
			Pattern:       `https?://[^\s<>"']+/archives/([A-Z0-9]+)(?:[/?#][^\s<>"']*)?`,
			IDTemplate:    "$1",
			QueryTemplate: "$1",
			URLTemplate:   "$0",
			Confidence:    0.85,
		},
	}
	messageEntity := runtimedatasource.EntityOf[Message](MessageEntity, "Slack message search result.")
	messageEntity.Detectors = []coredatasource.DetectorSpec{
		{
			Name:          "slack_message_permalink",
			Kind:          coredatasource.DetectorURL,
			Pattern:       `https?://[^\s<>"']+/archives/([A-Z0-9]+)/p([0-9]{10})([0-9]{6})`,
			IDTemplate:    "$1:$2.$3",
			QueryTemplate: "$0",
			URLTemplate:   "$0",
			Confidence:    0.95,
		},
	}
	return []coredatasource.Provider{connectorplugin.NewDatasourceProvider(p.executor, []connectorplugin.DatasourceAction{
		{
			Kind:           Name,
			Entity:         userEntity,
			SearchOp:       "slack.user.list",
			GetOp:          "slack.user.info",
			QueryParam:     "-",
			LimitParam:     "limit",
			IDParam:        "user",
			CursorParam:    "cursor",
			NextCursorPath: "response_metadata.next_cursor",
			LocalFilter:    true,
			IDFields:       []string{"id"},
			TitleFields:    []string{"real_name", "profile.real_name", "profile.display_name", "name"},
			TextFields:     []string{"name", "team_id", "profile.email", "profile.title"},
			URLFields:      []string{"profile.image_original"},
			Corpus: connectorplugin.CorpusPolicy{
				TitleFields:   []string{"real_name", "profile.real_name", "profile.display_name", "name"},
				BodyFields:    []string{"name", "profile.title"},
				ExcludeFields: []string{"email"},
			},
			MetadataFields: map[string][]string{
				"team_id":      {"team_id"},
				"is_bot":       {"is_bot"},
				"deleted":      {"deleted"},
				"email":        {"profile.email"},
				"display_name": {"profile.display_name"},
			},
		},
		{
			Kind:           Name,
			Entity:         channelEntity,
			SearchOp:       "slack.channel.list",
			GetOp:          "slack.channel.info",
			QueryParam:     "-",
			LimitParam:     "limit",
			IDParam:        "channel",
			CursorParam:    "cursor",
			NextCursorPath: "response_metadata.next_cursor",
			LocalFilter:    true,
			ParamDefaults: map[string]any{
				"types":            "public_channel,private_channel",
				"exclude_archived": "true",
			},
			IDFields:    []string{"id"},
			TitleFields: []string{"name", "name_normalized"},
			TextFields:  []string{"topic.value", "purpose.value"},
			Corpus: connectorplugin.CorpusPolicy{
				TitleFields: []string{"name", "name_normalized"},
				BodyFields:  []string{"topic.value", "purpose.value"},
			},
			MetadataFields: map[string][]string{
				"channel_id":  {"id"},
				"topic":       {"topic.value"},
				"purpose":     {"purpose.value"},
				"is_archived": {"is_archived"},
				"is_member":   {"is_member"},
			},
			Relations: []connectorplugin.DatasourceRelationAction{{
				Name:           "members",
				Description:    "Exact Slack channel members.",
				TargetEntity:   UserEntity,
				Operation:      "slack.channel.members",
				IDParam:        "channel",
				LimitParam:     "limit",
				CursorParam:    "cursor",
				ResultPath:     "members",
				NextCursorPath: "response_metadata.next_cursor",
				Exact:          true,
			}},
		},
		{
			Kind:       Name,
			Entity:     messageEntity,
			SearchOp:   "slack.message.search",
			QueryParam: "query",
			LimitParam: "count",
			ResultPath: "messages.matches",
			ParamDefaults: map[string]any{
				"sort":     "timestamp",
				"sort_dir": "desc",
			},
			IDFields:    []string{"iid", "ts"},
			TitleFields: []string{"channel.name", "username", "user"},
			TextFields:  []string{"text"},
			URLFields:   []string{"permalink"},
			Corpus: connectorplugin.CorpusPolicy{
				TitleFields: []string{"channel.name", "username", "user"},
				BodyFields:  []string{"text"},
				MetadataFields: map[string][]string{
					"channel_id": {"channel.id", "channel_id"},
					"user":       {"user", "username"},
					"timestamp":  {"ts"},
				},
			},
			MetadataFields: map[string][]string{
				"channel_id": {"channel.id", "channel_id"},
				"channel":    {"channel.name", "channel"},
				"user":       {"user", "username"},
				"permalink":  {"permalink"},
				"timestamp":  {"ts"},
			},
			RecordTransform: normalizeSlackMessageRecord,
		},
	})}, nil
}

const UserEntity coredatasource.EntityType = "slack.user"
const ChannelEntity coredatasource.EntityType = "slack.channel"
const MessageEntity coredatasource.EntityType = "slack.message"

type User struct {
	ID       string `json:"id" datasource:"id,filterable" jsonschema:"description=Slack user id."`
	Name     string `json:"name" datasource:"searchable,filterable" jsonschema:"description=Slack username."`
	RealName string `json:"real_name,omitempty" datasource:"searchable" jsonschema:"description=Slack real name."`
	TeamID   string `json:"team_id,omitempty" datasource:"filterable" jsonschema:"description=Slack team id."`
	Deleted  bool   `json:"deleted,omitempty" datasource:"filterable" jsonschema:"description=Whether the user is deleted."`
	IsBot    bool   `json:"is_bot,omitempty" datasource:"filterable" jsonschema:"description=Whether the user is a bot."`
}

type Channel struct {
	ID             string `json:"id" datasource:"id,filterable" jsonschema:"description=Slack channel id."`
	Name           string `json:"name" datasource:"searchable,filterable" jsonschema:"description=Slack channel name."`
	NameNormalized string `json:"name_normalized,omitempty" datasource:"searchable" jsonschema:"description=Normalized channel name."`
	IsChannel      bool   `json:"is_channel,omitempty" datasource:"filterable" jsonschema:"description=Whether this conversation is a public channel."`
	IsGroup        bool   `json:"is_group,omitempty" datasource:"filterable" jsonschema:"description=Whether this conversation is a private channel."`
	IsIM           bool   `json:"is_im,omitempty" datasource:"filterable" jsonschema:"description=Whether this conversation is a direct message."`
	IsMPIM         bool   `json:"is_mpim,omitempty" datasource:"filterable" jsonschema:"description=Whether this conversation is a group direct message."`
	IsArchived     bool   `json:"is_archived,omitempty" datasource:"filterable" jsonschema:"description=Whether the channel is archived."`
	IsMember       bool   `json:"is_member,omitempty" datasource:"filterable" jsonschema:"description=Whether the bot is a member."`
	Creator        string `json:"creator,omitempty" datasource:"filterable" jsonschema:"description=Creator user id."`
	NumMembers     int    `json:"num_members,omitempty" datasource:"filterable" jsonschema:"description=Approximate channel member count."`
	TopicValue     string `json:"topic_value,omitempty" datasource:"searchable" jsonschema:"description=Channel topic text."`
	PurposeValue   string `json:"purpose_value,omitempty" datasource:"searchable" jsonschema:"description=Channel purpose text."`
}

type Message struct {
	ID        string `json:"iid,omitempty" datasource:"id" jsonschema:"description=Slack search result id."`
	Timestamp string `json:"ts,omitempty" datasource:"filterable" jsonschema:"description=Slack message timestamp."`
	ChannelID string `json:"channel_id,omitempty" datasource:"filterable" jsonschema:"description=Slack channel id."`
	Channel   string `json:"channel,omitempty" datasource:"searchable,filterable" jsonschema:"description=Slack channel name."`
	User      string `json:"user,omitempty" datasource:"filterable" jsonschema:"description=Slack user id."`
	Username  string `json:"username,omitempty" datasource:"searchable" jsonschema:"description=Slack username."`
	Text      string `json:"text,omitempty" datasource:"searchable" jsonschema:"description=Message text."`
	Permalink string `json:"permalink,omitempty" datasource:"url" jsonschema:"description=Slack permalink."`
}

func normalizeSlackMessageRecord(record coredatasource.Record) coredatasource.Record {
	if record.Metadata == nil {
		record.Metadata = map[string]string{}
	}
	if record.URL != "" && record.Metadata["permalink"] == "" {
		record.Metadata["permalink"] = record.URL
	}
	if record.Metadata["channel_id"] == "" {
		record.Metadata["channel_id"] = slackChannelIDFromPermalink(firstNonEmpty(record.Metadata["permalink"], record.URL))
	}
	if record.Metadata["channel"] == "" && record.Title != "" {
		record.Metadata["channel"] = strings.TrimPrefix(record.Title, "#")
	}
	if record.Title == "" && record.Metadata["channel"] != "" {
		record.Title = record.Metadata["channel"]
	}
	if len(record.Metadata) == 0 {
		record.Metadata = nil
	}
	return record
}

func slackChannelIDFromPermalink(permalink string) string {
	const marker = "/archives/"
	index := strings.Index(permalink, marker)
	if index < 0 {
		return ""
	}
	rest := permalink[index+len(marker):]
	if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
		rest = rest[:cut]
	}
	return strings.TrimSpace(rest)
}

func channelSendSpec() operation.Spec {
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

func slackConnectorActions() []connectorplugin.Action {
	return []connectorplugin.Action{{
		Kind:        Name,
		Operation:   "slack.message.search",
		Suffix:      "search",
		Description: "Search Slack messages across the workspace.",
		Spec: func(name string) operation.Spec {
			return operationruntime.WithTypedContract[connectorSearchInput, connectorplugin.Output](operation.Spec{
				Ref:         operation.Ref{Name: operation.Name(name)},
				Description: "Search Slack messages across the workspace.",
				Semantics: operation.Semantics{
					Determinism: operation.DeterminismNonDeterministic,
					Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
					Idempotency: operation.IdempotencyIdempotent,
					Risk:        operation.RiskLow,
				},
			})
		},
	}}
}

type connectorSearchInput struct {
	Query   string `json:"query" jsonschema:"description=Slack search query.,required"`
	Count   int    `json:"count,omitempty" jsonschema:"description=Maximum matches to return. Defaults to 20."`
	Page    int    `json:"page,omitempty" jsonschema:"description=Result page number. Defaults to 1."`
	Sort    string `json:"sort,omitempty" jsonschema:"description=Slack search sort. Usually score or timestamp."`
	SortDir string `json:"sort_dir,omitempty" jsonschema:"description=Slack search sort direction. Usually desc or asc."`
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

type Target struct {
	ChannelName string
	ChannelID   string
	ThreadTS    string
	UserID      string
	TeamID      string
}

type targetKey struct{}

func ContextWithTarget(ctx context.Context, target Target) context.Context {
	return context.WithValue(ctx, targetKey{}, target)
}

func TargetFromContext(ctx context.Context) (Target, bool) {
	target, ok := ctx.Value(targetKey{}).(Target)
	return target, ok
}

type Dispatcher struct {
	mu        sync.RWMutex
	posters   map[string]poster
	searchers map[string]searcher
}

type poster interface {
	PostMessageContext(context.Context, string, ...slack.MsgOption) (string, string, error)
}

type searcher interface {
	SearchMessagesContext(context.Context, string, slack.SearchParameters) (*slack.SearchMessages, error)
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{posters: map[string]poster{}, searchers: map[string]searcher{}}
}

func (d *Dispatcher) Register(name string, p poster) {
	if d == nil || p == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.posters == nil {
		d.posters = map[string]poster{}
	}
	d.posters[name] = p
}

func (d *Dispatcher) RegisterSearcher(name string, s searcher) {
	if d == nil || s == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.searchers == nil {
		d.searchers = map[string]searcher{}
	}
	d.searchers[name] = s
}

func (d *Dispatcher) Post(ctx context.Context, target Target, text string) (string, error) {
	if d == nil {
		return "", fmt.Errorf("slackplugin: dispatcher is nil")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	selected := d.posters[target.ChannelName]
	if selected == nil {
		for _, poster := range d.posters {
			selected = poster
			break
		}
	}
	if selected == nil {
		return "", fmt.Errorf("slackplugin: no Slack poster registered")
	}
	_, ts, err := selected.PostMessageContext(ctx, target.ChannelID, slack.MsgOptionText(text, false), slack.MsgOptionTS(target.ThreadTS))
	return ts, err
}

func (d *Dispatcher) SearchMessages(ctx context.Context, channelName, query string, limit int) (*slack.SearchMessages, error) {
	if d == nil {
		return nil, fmt.Errorf("slackplugin: dispatcher is nil")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	selected := d.searchers[channelName]
	if selected == nil {
		for _, searcher := range d.searchers {
			selected = searcher
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("slackplugin: no Slack search client registered")
	}
	params := slack.NewSearchParameters()
	params.Count = limit
	params.Highlight = false
	return selected.SearchMessagesContext(ctx, query, params)
}

type ChannelConfig struct {
	Name       string
	Session    coresession.Ref
	BotToken   string
	UserToken  string
	AppToken   string
	BotUserID  string
	TeamID     string
	Debug      bool
	Access     AccessPolicy
	Dispatcher *Dispatcher
	API        *slack.Client
	SearchAPI  *slack.Client
	SocketMode *socketmode.Client
}

type SlackChannel struct {
	name       string
	session    coresession.Ref
	botUserID  string
	teamID     string
	access     AccessPolicy
	debug      bool
	api        *slack.Client
	socket     *socketmode.Client
	dispatcher *Dispatcher
}

var _ channelruntime.Channel = (*SlackChannel)(nil)

func NewChannel(cfg ChannelConfig) (*SlackChannel, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("slackplugin: channel name is empty")
	}
	if cfg.Session.Name == "" {
		return nil, fmt.Errorf("slackplugin: channel %q session is empty", cfg.Name)
	}
	api := cfg.API
	if api == nil {
		if cfg.BotToken == "" {
			return nil, fmt.Errorf("slackplugin: channel %q bot token is empty", cfg.Name)
		}
		api = slack.New(cfg.BotToken, slack.OptionAppLevelToken(cfg.AppToken))
	}
	socketClient := cfg.SocketMode
	if socketClient == nil {
		if cfg.AppToken == "" {
			return nil, fmt.Errorf("slackplugin: channel %q app token is empty", cfg.Name)
		}
		socketClient = socketmode.New(api)
	}
	dispatcher := cfg.Dispatcher
	if dispatcher == nil {
		dispatcher = NewDispatcher()
	}
	dispatcher.Register(cfg.Name, api)
	searchAPI := cfg.SearchAPI
	if searchAPI == nil {
		if cfg.UserToken != "" {
			searchAPI = slack.New(cfg.UserToken)
		} else {
			searchAPI = api
		}
	}
	dispatcher.RegisterSearcher(cfg.Name, searchAPI)
	return &SlackChannel{
		name:       cfg.Name,
		session:    cfg.Session,
		botUserID:  cfg.BotUserID,
		teamID:     cfg.TeamID,
		access:     cfg.Access,
		debug:      cfg.Debug,
		api:        api,
		socket:     socketClient,
		dispatcher: dispatcher,
	}, nil
}

func (c *SlackChannel) Name() string { return c.name }

func (c *SlackChannel) Start(ctx context.Context, client clientapi.ChannelClient) error {
	if c == nil || c.socket == nil {
		return fmt.Errorf("slackplugin: channel is nil")
	}
	if err := c.verifyBotAuth(ctx); err != nil {
		return err
	}
	runErr := make(chan error, 1)
	go func() {
		if err := c.socket.RunContext(ctx); err != nil && ctx.Err() == nil {
			runErr <- err
		}
		close(runErr)
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-runErr:
			if !ok {
				return nil
			}
			if err != nil {
				return fmt.Errorf("slackplugin: socket mode: %w", err)
			}
		case evt, ok := <-c.socket.Events:
			if !ok {
				return nil
			}
			c.handleSocketEvent(ctx, client, evt)
		}
	}
}

func (c *SlackChannel) handleSocketEvent(ctx context.Context, client clientapi.ChannelClient, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		slog.Info("slack channel connecting", "channel", c.name)
		return
	case socketmode.EventTypeConnected:
		slog.Info("slack channel connected", "channel", c.name, "bot_user_id", c.botUserID, "team_id", c.teamID, "required_bot_events", strings.Join(requiredSlackBotEvents, ","))
		return
	case socketmode.EventTypeHello:
		if evt.Request != nil {
			slog.Info("slack channel hello", "channel", c.name, "connections", evt.Request.NumConnections, "host", evt.Request.DebugInfo.Host, "approx_connection_time", evt.Request.DebugInfo.ApproximateConnectionTime)
		}
		return
	case socketmode.EventTypeDisconnect:
		if evt.Request != nil {
			slog.Warn("slack channel disconnect requested", "channel", c.name, "reason", evt.Request.Reason, "host", evt.Request.DebugInfo.Host, "approx_connection_time", evt.Request.DebugInfo.ApproximateConnectionTime)
		}
		return
	case socketmode.EventTypeConnectionError:
		slog.Warn("slack channel connection error", "channel", c.name, "error", fmt.Sprint(evt.Data))
		return
	case socketmode.EventTypeEventsAPI:
		if evt.Request != nil {
			_ = c.socket.Ack(*evt.Request)
		}
		apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		go c.handleEventsAPI(ctx, client, apiEvent)
	default:
		if evt.Request != nil {
			_ = c.socket.Ack(*evt.Request)
		}
	}
}

func (c *SlackChannel) verifyBotAuth(ctx context.Context) error {
	if c == nil || c.api == nil {
		return fmt.Errorf("slackplugin: channel is nil")
	}
	resp, err := c.api.AuthTestContext(ctx)
	if err != nil {
		return fmt.Errorf("slackplugin: bot auth test: %w", err)
	}
	if c.botUserID == "" {
		c.botUserID = resp.UserID
	}
	if c.teamID == "" {
		c.teamID = resp.TeamID
	}
	slog.Info("slack channel bot authenticated", "channel", c.name, "team", resp.Team, "team_id", resp.TeamID, "bot_user_id", resp.UserID, "bot_id", resp.BotID)
	return nil
}

func (c *SlackChannel) handleEventsAPI(ctx context.Context, client clientapi.ChannelClient, evt slackevents.EventsAPIEvent) {
	slog.Info("slack channel event received", "channel", c.name, "event", evt.InnerEvent.Type)
	switch data := evt.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		inbound := c.inboundFromMention(evt.TeamID, data)
		if err := c.handleInbound(ctx, client, inbound); err != nil {
			slog.Warn("slack channel mention failed", "channel", c.name, "slack_channel", inbound.ChannelID, "thread_ts", inbound.ThreadTS, "user", inbound.UserID, "error", err)
		}
	case *slackevents.MessageEvent:
		inbound, ok := c.inboundFromMessage(evt.TeamID, data)
		if !ok {
			return
		}
		if err := c.handleInbound(ctx, client, inbound); err != nil {
			slog.Warn("slack channel message failed", "channel", c.name, "kind", inbound.Kind, "slack_channel", inbound.ChannelID, "thread_ts", inbound.ThreadTS, "user", inbound.UserID, "error", err)
		}
	default:
		if evt.InnerEvent.Type == "app_home_opened" {
			slog.Warn("slack channel received app_home_opened but no message event", "channel", c.name, "hint", "enable the App Home Messages Tab and subscribe bot events app_mention,message.im,message.channels,message.groups,message.mpim; reinstall the Slack app after changing events")
			return
		}
		slog.Debug("slack channel event ignored", "channel", c.name, "event", evt.InnerEvent.Type)
	}
}

type inboundMessage struct {
	Text       string
	UserID     string
	ChannelID  string
	TS         string
	ThreadTS   string
	TeamID     string
	Kind       string
	IsDirect   bool
	ReceivedAt time.Time
}

func (c *SlackChannel) inboundFromMention(teamID string, event *slackevents.AppMentionEvent) inboundMessage {
	threadTS := firstNonEmpty(event.ThreadTimeStamp, event.TimeStamp)
	return inboundMessage{
		Text:       stripBotMention(event.Text, c.botUserID),
		UserID:     event.User,
		ChannelID:  event.Channel,
		TS:         event.TimeStamp,
		ThreadTS:   threadTS,
		TeamID:     firstNonEmpty(teamID, c.teamID),
		Kind:       "mention",
		ReceivedAt: time.Now().UTC(),
	}
}

func (c *SlackChannel) inboundFromMessage(teamID string, event *slackevents.MessageEvent) (inboundMessage, bool) {
	if event.BotID != "" || event.User == "" || event.User == c.botUserID {
		slog.Debug("slack channel message ignored", "channel", c.name, "reason", "bot_or_empty_user", "slack_channel", event.Channel, "user", event.User)
		return inboundMessage{}, false
	}
	if event.SubType != "" {
		slog.Debug("slack channel message ignored", "channel", c.name, "reason", "subtype", "subtype", event.SubType, "slack_channel", event.Channel)
		return inboundMessage{}, false
	}
	kind := "thread_reply"
	if event.ChannelType == "im" && event.ThreadTimeStamp == "" {
		kind = "dm"
	} else if event.ThreadTimeStamp == "" && strings.Contains(event.Text, "<@"+c.botUserID+">") {
		kind = "mention"
	}
	if kind == "thread_reply" && event.ChannelType != "im" {
		slog.Debug("slack channel message ignored", "channel", c.name, "reason", "non_dm_thread_reply", "slack_channel", event.Channel, "thread_ts", event.ThreadTimeStamp)
		return inboundMessage{}, false
	}
	if event.ChannelType != "im" && event.ThreadTimeStamp == "" && kind != "mention" {
		slog.Debug("slack channel message ignored", "channel", c.name, "reason", "top_level_without_mention", "slack_channel", event.Channel, "channel_type", event.ChannelType)
		return inboundMessage{}, false
	}
	threadTS := firstNonEmpty(event.ThreadTimeStamp, event.TimeStamp)
	return inboundMessage{
		Text:       stripBotMention(event.Text, c.botUserID),
		UserID:     event.User,
		ChannelID:  event.Channel,
		TS:         event.TimeStamp,
		ThreadTS:   threadTS,
		TeamID:     firstNonEmpty(teamID, c.teamID),
		Kind:       kind,
		IsDirect:   event.ChannelType == "im",
		ReceivedAt: time.Now().UTC(),
	}, true
}

func (c *SlackChannel) handleInbound(ctx context.Context, client clientapi.ChannelClient, msg inboundMessage) error {
	if client == nil {
		return fmt.Errorf("slackplugin: channel client is nil")
	}
	if err := c.access.Check(msg); err != nil {
		return err
	}
	started := time.Now()
	slog.Info("slack channel handling message", "channel", c.name, "kind", msg.Kind, "slack_channel", msg.ChannelID, "thread_ts", msg.ThreadTS, "user", msg.UserID)
	conversationID := msg.ChannelID + ":" + msg.ThreadTS
	session, err := client.Open(ctx, clientapi.OpenRequest{
		Session:      c.session,
		Conversation: channel.ConversationRef{ID: conversationID},
		Metadata: map[string]string{
			"slack_channel_id": msg.ChannelID,
			"slack_thread_ts":  msg.ThreadTS,
			"slack_team_id":    msg.TeamID,
		},
	})
	if err != nil {
		return err
	}
	turnCtx := ContextWithTarget(ctx, Target{
		ChannelName: c.name,
		ChannelID:   msg.ChannelID,
		ThreadTS:    msg.ThreadTS,
		UserID:      msg.UserID,
		TeamID:      msg.TeamID,
	})
	trust := c.access.TrustFor(msg)
	run, err := session.Submit(turnCtx, clientapi.NewSubmission().
		WithInput(clientapi.Input{
			Content: slackInputContent(msg, c.access.AudienceTrustFor(msg), c.access.Sharing),
		}).
		WithCaller(slackCaller(c.name, msg)).
		WithTrust(slackPolicyTrust(trust)))
	if err != nil {
		return err
	}
	observer := newRunObserver(c, Target{ChannelName: c.name, ChannelID: msg.ChannelID, ThreadTS: msg.ThreadTS, UserID: msg.UserID, TeamID: msg.TeamID})
	observer.setStatus(turnCtx, slackWorkingStatus)
	eventsDone := observer.Observe(run.Events())
	result, err := run.Wait(turnCtx)
	summary := <-eventsDone
	observerFinished := false
	finishObserver := func(finalMarkdown string) {
		if observerFinished {
			return
		}
		observer.Finish(turnCtx, finalMarkdown)
		observerFinished = true
	}
	if err != nil {
		finishObserver("")
		_ = c.postError(turnCtx, Target{ChannelName: c.name, ChannelID: msg.ChannelID, ThreadTS: msg.ThreadTS, UserID: msg.UserID, TeamID: msg.TeamID}, err)
		return err
	}
	if inputErr := slackResultError(result); inputErr != nil {
		finishObserver("")
		_ = c.postError(turnCtx, Target{ChannelName: c.name, ChannelID: msg.ChannelID, ThreadTS: msg.ThreadTS, UserID: msg.UserID, TeamID: msg.TeamID}, inputErr)
		return inputErr
	}
	if len(summary.ActiveTasks) > 0 {
		summary = observer.FollowTasks(turnCtx, session, summary.ActiveTasks)
	}
	if result.Outbound != nil && result.Outbound.Message != nil {
		text := fmt.Sprint(result.Outbound.Message.Content)
		if strings.TrimSpace(text) != "" {
			if summary.Streamed {
				finishObserver(text)
				slog.Info("slack channel reply streamed", "channel", c.name, "kind", msg.Kind, "slack_channel", msg.ChannelID, "thread_ts", msg.ThreadTS, "duration", time.Since(started))
			} else {
				finishObserver("")
				_, err = c.dispatcher.Post(turnCtx, Target{ChannelName: c.name, ChannelID: msg.ChannelID, ThreadTS: msg.ThreadTS, UserID: msg.UserID, TeamID: msg.TeamID}, text)
			}
		}
	}
	if err != nil {
		return err
	}
	if result.Outbound == nil || result.Outbound.Message == nil || strings.TrimSpace(fmt.Sprint(result.Outbound.Message.Content)) == "" {
		finishObserver("")
		if summary.Streamed {
			slog.Info("slack channel run completed with streamed content", "channel", c.name, "kind", msg.Kind, "slack_channel", msg.ChannelID, "thread_ts", msg.ThreadTS, "duration", time.Since(started))
			return nil
		}
		slog.Warn("slack channel run completed without outbound message", "channel", c.name, "kind", msg.Kind, "slack_channel", msg.ChannelID, "thread_ts", msg.ThreadTS, "duration", time.Since(started), "events", summary.Events, "model_events", summary.ModelEvents, "operation_events", summary.OperationEvents)
		return nil
	}
	if !summary.Streamed {
		finishObserver("")
	}
	slog.Info("slack channel reply posted", "channel", c.name, "kind", msg.Kind, "slack_channel", msg.ChannelID, "thread_ts", msg.ThreadTS, "duration", time.Since(started))
	return err
}

func slackCaller(channelName string, msg inboundMessage) policy.Caller {
	return policy.Caller{
		Kind: policy.CallerUser,
		Principal: policy.Principal{
			Kind: "slack_user",
			ID:   msg.UserID,
		},
		Source: "slack:" + channelName,
	}
}

func slackPolicyTrust(trust user.TrustLevel) policy.Trust {
	out := policy.Trust{
		Kind:       policy.TrustInvocation,
		VerifiedBy: "slack",
		Reason:     "slack_access_policy",
	}
	switch user.NormalizeTrust(trust) {
	case user.TrustOperator:
		out.Level = policy.TrustPrivileged
	case user.TrustInternal:
		out.Level = policy.TrustVerified
	default:
		out.Level = policy.TrustUntrusted
	}
	return out
}

type slackInputPayload struct {
	Text         string              `json:"text"`
	SlackContext slackContextPayload `json:"slack_context"`
}

type slackContextPayload struct {
	ChannelID       string          `json:"channel_id,omitempty"`
	ThreadTS        string          `json:"thread_ts,omitempty"`
	UserID          string          `json:"user_id,omitempty"`
	TeamID          string          `json:"team_id,omitempty"`
	InteractionKind string          `json:"interaction_kind,omitempty"`
	IsDirect        bool            `json:"is_direct,omitempty"`
	AudienceTrust   user.TrustLevel `json:"audience_trust,omitempty"`
	Sharing         string          `json:"sharing,omitempty"`
}

func slackInputContent(msg inboundMessage, audienceTrust user.TrustLevel, sharing string) slackInputPayload {
	payload := slackInputPayload{
		Text: msg.Text,
		SlackContext: slackContextPayload{
			ChannelID:       msg.ChannelID,
			ThreadTS:        msg.ThreadTS,
			UserID:          msg.UserID,
			TeamID:          msg.TeamID,
			InteractionKind: msg.Kind,
			IsDirect:        msg.IsDirect,
			Sharing:         firstNonEmpty(sharing, "strict"),
		},
	}
	if !isDirectSlackMessage(msg) {
		payload.SlackContext.AudienceTrust = user.NormalizeTrust(audienceTrust)
	}
	return payload
}

type AccessPolicy struct {
	Mode             string
	AllowUsers       []string
	DenyUsers        []string
	AllowChannels    []string
	DenyChannels     []string
	AllowKinds       []string
	DefaultTrust     user.TrustLevel
	Operators        []string
	InternalUsers    []string
	InternalChannels []string
	Sharing          string
}

func (p AccessPolicy) Check(msg inboundMessage) error {
	if contains(p.DenyUsers, msg.UserID) {
		return fmt.Errorf("slackplugin: user %q denied", msg.UserID)
	}
	if contains(p.DenyChannels, msg.ChannelID) {
		return fmt.Errorf("slackplugin: channel %q denied", msg.ChannelID)
	}
	if len(p.AllowKinds) > 0 && !contains(p.AllowKinds, msg.Kind) {
		return fmt.Errorf("slackplugin: interaction kind %q denied", msg.Kind)
	}
	if len(p.AllowChannels) > 0 && !contains(p.AllowChannels, msg.ChannelID) {
		return fmt.Errorf("slackplugin: channel %q not allowed", msg.ChannelID)
	}
	if p.Mode == "allow_list" && !contains(p.AllowUsers, msg.UserID) {
		return fmt.Errorf("slackplugin: user %q not allowed", msg.UserID)
	}
	return nil
}

func (p AccessPolicy) TrustFor(msg inboundMessage) user.TrustLevel {
	switch {
	case contains(p.Operators, msg.UserID):
		return user.TrustOperator
	case contains(p.InternalUsers, msg.UserID), contains(p.InternalChannels, msg.ChannelID):
		return user.TrustInternal
	default:
		return user.NormalizeTrust(p.DefaultTrust)
	}
}

func (p AccessPolicy) AudienceTrustFor(msg inboundMessage) user.TrustLevel {
	if isDirectSlackMessage(msg) {
		return p.TrustFor(msg)
	}
	switch {
	case contains(p.InternalChannels, msg.ChannelID):
		return user.TrustInternal
	default:
		return user.NormalizeTrust(p.DefaultTrust)
	}
}

func isDirectSlackMessage(msg inboundMessage) bool {
	return msg.IsDirect || msg.Kind == "dm"
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}

func stripBotMention(text, botUserID string) string {
	if botUserID == "" {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(strings.ReplaceAll(text, "<@"+botUserID+">", ""))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func InvocationTarget() invocation.Target {
	return invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: ChannelSendOp}}
}
