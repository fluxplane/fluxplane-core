package slack

import (
	coredata "github.com/fluxplane/engine/core/data"
	runtimedata "github.com/fluxplane/engine/runtime/data"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
)

const (
	SlackUserView               coredata.ViewName = "slack.user"
	SlackChannelView            coredata.ViewName = "slack.channel"
	SlackMessageView            coredata.ViewName = "slack.message"
	SlackChannelWithMembersView coredata.ViewName = "slack.channel_with_members"
)

// DataSourceSpec describes the Slack source schema and default materialized views.
func DataSourceSpec() coredata.SourceSpec {
	spec := runtimedata.SourceFromDatasource("slack", Name, entitySpecs(), DataViews()...)
	spec.ConfigSchema = operationruntime.SchemaFor[datasourceConfig]()
	return spec
}

type datasourceConfig struct {
	Instance string `json:"instance,omitempty" jsonschema:"description=Slack plugin instance that provides credentials for this datasource."`
}

// DataViews returns the Slack materializations the query API should prefer.
func DataViews() []coredata.ViewSpec {
	return []coredata.ViewSpec{
		runtimedata.ViewOf[User](
			SlackUserView,
			coredata.EntityType(UserEntity),
			runtimedata.WithViewDescription("Slack workspace users by id, name, display name, and email."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch),
		),
		runtimedata.ViewOf[Channel](
			SlackChannelView,
			coredata.EntityType(ChannelEntity),
			runtimedata.WithViewDescription("Slack conversations by id, name, type, topic, and purpose."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch, coredata.QueryRelation),
		),
		runtimedata.ViewOf[Message](
			SlackMessageView,
			coredata.EntityType(MessageEntity),
			runtimedata.WithViewDescription("Slack message search and channel history records."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch, coredata.QueryRelation),
			runtimedata.WithViewAnnotations(map[string]string{"mirror": "bounded-or-live"}),
		),
		runtimedata.ViewOf[slackChannelWithMembersView](
			SlackChannelWithMembersView,
			coredata.EntityType(ChannelEntity),
			runtimedata.WithViewDescription("Slack channels with minimal member summaries for membership questions."),
			runtimedata.WithViewIncludes(coredata.RelationIncludeSpec{
				Relation: "members",
				Target:   coredata.EntityType(UserEntity),
				Fields:   []string{"id", "name", "real_name", "display_name", "email"},
			}),
			runtimedata.WithViewQueryHints(coredata.QueryList, coredata.QuerySearch, coredata.QueryRelation),
		),
	}
}

type slackChannelWithMembersView struct {
	ID             string                   `json:"id" datasource:"id,filterable" jsonschema:"description=Slack channel id."`
	Name           string                   `json:"name" datasource:"searchable,filterable" jsonschema:"description=Slack channel name."`
	NameNormalized string                   `json:"name_normalized,omitempty" datasource:"searchable" jsonschema:"description=Normalized channel name."`
	IsChannel      bool                     `json:"is_channel,omitempty" datasource:"filterable" jsonschema:"description=Whether this conversation is a public channel."`
	IsGroup        bool                     `json:"is_group,omitempty" datasource:"filterable" jsonschema:"description=Whether this conversation is a private channel."`
	IsArchived     bool                     `json:"is_archived,omitempty" datasource:"filterable" jsonschema:"description=Whether the channel is archived."`
	TopicValue     string                   `json:"topic_value,omitempty" datasource:"searchable" jsonschema:"description=Channel topic text."`
	PurposeValue   string                   `json:"purpose_value,omitempty" datasource:"searchable" jsonschema:"description=Channel purpose text."`
	Members        []slackMemberViewSummary `json:"members"`
}

type slackMemberViewSummary struct {
	ID          string `json:"id" datasource:"filterable" jsonschema:"description=Slack user id."`
	Name        string `json:"name" datasource:"searchable,filterable" jsonschema:"description=Slack username."`
	RealName    string `json:"real_name,omitempty" datasource:"searchable" jsonschema:"description=Slack real name."`
	DisplayName string `json:"display_name,omitempty" datasource:"searchable" jsonschema:"description=Slack display name."`
	Email       string `json:"email,omitempty" datasource:"searchable,filterable" jsonschema:"description=Slack profile email."`
}
