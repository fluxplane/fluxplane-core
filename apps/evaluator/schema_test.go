package evaluator

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreusage "github.com/fluxplane/fluxplane-core/core/usage"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-operation"
	"gopkg.in/yaml.v3"
)

func TestScenarioAndReportSerialize(t *testing.T) {
	scenario := Scenario{
		Name:      "support-slack-bot",
		Objective: "Evaluate whether the bot handles ambiguous support requests.",
		Target: Target{
			Kind:         "support-slack-bot",
			UnixSocket:   "/tmp/support.sock",
			Session:      "support",
			Conversation: "eval-support",
		},
		Prompt:     "Ask a vague PTO question and check whether the bot clarifies.",
		Rubric:     []string{"asks clarifying questions", "cites evidence"},
		MaxTurns:   4,
		Autonomous: true,
	}
	report := Report{
		Scenario: scenario,
		Status:   ReportStatusPartial,
		Score:    0.75,
		Metrics:  Metrics{RuntimeMS: 1200, ModelCalls: 2, OperationCalls: 1, InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		Assessment: Assessment{
			TaskSuccess:          "partial",
			ClarificationQuality: "good",
		},
		Findings:        []Finding{{Title: "Clarifies ambiguity", Severity: "low", Confidence: "high"}},
		Evidence:        []Evidence{{Kind: "transcript", Quote: "Which policy do you mean?"}},
		Recommendations: []string{"Add source citations to every policy answer."},
		Artifacts:       []ArtifactRef{{Kind: "transcript", Path: "target-transcript.md"}},
	}

	jsonBytes, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var fromJSON Report
	if err := json.Unmarshal(jsonBytes, &fromJSON); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if fromJSON.Scenario.Target.Kind != scenario.Target.Kind || fromJSON.Status != ReportStatusPartial {
		t.Fatalf("json round trip = %#v", fromJSON)
	}

	yamlBytes, err := yaml.Marshal(report)
	if err != nil {
		t.Fatalf("yaml marshal: %v", err)
	}
	var fromYAML Report
	if err := yaml.Unmarshal(yamlBytes, &fromYAML); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if fromYAML.Scenario.Name != scenario.Name || len(fromYAML.Recommendations) != 1 {
		t.Fatalf("yaml round trip = %#v", fromYAML)
	}
}

func TestMetricsAggregation(t *testing.T) {
	start := time.Now().Add(-1500 * time.Millisecond)
	events := []clientapi.Event{
		{Kind: clientapi.EventAgentStepCompleted, Agent: &agent.StepResult{Decision: agent.Decision{Kind: agent.DecisionMessage}}},
		{Kind: clientapi.EventAgentStepCompleted, Agent: &agent.StepResult{Decision: agent.Decision{Kind: agent.DecisionOperation, Operations: []agent.OperationRequest{{Operation: operation.Ref{Name: "target_submit"}}}}}},
		{Kind: clientapi.EventOperationRequested},
		{Kind: clientapi.EventOperationCompleted, Operation: &clientapi.OperationEvent{Result: &operation.Result{Status: operation.StatusRejected}}},
		{Kind: clientapi.EventRunFailed},
	}
	records := []coreusage.Recorded{{Measurements: []coreusage.Measurement{
		{Metric: coreusage.MetricLLMInputTokens, Quantity: 11, Unit: coreusage.UnitToken},
		{Metric: coreusage.MetricLLMCachedTokens, Quantity: 5, Unit: coreusage.UnitToken},
		{Metric: coreusage.MetricLLMCacheWriteTokens, Quantity: 2, Unit: coreusage.UnitToken},
		{Metric: coreusage.MetricLLMOutputTokens, Quantity: 7, Unit: coreusage.UnitToken},
		{Metric: coreusage.MetricLLMReasoningTokens, Quantity: 3, Unit: coreusage.UnitToken},
	}}}

	metrics := MetricsFromRun(start, events, records)
	if metrics.ModelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", metrics.ModelCalls)
	}
	if metrics.ToolCalls != 1 || metrics.OperationCalls != 1 {
		t.Fatalf("tool/operation calls = %d/%d, want 1/1", metrics.ToolCalls, metrics.OperationCalls)
	}
	if metrics.OperationRejections != 1 || metrics.OperationFailures != 1 {
		t.Fatalf("operation rejected/failed = %d/%d, want 1/1", metrics.OperationRejections, metrics.OperationFailures)
	}
	if metrics.EventCount != len(events) {
		t.Fatalf("event count = %d, want %d", metrics.EventCount, len(events))
	}
	if metrics.InputTokens != 18 || metrics.OutputTokens != 7 || metrics.ReasoningTokens != 3 || metrics.TotalTokens != 25 {
		t.Fatalf("tokens = %#v", metrics)
	}
	if metrics.RuntimeMS <= 0 {
		t.Fatalf("runtime = %d, want positive", metrics.RuntimeMS)
	}
}

func TestMetricsAggregationDerivesTotalsAcrossRecords(t *testing.T) {
	var metrics Metrics
	metrics.AddUsage(coreusage.Recorded{Measurements: []coreusage.Measurement{
		{Metric: coreusage.MetricLLMInputTokens, Quantity: 10, Unit: coreusage.UnitToken},
		{Metric: coreusage.MetricLLMCachedTokens, Quantity: 3, Unit: coreusage.UnitToken},
		{Metric: coreusage.MetricLLMCacheWriteTokens, Quantity: 2, Unit: coreusage.UnitToken},
	}})
	metrics.AddUsage(coreusage.Recorded{Measurements: []coreusage.Measurement{
		{Metric: coreusage.MetricLLMOutputTokens, Quantity: 4, Unit: coreusage.UnitToken},
		{Metric: coreusage.MetricLLMReasoningTokens, Quantity: 2, Unit: coreusage.UnitToken},
	}})
	if metrics.TotalTokens != 19 {
		t.Fatalf("total tokens = %v, want 19", metrics.TotalTokens)
	}
}

func TestMetricsAggregationKeepsExplicitTotalTokens(t *testing.T) {
	var metrics Metrics
	metrics.AddUsage(coreusage.Recorded{Measurements: []coreusage.Measurement{
		{Metric: coreusage.MetricLLMInputTokens, Quantity: 10, Unit: coreusage.UnitToken},
		{Metric: coreusage.MetricLLMTotalTokens, Quantity: 12, Unit: coreusage.UnitToken},
	}})
	metrics.AddUsage(coreusage.Recorded{Measurements: []coreusage.Measurement{
		{Metric: coreusage.MetricLLMOutputTokens, Quantity: 4, Unit: coreusage.UnitToken},
	}})
	if metrics.TotalTokens != 12 {
		t.Fatalf("total tokens = %v, want explicit 12", metrics.TotalTokens)
	}
}
