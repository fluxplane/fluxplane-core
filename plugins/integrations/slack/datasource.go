package slack

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	runtimedatasource "github.com/fluxplane/fluxplane-core/runtime/datasource"
	"github.com/slack-go/slack"
)

const UserEntity coredatasource.EntityType = "slack.user"
const ChannelEntity coredatasource.EntityType = "slack.channel"
const MessageEntity coredatasource.EntityType = "slack.message"
const ThreadMessageEntity coredatasource.EntityType = "slack.thread_message"

var slackConversationTypes = []string{"public_channel", "private_channel", "im", "mpim"}

const defaultSearchHistoryWindow = 90 * 24 * time.Hour

var errSlackNativeSearchUnavailable = errors.New("slack native search requires a user token")

type User struct {
	ID          string `json:"id" datasource:"id,filterable" jsonschema:"description=Slack user id."`
	Name        string `json:"name" datasource:"searchable,filterable" jsonschema:"description=Slack username."`
	RealName    string `json:"real_name,omitempty" datasource:"searchable" jsonschema:"description=Slack real name."`
	DisplayName string `json:"display_name,omitempty" datasource:"searchable" jsonschema:"description=Slack display name."`
	Email       string `json:"email,omitempty" datasource:"searchable,filterable" jsonschema:"description=Slack profile email."`
	TeamID      string `json:"team_id,omitempty" datasource:"filterable" jsonschema:"description=Slack team id."`
	Deleted     bool   `json:"deleted,omitempty" datasource:"filterable" jsonschema:"description=Whether the user is deleted."`
	IsBot       bool   `json:"is_bot,omitempty" datasource:"filterable" jsonschema:"description=Whether the user is a bot."`
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
	ID        string `json:"id,omitempty" datasource:"id" jsonschema:"description=Slack message id as channel:timestamp."`
	Timestamp string `json:"ts,omitempty" datasource:"filterable" jsonschema:"description=Slack message timestamp."`
	ChannelID string `json:"channel_id,omitempty" datasource:"filterable" jsonschema:"description=Slack channel id."`
	Channel   string `json:"channel,omitempty" datasource:"searchable,filterable" jsonschema:"description=Slack channel name."`
	User      string `json:"user,omitempty" datasource:"filterable" jsonschema:"description=Slack user id."`
	Username  string `json:"username,omitempty" datasource:"searchable" jsonschema:"description=Slack username."`
	Text      string `json:"text,omitempty" datasource:"searchable" jsonschema:"description=Message text."`
	Permalink string `json:"permalink,omitempty" datasource:"url" jsonschema:"description=Slack permalink."`
}

type ThreadMessage struct {
	ID              string `json:"id,omitempty" datasource:"id" jsonschema:"description=Slack thread message id as channel:thread_ts:timestamp."`
	Timestamp       string `json:"ts,omitempty" datasource:"filterable" jsonschema:"description=Slack message timestamp."`
	ThreadTimestamp string `json:"thread_ts,omitempty" datasource:"filterable" jsonschema:"description=Slack thread root timestamp."`
	ChannelID       string `json:"channel_id,omitempty" datasource:"filterable" jsonschema:"description=Slack channel id."`
	User            string `json:"user,omitempty" datasource:"filterable" jsonschema:"description=Slack user id."`
	Text            string `json:"text,omitempty" datasource:"searchable" jsonschema:"description=Message text."`
	Permalink       string `json:"permalink,omitempty" datasource:"url" jsonschema:"description=Slack permalink."`
}

