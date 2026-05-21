package connector

import (
	"context"
	"errors"
	"testing"

	connectoroperation "github.com/codewandler/connectors/operation"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/operation"
)

func TestToolNameUsesDefaultAndCustomInstanceNames(t *testing.T) {
	action := Action{Kind: "slack", Operation: "slack.message.search", Suffix: "search"}
	if got := ToolName(Instance{ID: "slack", Kind: "slack"}, action); got != "slack_search" {
		t.Fatalf("default tool name = %q, want slack_search", got)
	}
	if got := ToolName(Instance{ID: "slack-cs-team", Kind: "slack"}, action); got != "slack_cs_team_search" {
		t.Fatalf("custom tool name = %q, want slack_cs_team_search", got)
	}
}

func TestOperationsExecuteWithConfiguredInstance(t *testing.T) {
	executor := &fakeConnectorExecutor{
		result: connectoroperation.Result{
			Status:     connectoroperation.StatusOK,
			Data:       map[string]any{"ok": true},
			HTTPStatus: 200,
		},
	}
	ops, err := Operations(executor, []Instance{{ID: "gitlab-prod", Kind: "gitlab"}}, []Action{{
		Kind:      "gitlab",
		Operation: "gitlab.project.search",
		Suffix:    "project_search",
	}})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops len = %d, want 1", len(ops))
	}
	if got := string(ops[0].Spec().Ref.Name); got != "gitlab_prod_project_search" {
		t.Fatalf("op name = %q, want gitlab_prod_project_search", got)
	}
	result := ops[0].Run(operation.NewContext(context.Background(), nil), map[string]any{"query": "runtime"})
	if result.IsError() {
		t.Fatalf("result = %#v", result)
	}
	if executor.instanceID != "gitlab-prod" || executor.operation != "gitlab.project.search" {
		t.Fatalf("executor call = instance %q operation %q", executor.instanceID, executor.operation)
	}
	if executor.params["query"] != "runtime" {
		t.Fatalf("params = %#v, want query runtime", executor.params)
	}
	out, ok := result.Output.(Output)
	if !ok {
		t.Fatalf("output = %#v, want Output", result.Output)
	}
	if out.Status != "ok" || out.HTTPStatus != 200 {
		t.Fatalf("output = %#v, want ok/200", out)
	}
}

func TestOperationsReturnFailureForConnectorErrors(t *testing.T) {
	executor := &fakeConnectorExecutor{err: errors.New("no token")}
	ops, err := Operations(executor, []Instance{{ID: "jira", Kind: "jira"}}, []Action{{
		Kind:      "jira",
		Operation: "jira.issue.search",
		Suffix:    "issue_search",
	}})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[0].Run(operation.NewContext(context.Background(), nil), map[string]any{"jql": "project = DEV"})
	if !result.IsError() || result.Error == nil || result.Error.Code != "jira_issue_search_failed" {
		t.Fatalf("result = %#v, want jira_issue_search_failed", result)
	}
}

func TestDatasourceFlattenRecordsHandlesNestedSlackMessageMatches(t *testing.T) {
	records := flattenRecords(map[string]any{
		"messages": map[string]any{
			"matches": []any{
				map[string]any{
					"iid":       "m1",
					"text":      "hello",
					"permalink": "https://example.test/archives/C1/p123",
					"channel": map[string]any{
						"id":   "C1",
						"name": "general",
					},
				},
			},
		},
	})
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	if got := firstString(records[0], "channel.name"); got != "general" {
		t.Fatalf("channel.name = %q, want general", got)
	}
}

func TestDatasourceFlattenRecordsHandlesJiraProjectValues(t *testing.T) {
	records := flattenRecords(map[string]any{
		"values": []any{
			map[string]any{"key": "DEV", "name": "Development"},
		},
	})
	if len(records) != 1 || firstString(records[0], "key") != "DEV" {
		t.Fatalf("records = %#v", records)
	}
}

