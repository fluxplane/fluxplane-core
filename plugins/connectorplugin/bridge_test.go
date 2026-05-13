package connectorplugin

import (
	"context"
	"errors"
	"testing"

	connectoroperation "github.com/codewandler/connectors/operation"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
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
	entities := provider.Entities()
	if len(entities) != 1 || !entities[0].Supports(coredatasource.EntityCapabilitySearch) || entities[0].Supports(coredatasource.EntityCapabilityGet) {
		t.Fatalf("entities = %#v, want search-only", entities)
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