type slackAPI interface {
	GetUserInfoContext(context.Context, string) (*slack.User, error)
	GetUsersInfoContext(context.Context, ...string) (*[]slack.User, error)
	GetUsersPaginated(...slack.GetUsersOption) slack.UserPagination
	GetConversationsContext(context.Context, *slack.GetConversationsParameters) ([]slack.Channel, string, error)
	GetConversationInfoContext(context.Context, *slack.GetConversationInfoInput) (*slack.Channel, error)
	SearchMessagesContext(context.Context, string, slack.SearchParameters) (*slack.SearchMessages, error)
	PostMessageContext(context.Context, string, ...slack.MsgOption) (string, string, error)
	AuthTestContext(context.Context) (*slack.AuthTestResponse, error)
	GetUsersInConversationContext(context.Context, *slack.GetUsersInConversationParameters) ([]string, string, error)
	GetConversationRepliesContext(context.Context, *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error)
	GetConversationHistoryContext(context.Context, *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
}

type slackDatasourceProvider struct {
	plugin Plugin
}

func (p slackDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return entitySpecs()
}

func (p slackDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if strings.TrimSpace(spec.Kind) != Name {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	var selected []coredatasource.EntitySpec
	if len(spec.Entities) == 0 {
		selected = entitySpecs()
	} else {
		var err error
		selected, err = runtimedatasource.SelectEntities(Name, entitySpecs(), spec.Entities)
		if err != nil {
			return nil, err
		}
	}
	instance := strings.TrimSpace(spec.Config["instance"])
	if instance != "" && instance != p.plugin.ref.InstanceName() {
		return nil, fmt.Errorf("slack datasource instance %q does not match plugin instance %q", instance, p.plugin.ref.InstanceName())
	}
	return slackAccessor{spec: spec, plugin: p.plugin, entities: selected, search: p.plugin.cfg.Search}, nil
}

type slackAccessor struct {
	spec     coredatasource.Spec
	plugin   Plugin
	entities []coredatasource.EntitySpec
	search   SearchConfig
}

func (a slackAccessor) Spec() coredatasource.Spec { return a.spec }

func (a slackAccessor) Entities() []coredatasource.EntitySpec {
	return append([]coredatasource.EntitySpec(nil), a.entities...)
}

func (a slackAccessor) ProviderFirstSearch(entity coredatasource.EntityType) bool {
	return entity == MessageEntity
}

func (a slackAccessor) ProviderSearchFallback(entity coredatasource.EntityType, err error) bool {
	return entity == MessageEntity && isSlackProviderSearchUnavailable(err)
}

func (a slackAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = MessageEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	api, err := a.plugin.api(ctx)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	switch entity {
	case UserEntity:
		users, _, err := listUsersPage(ctx, api, "", max(limit, 200))
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		records := runtimedatasource.NonEmptyRecordsFrom(filterUsers(users, req.Query), a.userRecord)
		if len(records) > limit {
			records = records[:limit]
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, records, len(records)), nil
	case ChannelEntity:
		channels, err := listChannels(ctx, api, max(limit, 200))
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		records := runtimedatasource.NonEmptyRecordsFrom(filterChannels(channels, req.Query), a.channelRecord)
		if len(records) > limit {
			records = records[:limit]
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, records, len(records)), nil
	case MessageEntity:
		api, ok, err := a.plugin.userAPI(ctx)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		if !ok {
			return coredatasource.SearchResult{}, errSlackNativeSearchUnavailable
		}
		params := slack.NewSearchParameters()
		params.Count = limit
		params.Sort = "timestamp"
		params.SortDirection = "desc"
		params.Highlight = false
		messages, err := api.SearchMessagesContext(ctx, req.Query, params)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.NonEmptyRecordsFrom(messages.Matches, a.searchMessageRecord), messages.Total), nil
	case ThreadMessageEntity:
		list, err := a.listThreadMessages(ctx, api, coredatasource.ListRequest{Entity: entity, Limit: limit, Filters: req.Filters})
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		records := filterRecords(list.Records, req.Query)
		return runtimedatasource.SearchResult(a.spec.Name, entity, records, len(records)), nil
	default:
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q entity %q does not support search", a.spec.Name, entity)
	}
}

func (a slackAccessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = ChannelEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	api, err := a.plugin.api(ctx)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	switch entity {
	case UserEntity:
		users, next, err := listUsersPage(ctx, api, req.Cursor, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResult(a.spec.Name, entity, runtimedatasource.NonEmptyRecordsFrom(users, a.userRecord), -1, next), nil
	case ChannelEntity:
		channels, next, err := listChannelsPage(ctx, api, req.Cursor, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResult(a.spec.Name, entity, runtimedatasource.NonEmptyRecordsFrom(channels, a.channelRecord), -1, next), nil
	case MessageEntity:
		return a.listChannelMessages(ctx, api, req)
	case ThreadMessageEntity:
		return a.listThreadMessages(ctx, api, req)
	default:
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q entity %q does not support list", a.spec.Name, entity)
	}
}

func (a slackAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if !runtimedatasource.HasEntity(a.entities, req.Entity) {
		return coredatasource.Record{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	api, err := a.plugin.api(ctx)
	if err != nil {
		return coredatasource.Record{}, err
	}
	switch req.Entity {
	case UserEntity:
		user, err := api.GetUserInfoContext(ctx, strings.TrimSpace(req.ID))
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.userRecord(*user), nil
	case ChannelEntity:
		channel, err := api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: strings.TrimSpace(req.ID), IncludeNumMembers: true})
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.channelRecord(*channel), nil
	case MessageEntity:
		channelID, ts, ok := parseMessageID(req.ID)
		if !ok {
			return coredatasource.Record{}, fmt.Errorf("slack message id must be channel:timestamp")
		}
		msg, err := getMessageByTimestamp(ctx, api, channelID, ts)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.messageRecord(channelID, slack.Message{Msg: msg}), nil
	case ThreadMessageEntity:
		channelID, threadTS, ts, ok := parseThreadMessageID(req.ID)
		if !ok {
			return coredatasource.Record{}, fmt.Errorf("slack thread message id must be channel:thread_ts:timestamp")
		}
		msg, err := getThreadMessageByTimestamp(ctx, api, channelID, threadTS, ts)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.threadMessageRecord(channelID, threadTS, msg), nil
	default:
		return coredatasource.Record{}, fmt.Errorf("datasource %q entity %q does not support get", a.spec.Name, req.Entity)
	}
}

func (a slackAccessor) BatchGet(ctx context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	out := coredatasource.BatchGetResult{Datasource: a.spec.Name, Entity: req.Entity}
	for _, id := range req.IDs {
		record, err := a.Get(ctx, coredatasource.GetRequest{Entity: req.Entity, ID: id})
		if err != nil {
			out.Errors = append(out.Errors, coredatasource.BatchGetError{ID: id, Message: err.Error()})
			continue
		}
		out.Records = append(out.Records, record)
	}
	return out, nil
}

func (a slackAccessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	switch req.Entity {
	case UserEntity, ChannelEntity:
		result, err := a.List(ctx, coredatasource.ListRequest{Entity: req.Entity, Cursor: req.Cursor, Limit: req.Limit})
		if err != nil {
			return coredatasource.CorpusPage{}, err
		}
		return coredatasource.CorpusPage{
			Documents:  runtimedatasource.RecordsToCorpusDocuments(result.Records),
			NextCursor: result.NextCursor,
			Complete:   result.Complete,
		}, nil
	case MessageEntity:
		return a.messageCorpus(ctx, req)
	default:
		return coredatasource.CorpusPage{}, fmt.Errorf("datasource %q entity %q is not materializable", a.spec.Name, req.Entity)
	}
}

type slackMessageCorpusCursor struct {
	Channel int    `json:"channel,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
}

func (a slackAccessor) messageCorpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	configured := cleaned(a.search.Channels)
	if len(configured) == 0 {
		return coredatasource.CorpusPage{Complete: true}, nil
	}
	api, err := a.plugin.botAPI(ctx)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	channels, err := a.resolveSearchChannels(ctx, api, configured)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	if len(channels) == 0 {
		return coredatasource.CorpusPage{Complete: true}, nil
	}
	cursor, err := decodeSlackMessageCorpusCursor(req.Cursor)
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	if cursor.Channel >= len(channels) {
		return coredatasource.CorpusPage{Complete: true}, nil
	}
	limit := normalizedLimit(req.Limit)
	oldest := slackTimestamp(time.Now().Add(-searchHistoryWindow(a.search.HistoryWindow)))
	workspaceURL := slackWorkspaceURL(ctx, api)
	for cursor.Channel < len(channels) {
		channel := channels[cursor.Channel]
		response, err := api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID: channel.ID,
			Cursor:    cursor.Cursor,
			Limit:     limit,
			Oldest:    oldest,
		})
		if isSlackInaccessibleConversation(err) {
			cursor.Channel++
			cursor.Cursor = ""
			continue
		}
		if err != nil {
			return coredatasource.CorpusPage{}, err
		}
		documents := make([]coredatasource.CorpusDocument, 0, len(response.Messages))
		for _, msg := range response.Messages {
			if strings.TrimSpace(msg.Timestamp) == "" || strings.TrimSpace(msg.Text) == "" {
				continue
			}
			record := a.messageRecord(channel.ID, msg)
			record = withSlackPermalink(record, workspaceURL, channel.ID, msg.Timestamp)
			record.Title = firstNonEmpty(channel.Name, channel.ID, record.Title)
			if record.Metadata == nil {
				record.Metadata = map[string]string{}
			}
			record.Metadata["channel"] = channel.Name
			documents = append(documents, runtimedatasource.RecordsToCorpusDocuments([]coredatasource.Record{record})...)
			if slackSearchIncludeThreads(a.search) && msg.ReplyCount > 0 {
				replies, err := a.threadReplyCorpusDocuments(ctx, api, channel, msg, limit, workspaceURL)
				if err != nil {
					return coredatasource.CorpusPage{}, err
				}
				documents = append(documents, replies...)
			}
		}
		next := slackMessageCorpusCursor{Channel: cursor.Channel, Cursor: response.ResponseMetaData.NextCursor}
		if next.Cursor == "" {
			next.Channel++
		}
		return coredatasource.CorpusPage{
			Documents:  documents,
			NextCursor: encodeSlackMessageCorpusCursor(next, len(channels)),
			Complete:   next.Channel >= len(channels) && next.Cursor == "",
		}, nil
	}
	return coredatasource.CorpusPage{Complete: true}, nil
}

func (a slackAccessor) threadReplyCorpusDocuments(ctx context.Context, api slackAPI, channel slack.Channel, root slack.Message, limit int, workspaceURL string) ([]coredatasource.CorpusDocument, error) {
	threadTS := firstNonEmpty(root.ThreadTimestamp, root.Timestamp)
	if threadTS == "" {
		return nil, nil
	}
	messages, _, _, err := api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: channel.ID, Timestamp: threadTS, Limit: limit, Inclusive: true})
	if isSlackInaccessibleConversation(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]coredatasource.Record, 0, len(messages))
	for _, msg := range messages {
		if msg.Timestamp == root.Timestamp || strings.TrimSpace(msg.Text) == "" {
			continue
		}
		record := a.messageRecord(channel.ID, msg)
		record = withSlackPermalink(record, workspaceURL, channel.ID, msg.Timestamp)
		record.Title = firstNonEmpty(channel.Name, channel.ID, record.Title)
		if record.Metadata == nil {
			record.Metadata = map[string]string{}
		}
		record.Metadata["channel"] = channel.Name
		record.Metadata["thread_ts"] = threadTS
		records = append(records, record)
	}
	return runtimedatasource.RecordsToCorpusDocuments(records), nil
}

func (a slackAccessor) resolveSearchChannels(ctx context.Context, api slackAPI, configured []string) ([]slack.Channel, error) {
	want := map[string]bool{}
	for _, channel := range configured {
		channel = strings.TrimPrefix(strings.TrimSpace(channel), "#")
		if channel != "" {
			want[strings.ToLower(channel)] = true
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	var out []slack.Channel
	cursor := ""
	for {
		channels, next, err := api.GetConversationsContext(ctx, &slack.GetConversationsParameters{
			Cursor:          cursor,
			Limit:           200,
			Types:           []string{"public_channel"},
			ExcludeArchived: true,
		})
		if err != nil {
			return nil, err
		}
		for _, channel := range channels {
			if want[strings.ToLower(channel.ID)] || want[strings.ToLower(channel.Name)] || want[strings.ToLower(channel.NameNormalized)] {
				out = append(out, channel)
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return out, nil
}

func (a slackAccessor) Relation(ctx context.Context, req coredatasource.RelationRequest) (coredatasource.RelationResult, error) {
	if !runtimedatasource.HasEntity(a.entities, req.Entity) {
		return coredatasource.RelationResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	api, err := a.plugin.api(ctx)
	if err != nil {
		return coredatasource.RelationResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	switch {
	case req.Entity == ChannelEntity && req.Relation == "members":
		members, next, err := api.GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{ChannelID: req.ID, Cursor: req.Cursor, Limit: limit})
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		records, err := a.memberRecords(ctx, api, members)
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		return runtimedatasource.RelationResult(a.spec.Name, req, UserEntity, records, -1, next, true), nil
	case req.Entity == MessageEntity && req.Relation == "thread_messages":
		channelID, ts, ok := parseMessageID(req.ID)
		if !ok {
			return coredatasource.RelationResult{}, fmt.Errorf("slack message id must be channel:timestamp")
		}
		messages, _, next, err := api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: channelID, Timestamp: ts, Cursor: req.Cursor, Limit: limit, Inclusive: true})
		if err != nil {
			return coredatasource.RelationResult{}, err
		}
		records := make([]coredatasource.Record, 0, len(messages))
		for _, msg := range messages {
			records = append(records, a.threadMessageRecord(channelID, ts, msg))
		}
		return runtimedatasource.RelationResult(a.spec.Name, req, ThreadMessageEntity, records, -1, next, true), nil
	default:
		return coredatasource.RelationResult{}, fmt.Errorf("datasource %q entity %q relation %q is unsupported", a.spec.Name, req.Entity, req.Relation)
	}
}

func (a slackAccessor) listChannelMessages(ctx context.Context, api slackAPI, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	channelID := strings.TrimSpace(req.Filters["channel_id"])
	if channelID == "" {
		return coredatasource.ListResult{}, fmt.Errorf("slack message list requires filter channel_id")
	}
	response, err := api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: channelID, Cursor: req.Cursor, Limit: normalizedLimit(req.Limit)})
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	records := make([]coredatasource.Record, 0, len(response.Messages))
	for _, msg := range response.Messages {
		records = append(records, a.messageRecord(channelID, msg))
	}
	return runtimedatasource.ListResult(a.spec.Name, MessageEntity, records, -1, response.ResponseMetaData.NextCursor), nil
}

func (a slackAccessor) listThreadMessages(ctx context.Context, api slackAPI, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	channelID := strings.TrimSpace(req.Filters["channel_id"])
	threadTS := strings.TrimSpace(req.Filters["thread_ts"])
	if channelID == "" || threadTS == "" {
		return coredatasource.ListResult{}, fmt.Errorf("slack thread message list requires filters channel_id and thread_ts")
	}
	messages, _, next, err := api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: channelID, Timestamp: threadTS, Cursor: req.Cursor, Limit: normalizedLimit(req.Limit), Inclusive: true})
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	records := make([]coredatasource.Record, 0, len(messages))
	for _, msg := range messages {
		records = append(records, a.threadMessageRecord(channelID, threadTS, msg))
	}
	return runtimedatasource.ListResult(a.spec.Name, ThreadMessageEntity, records, -1, next), nil
}

func (a slackAccessor) memberRecords(ctx context.Context, api slackAPI, members []string) ([]coredatasource.Record, error) {
	if len(members) == 0 {
		return nil, nil
	}
	users, err := api.GetUsersInfoContext(ctx, members...)
	if err != nil || users == nil {
		records := make([]coredatasource.Record, 0, len(members))
		for _, member := range members {
			records = append(records, coredatasource.Record{ID: member, Datasource: a.spec.Name, Entity: UserEntity, Title: member})
		}
		return records, nil
	}
	return runtimedatasource.NonEmptyRecordsFrom(*users, a.userRecord), nil
}

func (a slackAccessor) userRecord(user slack.User) coredatasource.Record {
	displayName := firstNonEmpty(user.Profile.DisplayName, user.Profile.RealName, user.RealName, user.Name, user.ID)
	return coredatasource.Record{
		ID:         user.ID,
		Datasource: a.spec.Name,
		Entity:     UserEntity,
		Title:      displayName,
		Content:    strings.Join(cleaned([]string{user.Name, user.Profile.Title}), " "),
		Metadata: map[string]string{
			"avatar_url":   user.Profile.ImageOriginal,
			"id":           user.ID,
			"name":         user.Name,
			"real_name":    user.RealName,
			"team_id":      user.TeamID,
			"is_bot":       boolString(user.IsBot),
			"deleted":      boolString(user.Deleted),
			"email":        user.Profile.Email,
			"display_name": user.Profile.DisplayName,
		},
		Raw: User{
			ID:          user.ID,
			Name:        user.Name,
			RealName:    user.RealName,
			DisplayName: user.Profile.DisplayName,
			Email:       user.Profile.Email,
			TeamID:      user.TeamID,
			Deleted:     user.Deleted,
			IsBot:       user.IsBot,
		},
	}
}

func (a slackAccessor) channelRecord(channel slack.Channel) coredatasource.Record {
	return coredatasource.Record{
		ID:         channel.ID,
		Datasource: a.spec.Name,
		Entity:     ChannelEntity,
		Title:      firstNonEmpty(channel.Name, channel.NameNormalized, channel.ID),
		Content:    strings.Join(cleaned([]string{channel.Topic.Value, channel.Purpose.Value}), " "),
		Metadata: map[string]string{
			"id":          channel.ID,
			"name":        channel.Name,
			"channel_id":  channel.ID,
			"topic":       channel.Topic.Value,
			"purpose":     channel.Purpose.Value,
			"is_archived": boolString(channel.IsArchived),
			"is_member":   boolString(channel.IsMember),
		},
		Raw: Channel{
			ID:             channel.ID,
			Name:           channel.Name,
			NameNormalized: channel.NameNormalized,
			IsChannel:      channel.IsChannel,
			IsGroup:        channel.IsGroup,
			IsIM:           channel.IsIM,
			IsMPIM:         channel.IsMpIM,
			IsArchived:     channel.IsArchived,
			IsMember:       channel.IsMember,
			Creator:        channel.Creator,
			NumMembers:     channel.NumMembers,
			TopicValue:     channel.Topic.Value,
			PurposeValue:   channel.Purpose.Value,
		},
	}
}

func (a slackAccessor) searchMessageRecord(message slack.SearchMessage) coredatasource.Record {
	channelID := firstNonEmpty(message.Channel.ID, slackChannelIDFromPermalink(message.Permalink))
	id := messageID(channelID, message.Timestamp)
	record := coredatasource.Record{
		ID:         id,
		Datasource: a.spec.Name,
		Entity:     MessageEntity,
		Title:      firstNonEmpty(message.Channel.Name, message.Username, message.User, channelID),
		Content:    message.Text,
		URL:        message.Permalink,
		Metadata: map[string]string{
			"channel_id": channelID,
			"channel":    message.Channel.Name,
			"user":       firstNonEmpty(message.User, message.Username),
			"permalink":  message.Permalink,
			"timestamp":  message.Timestamp,
		},
		Raw: Message{
			ID:        id,
			Timestamp: message.Timestamp,
			ChannelID: channelID,
			Channel:   message.Channel.Name,
			User:      message.User,
			Username:  message.Username,
			Text:      message.Text,
			Permalink: message.Permalink,
		},
	}
	return normalizeSlackMessageRecord(record)
}

func (a slackAccessor) messageRecord(channelID string, message slack.Message) coredatasource.Record {
	id := messageID(channelID, message.Timestamp)
	return coredatasource.Record{
		ID:         id,
		Datasource: a.spec.Name,
		Entity:     MessageEntity,
		Title:      firstNonEmpty(channelID, message.User),
		Content:    message.Text,
		Metadata: map[string]string{
			"channel_id": channelID,
			"user":       message.User,
			"timestamp":  message.Timestamp,
			"thread_ts":  message.ThreadTimestamp,
		},
		Raw: Message{
			ID:        id,
			Timestamp: message.Timestamp,
			ChannelID: channelID,
			User:      message.User,
			Text:      message.Text,
		},
	}
}

func (a slackAccessor) threadMessageRecord(channelID, threadTS string, message slack.Message) coredatasource.Record {
	ts := firstNonEmpty(message.Timestamp, threadTS)
	id := threadMessageID(channelID, threadTS, ts)
	return coredatasource.Record{
		ID:         id,
		Datasource: a.spec.Name,
		Entity:     ThreadMessageEntity,
		Title:      firstNonEmpty(message.User, ts),
		Content:    message.Text,
		Metadata: map[string]string{
			"channel_id": channelID,
			"user":       message.User,
			"timestamp":  ts,
			"thread_ts":  threadTS,
		},
		Raw: ThreadMessage{
			ID:              id,
			Timestamp:       ts,
			ThreadTimestamp: threadTS,
			ChannelID:       channelID,
			User:            message.User,
			Text:            message.Text,
		},
	}
}

func entitySpecs() []coredatasource.EntitySpec {
	userEntity := runtimedatasource.EntityOf[User](UserEntity, "Slack workspace user.")
	userEntity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityIndex,
	}

	channelEntity := runtimedatasource.EntityOf[Channel](ChannelEntity, "Slack public, private, direct, or multi-party conversation.")
	channelEntity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
		coredatasource.EntityCapabilityIndex,
	}
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
	channelEntity.Relations = []coredatasource.RelationSpec{{
		Name:         "members",
		Description:  "Exact Slack channel members.",
		TargetEntity: UserEntity,
		Exact:        true,
	}}

	messageEntity := runtimedatasource.EntityOf[Message](MessageEntity, "Slack message search result. Native Slack search requires a user token; bot mode searches configured indexed channels.")
	messageEntity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityRelation,
		coredatasource.EntityCapabilityIndex,
	}
	messageEntity.Detectors = []coredatasource.DetectorSpec{{
		Name:          "slack_message_permalink",
		Kind:          coredatasource.DetectorURL,
		Pattern:       `https?://[^\s<>"']+/archives/([A-Z0-9]+)/p([0-9]{10})([0-9]{6})`,
		IDTemplate:    "$1:$2.$3",
		QueryTemplate: "$0",
		URLTemplate:   "$0",
		Confidence:    0.95,
		Annotations: map[string]string{
			"prewarm.get":       "true",
			"prewarm.relations": "thread_messages",
			"prewarm.limit":     "20",
		},
	}}
	messageEntity.Relations = []coredatasource.RelationSpec{{
		Name:         "thread_messages",
		Description:  "Slack thread messages for a message.",
		TargetEntity: ThreadMessageEntity,
		Exact:        true,
	}}

	threadEntity := runtimedatasource.EntityOf[ThreadMessage](ThreadMessageEntity, "Slack thread message for exact channel/thread lookups.")
	threadEntity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
	}

	return []coredatasource.EntitySpec{userEntity, channelEntity, messageEntity, threadEntity}
}