func TestDatasourceActionMapsResultPathDefaultsAndMetadata(t *testing.T) {
	executor := &fakeConnectorExecutor{
		result: connectoroperation.Result{
			Status: connectoroperation.StatusOK,
			Data: map[string]any{
				"messages": map[string]any{
					"matches": []any{
						map[string]any{
							"iid":       "m1",
							"ts":        "1710000000.000100",
							"text":      "deployment finished",
							"permalink": "https://example.test/archives/C1/p171",
							"channel": map[string]any{
								"id":   "C1",
								"name": "deploys",
							},
							"user": "U1",
						},
					},
				},
			},
		},
	}
	provider := NewDatasourceProvider(executor, []DatasourceAction{{
		Kind:       "slack",
		Entity:     coredatasource.EntitySpec{Type: "slack.message"},
		SearchOp:   "slack.message.search",
		QueryParam: "query",
		LimitParam: "count",
		ResultPath: "messages.matches",
		ParamDefaults: map[string]any{
			"sort":     "timestamp",
			"sort_dir": "desc",
		},
		IDFields:    []string{"iid"},
		TitleFields: []string{"channel.name"},
		TextFields:  []string{"text"},
		URLFields:   []string{"permalink"},
		MetadataFields: map[string][]string{
			"channel_id": {"channel.id"},
			"channel":    {"channel.name"},
			"user":       {"user"},
			"permalink":  {"permalink"},
		},
		RecordTransform: func(record coredatasource.Record) coredatasource.Record {
			record.Metadata["transformed"] = "yes"
			return record
		},
	}})
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:      "slack-bot",
		Connector: "slack-bot",
		Kind:      "slack",
		Entities:  []coredatasource.EntityType{"slack.message"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{
		Entity: "slack.message",
		Query:  "deploy",
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if executor.params["query"] != "deploy" || executor.params["count"] != 5 || executor.params["sort"] != "timestamp" || executor.params["sort_dir"] != "desc" {
		t.Fatalf("params = %#v, want query/count/default sort", executor.params)
	}
	if len(result.Records) != 1 {
		t.Fatalf("records = %#v, want one", result.Records)
	}
	record := result.Records[0]
	if record.ID != "m1" || record.Title != "deploys" || record.Content != "deployment finished" || record.URL == "" {
		t.Fatalf("record = %#v, want normalized Slack message", record)
	}
	if record.Metadata["channel_id"] != "C1" || record.Metadata["channel"] != "deploys" || record.Metadata["user"] != "U1" || record.Metadata["permalink"] == "" {
		t.Fatalf("metadata = %#v, want mapped Slack fields", record.Metadata)
	}
	if record.Metadata["transformed"] != "yes" {
		t.Fatalf("metadata = %#v, want record transform applied", record.Metadata)
	}
	entities := provider.Entities()
	if len(entities) != 1 || !entities[0].Supports(coredatasource.EntityCapabilitySearch) || entities[0].Supports(coredatasource.EntityCapabilityGet) {
		t.Fatalf("entities = %#v, want search-only", entities)
	}
}

func TestDatasourceRelationFetchesSlackChannelMembersAndHydratesUsers(t *testing.T) {
	executor := &scriptedConnectorExecutor{results: map[string][]connectoroperation.Result{
		"slack.channel.members": {{
			Status: connectoroperation.StatusOK,
			Data: map[string]any{
				"members": []any{"U2", "U1"},
			},
		}},
		"slack.user.list": {{
			Status: connectoroperation.StatusOK,
			Data: map[string]any{
				"members": []any{
					map[string]any{"id": "U1", "name": "alice", "real_name": "Alice Example"},
				},
			},
		}},
		"slack.user.info": {{
			Status: connectoroperation.StatusOK,
			Data: map[string]any{
				"user": map[string]any{"id": "U2", "name": "bob", "real_name": "Bob Example"},
			},
		}},
	}}
	provider := NewDatasourceProvider(executor, []DatasourceAction{
		{
			Kind:        "slack",
			Entity:      coredatasource.EntitySpec{Type: "slack.user"},
			SearchOp:    "slack.user.list",
			GetOp:       "slack.user.info",
			QueryParam:  "-",
			IDParam:     "user",
			IDFields:    []string{"id"},
			TitleFields: []string{"real_name", "name"},
		},
		{
			Kind:     "slack",
			Entity:   coredatasource.EntitySpec{Type: "slack.channel"},
			SearchOp: "slack.channel.list",
			GetOp:    "slack.channel.info",
			IDParam:  "channel",
			Relations: []DatasourceRelationAction{{
				Name:         "members",
				TargetEntity: "slack.user",
				Operation:    "slack.channel.members",
				IDParam:      "channel",
				ResultPath:   "members",
				Exact:        true,
			}},
		},
	})
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:      "slack-bot",
		Connector: "slack-bot",
		Kind:      "slack",
		Entities:  []coredatasource.EntityType{"slack.channel", "slack.user"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Relationer).Relation(context.Background(), coredatasource.RelationRequest{
		Entity:   "slack.channel",
		ID:       "C1",
		Relation: "members",
	})
	if err != nil {
		t.Fatalf("Relation: %v", err)
	}
	if !result.Exact || !result.Complete || result.TargetEntity != "slack.user" {
		t.Fatalf("result metadata = %#v, want exact complete slack.user", result)
	}
	if len(result.Records) != 2 || result.Records[0].ID != "U2" || result.Records[0].Title != "Bob Example" || result.Records[1].ID != "U1" {
		t.Fatalf("records = %#v, want requested member order with hydrated users", result.Records)
	}
	if executor.calls[0].operation != "slack.channel.members" || executor.calls[0].params["channel"] != "C1" {
		t.Fatalf("calls = %#v, want channel.members with channel C1", executor.calls)
	}
}

func TestDatasourceLocalFilterPaginatesBeforeFiltering(t *testing.T) {
	executor := &scriptedConnectorExecutor{results: map[string][]connectoroperation.Result{
		"slack.channel.list": {
			{
				Status: connectoroperation.StatusOK,
				Data: map[string]any{
					"channels": []any{
						map[string]any{"id": "C1", "name": "general"},
					},
					"response_metadata": map[string]any{"next_cursor": "next-page"},
				},
			},
			{
				Status: connectoroperation.StatusOK,
				Data: map[string]any{
					"channels": []any{
						map[string]any{"id": "C2", "name": "lyse-internal"},
					},
				},
			},
		},
	}}
	provider := NewDatasourceProvider(executor, []DatasourceAction{{
		Kind:           "slack",
		Entity:         coredatasource.EntitySpec{Type: "slack.channel"},
		SearchOp:       "slack.channel.list",
		QueryParam:     "-",
		LocalFilter:    true,
		NextCursorPath: "response_metadata.next_cursor",
		IDFields:       []string{"id"},
		TitleFields:    []string{"name"},
	}})
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:      "slack-bot",
		Connector: "slack-bot",
		Kind:      "slack",
		Entities:  []coredatasource.EntityType{"slack.channel"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{
		Entity: "slack.channel",
		Query:  "lyse",
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "C2" {
		t.Fatalf("records = %#v, want second-page lyse channel", result.Records)
	}
	if len(executor.calls) != 2 || executor.calls[1].params["cursor"] != "next-page" {
		t.Fatalf("calls = %#v, want second page with cursor", executor.calls)
	}
}

type fakeConnectorExecutor struct {
	instanceID string
	operation  string
	role       string
	params     map[string]any
	result     connectoroperation.Result
	err        error
}

func (e *fakeConnectorExecutor) ExecWithInstance(_ context.Context, instanceID, opName, role string, params map[string]any) (connectoroperation.Result, error) {
	e.instanceID = instanceID
	e.operation = opName
	e.role = role
	e.params = params
	return e.result, e.err
}

type connectorCall struct {
	instanceID string
	operation  string
	role       string
	params     map[string]any
}

type scriptedConnectorExecutor struct {
	calls   []connectorCall
	results map[string][]connectoroperation.Result
}

func (e *scriptedConnectorExecutor) ExecWithInstance(_ context.Context, instanceID, opName, role string, params map[string]any) (connectoroperation.Result, error) {
	copied := map[string]any{}
	for key, value := range params {
		copied[key] = value
	}
	e.calls = append(e.calls, connectorCall{instanceID: instanceID, operation: opName, role: role, params: copied})
	results := e.results[opName]
	if len(results) == 0 {
		return connectoroperation.Result{Status: connectoroperation.StatusOK}, nil
	}
	result := results[0]
	e.results[opName] = results[1:]
	return result, nil
}