func listUsersPage(ctx context.Context, api slackAPI, cursor string, limit int) ([]slack.User, string, error) {
	page := api.GetUsersPaginated(slack.GetUsersOptionLimit(limit), slack.GetUsersOptionCursor(cursor))
	page, err := page.Next(ctx)
	if err != nil {
		return nil, "", page.Failure(err)
	}
	return page.Users, page.Cursor, nil
}

func listChannelsPage(ctx context.Context, api slackAPI, cursor string, limit int) ([]slack.Channel, string, error) {
	typeIndex, slackCursor := decodeConversationCursor(cursor)
	for i := typeIndex; i < len(slackConversationTypes); i++ {
		channels, next, err := api.GetConversationsContext(ctx, &slack.GetConversationsParameters{
			Cursor:          slackCursor,
			Limit:           limit,
			Types:           []string{slackConversationTypes[i]},
			ExcludeArchived: true,
		})
		if isSlackMissingScope(err) {
			slackCursor = ""
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if next != "" {
			return channels, encodeConversationCursor(i, next), nil
		}
		if len(channels) > 0 {
			return channels, encodeConversationCursor(i+1, ""), nil
		}
		slackCursor = ""
	}
	return nil, "", nil
}

func listChannels(ctx context.Context, api slackAPI, limit int) ([]slack.Channel, error) {
	var out []slack.Channel
	cursor := ""
	for {
		page, next, err := listChannelsPage(ctx, api, cursor, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if next == "" || len(out) >= limit {
			break
		}
		cursor = next
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func encodeConversationCursor(typeIndex int, cursor string) string {
	if typeIndex >= len(slackConversationTypes) {
		return ""
	}
	return fmt.Sprintf("%d:%s", typeIndex, cursor)
}

func decodeConversationCursor(cursor string) (int, string) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, ""
	}
	rawIndex, rest, ok := strings.Cut(cursor, ":")
	if !ok {
		return 0, cursor
	}
	for i := range slackConversationTypes {
		if rawIndex == fmt.Sprint(i) {
			return i, rest
		}
	}
	return 0, cursor
}

func isSlackMissingScope(err error) bool {
	if err == nil {
		return false
	}
	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) && slackErr.Err == "missing_scope" {
		return true
	}
	return err.Error() == "missing_scope"
}

func isSlackInaccessibleConversation(err error) bool {
	if err == nil {
		return false
	}
	if isSlackMissingScope(err) {
		return true
	}
	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) {
		switch slackErr.Err {
		case "not_in_channel", "channel_not_found":
			return true
		}
	}
	switch err.Error() {
	case "not_in_channel", "channel_not_found":
		return true
	default:
		return false
	}
}

func isSlackProviderSearchUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errSlackNativeSearchUnavailable) {
		return true
	}
	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) {
		switch slackErr.Err {
		case "missing_scope", "not_allowed_token_type", "invalid_auth", "token_revoked":
			return true
		}
	}
	switch err.Error() {
	case "missing_scope", "not_allowed_token_type":
		return true
	default:
		return false
	}
}

func searchHistoryWindow(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultSearchHistoryWindow
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return defaultSearchHistoryWindow
	}
	return duration
}

func slackSearchIncludeThreads(cfg SearchConfig) bool {
	return cfg.IncludeThreads == nil || *cfg.IncludeThreads
}

func slackTimestamp(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixNano())/float64(time.Second), 'f', 6, 64)
}

func encodeSlackMessageCorpusCursor(cursor slackMessageCorpusCursor, channelCount int) string {
	if cursor.Cursor == "" && cursor.Channel >= channelCount {
		return ""
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeSlackMessageCorpusCursor(value string) (slackMessageCorpusCursor, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return slackMessageCorpusCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return slackMessageCorpusCursor{}, fmt.Errorf("slack message corpus cursor: %w", err)
	}
	var cursor slackMessageCorpusCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return slackMessageCorpusCursor{}, fmt.Errorf("slack message corpus cursor: %w", err)
	}
	if cursor.Channel < 0 {
		cursor.Channel = 0
	}
	return cursor, nil
}

func getMessageByTimestamp(ctx context.Context, api slackAPI, channelID, ts string) (slack.Msg, error) {
	response, err := api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: channelID, Latest: ts, Oldest: ts, Inclusive: true, Limit: 1})
	if err != nil {
		return slack.Msg{}, err
	}
	for _, msg := range response.Messages {
		if msg.Timestamp == ts {
			return msg.Msg, nil
		}
	}
	return slack.Msg{}, coredatasource.ErrNotFound
}

func getThreadMessageByTimestamp(ctx context.Context, api slackAPI, channelID, threadTS, ts string) (slack.Message, error) {
	messages, _, _, err := api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: channelID, Timestamp: threadTS, Inclusive: true, Limit: 200})
	if err != nil {
		return slack.Message{}, err
	}
	for _, msg := range messages {
		if msg.Timestamp == ts {
			return msg, nil
		}
	}
	return slack.Message{}, coredatasource.ErrNotFound
}

func filterUsers(users []slack.User, query string) []slack.User {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return users
	}
	var out []slack.User
	for _, user := range users {
		values := []string{user.ID, user.Name, user.RealName, user.Profile.DisplayName, user.Profile.RealName, user.Profile.Email, user.Profile.Title}
		if anyContains(values, query) {
			out = append(out, user)
		}
	}
	return out
}

func filterChannels(channels []slack.Channel, query string) []slack.Channel {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return channels
	}
	var out []slack.Channel
	for _, channel := range channels {
		values := []string{channel.ID, channel.Name, channel.NameNormalized, channel.Topic.Value, channel.Purpose.Value}
		if anyContains(values, query) {
			out = append(out, channel)
		}
	}
	return out
}

func filterRecords(records []coredatasource.Record, query string) []coredatasource.Record {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return records
	}
	var out []coredatasource.Record
	for _, record := range records {
		values := []string{record.ID, record.Title, record.Content}
		for _, value := range record.Metadata {
			values = append(values, value)
		}
		if anyContains(values, query) {
			out = append(out, record)
		}
	}
	return out
}

func anyContains(values []string, query string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
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

func slackWorkspaceURL(ctx context.Context, api slackAPI) string {
	auth, err := api.AuthTestContext(ctx)
	if err != nil || auth == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(auth.URL), "/")
}

func withSlackPermalink(record coredatasource.Record, workspaceURL, channelID, ts string) coredatasource.Record {
	permalink := slackPermalink(workspaceURL, channelID, ts)
	if permalink == "" {
		return normalizeSlackMessageRecord(record)
	}
	record.URL = permalink
	if record.Metadata == nil {
		record.Metadata = map[string]string{}
	}
	record.Metadata["permalink"] = permalink
	if raw, ok := record.Raw.(Message); ok {
		raw.Permalink = permalink
		record.Raw = raw
	}
	if raw, ok := record.Raw.(ThreadMessage); ok {
		raw.Permalink = permalink
		record.Raw = raw
	}
	return normalizeSlackMessageRecord(record)
}

func slackPermalink(workspaceURL, channelID, ts string) string {
	workspaceURL = strings.TrimRight(strings.TrimSpace(workspaceURL), "/")
	channelID = strings.TrimSpace(channelID)
	seconds, fraction, ok := strings.Cut(strings.TrimSpace(ts), ".")
	if workspaceURL == "" || channelID == "" || !ok || seconds == "" || fraction == "" {
		return ""
	}
	var micros strings.Builder
	for _, r := range fraction {
		if r < '0' || r > '9' {
			break
		}
		micros.WriteRune(r)
		if micros.Len() == 6 {
			break
		}
	}
	if micros.Len() == 0 {
		return ""
	}
	for micros.Len() < 6 {
		micros.WriteByte('0')
	}
	return workspaceURL + "/archives/" + channelID + "/p" + seconds + micros.String()
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

func slackMessageTargetFromPermalink(permalink string) (string, string, bool) {
	channelID := slackChannelIDFromPermalink(permalink)
	if channelID == "" {
		return "", "", false
	}
	const marker = "/p"
	index := strings.Index(permalink, marker)
	if index < 0 {
		return "", "", false
	}
	rest := permalink[index+len(marker):]
	var digits strings.Builder
	for _, r := range rest {
		if r < '0' || r > '9' {
			break
		}
		digits.WriteRune(r)
	}
	raw := digits.String()
	if len(raw) != 16 {
		return "", "", false
	}
	return channelID, raw[:10] + "." + raw[10:], true
}

func parseMessageID(id string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(id), ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func parseThreadMessageID(id string) (string, string, string, bool) {
	parts := strings.Split(strings.TrimSpace(id), ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func messageID(channelID, ts string) string {
	if channelID == "" || ts == "" {
		return ""
	}
	return channelID + ":" + ts
}

func threadMessageID(channelID, threadTS, ts string) string {
	if channelID == "" || threadTS == "" || ts == "" {
		return ""
	}
	return channelID + ":" + threadTS + ":" + ts
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func cleaned(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
