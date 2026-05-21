package evaluator

import (
	"time"

	"github.com/fluxplane/engine/core/agent"
	"github.com/fluxplane/engine/core/operation"
	coreusage "github.com/fluxplane/engine/core/usage"
	clientapi "github.com/fluxplane/engine/orchestration/client"
)

type Scenario struct {
	Name        string   `json:"name" yaml:"name"`
	Objective   string   `json:"objective" yaml:"objective"`
	Target      Target   `json:"target" yaml:"target"`
	Prompt      string   `json:"prompt" yaml:"prompt"`
	Rubric      []string `json:"rubric,omitempty" yaml:"rubric,omitempty"`
	MaxTurns    int      `json:"max_turns,omitempty" yaml:"max_turns,omitempty"`
	Autonomous  bool     `json:"autonomous,omitempty" yaml:"autonomous,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
}

type Target struct {
	Kind         string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Description  string `json:"description,omitempty" yaml:"description,omitempty"`
	BaseURL      string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	UnixSocket   string `json:"unix_socket,omitempty" yaml:"unix_socket,omitempty"`
	BearerToken  string `json:"bearer_token,omitempty" yaml:"bearer_token,omitempty"`
	Session      string `json:"session,omitempty" yaml:"session,omitempty"`
	Conversation string `json:"conversation,omitempty" yaml:"conversation,omitempty"`
}

type ReportStatus string

const (
	ReportStatusUnknown ReportStatus = "unknown"
	ReportStatusPassed  ReportStatus = "passed"
	ReportStatusFailed  ReportStatus = "failed"
	ReportStatusPartial ReportStatus = "partial"
)

type Report struct {
	Scenario        Scenario      `json:"scenario" yaml:"scenario"`
	Status          ReportStatus  `json:"status" yaml:"status"`
	Score           float64       `json:"score,omitempty" yaml:"score,omitempty"`
	Summary         string        `json:"summary,omitempty" yaml:"summary,omitempty"`
	Metrics         Metrics       `json:"metrics,omitempty" yaml:"metrics,omitempty"`
	Assessment      Assessment    `json:"assessment,omitempty" yaml:"assessment,omitempty"`
	Findings        []Finding     `json:"findings,omitempty" yaml:"findings,omitempty"`
	Evidence        []Evidence    `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	Recommendations []string      `json:"recommendations,omitempty" yaml:"recommendations,omitempty"`
	Artifacts       []ArtifactRef `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
}

type Metrics struct {
	RuntimeMS           int64   `json:"runtime_ms,omitempty" yaml:"runtime_ms,omitempty"`
	ModelCalls          int     `json:"model_calls,omitempty" yaml:"model_calls,omitempty"`
	ToolCalls           int     `json:"tool_calls,omitempty" yaml:"tool_calls,omitempty"`
	OperationCalls      int     `json:"operation_calls,omitempty" yaml:"operation_calls,omitempty"`
	OperationFailures   int     `json:"operation_failures,omitempty" yaml:"operation_failures,omitempty"`
	OperationRejections int     `json:"operation_rejections,omitempty" yaml:"operation_rejections,omitempty"`
	Retries             int     `json:"retries,omitempty" yaml:"retries,omitempty"`
	EventCount          int     `json:"event_count,omitempty" yaml:"event_count,omitempty"`
	InputTokens         float64 `json:"input_tokens,omitempty" yaml:"input_tokens,omitempty"`
	OutputTokens        float64 `json:"output_tokens,omitempty" yaml:"output_tokens,omitempty"`
	ReasoningTokens     float64 `json:"reasoning_tokens,omitempty" yaml:"reasoning_tokens,omitempty"`
	TotalTokens         float64 `json:"total_tokens,omitempty" yaml:"total_tokens,omitempty"`
	explicitTotalTokens bool
}

type Assessment struct {
	TaskSuccess          string `json:"task_success,omitempty" yaml:"task_success,omitempty"`
	ConversationQuality  string `json:"conversation_quality,omitempty" yaml:"conversation_quality,omitempty"`
	ClarificationQuality string `json:"clarification_quality,omitempty" yaml:"clarification_quality,omitempty"`
	ToolUseQuality       string `json:"tool_use_quality,omitempty" yaml:"tool_use_quality,omitempty"`
	EvidenceQuality      string `json:"evidence_quality,omitempty" yaml:"evidence_quality,omitempty"`
}

type Finding struct {
	Title      string     `json:"title" yaml:"title"`
	Severity   string     `json:"severity,omitempty" yaml:"severity,omitempty"`
	Confidence string     `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Detail     string     `json:"detail,omitempty" yaml:"detail,omitempty"`
	Evidence   []Evidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
}

type Evidence struct {
	Kind      string `json:"kind" yaml:"kind"`
	Summary   string `json:"summary,omitempty" yaml:"summary,omitempty"`
	Reference string `json:"reference,omitempty" yaml:"reference,omitempty"`
	Quote     string `json:"quote,omitempty" yaml:"quote,omitempty"`
}

type ArtifactRef struct {
	Kind string `json:"kind" yaml:"kind"`
	Path string `json:"path" yaml:"path"`
}

func MetricsFromRun(start time.Time, events []clientapi.Event, records []coreusage.Recorded) Metrics {
	metrics := Metrics{RuntimeMS: time.Since(start).Milliseconds(), EventCount: len(events)}
	for _, event := range events {
		metrics.AddEvent(event)
	}
	for _, record := range records {
		metrics.AddUsage(record)
	}
	return metrics
}

func (m *Metrics) AddEvent(event clientapi.Event) {
	if m == nil {
		return
	}
	switch event.Kind {
	case clientapi.EventAgentStepCompleted:
		m.ModelCalls++
		if event.Agent != nil && event.Agent.Decision.Kind == agent.DecisionOperation {
			m.ToolCalls += len(event.Agent.Decision.Operations)
		}
	case clientapi.EventOperationRequested:
		m.OperationCalls++
	case clientapi.EventOperationCompleted:
		if event.Operation != nil && event.Operation.Result != nil {
			switch event.Operation.Result.Status {
			case operation.StatusFailed, operation.StatusCanceled:
				m.OperationFailures++
			case operation.StatusRejected:
				m.OperationRejections++
			}
		}
	case clientapi.EventRunFailed:
		m.OperationFailures++
	}
}

func (m *Metrics) AddUsage(record coreusage.Recorded) {
	if m == nil {
		return
	}
	for _, measurement := range record.Measurements {
		switch measurement.Metric {
		case coreusage.MetricLLMInputTokens:
			m.InputTokens += measurement.Quantity
		case coreusage.MetricLLMOutputTokens:
			m.OutputTokens += measurement.Quantity
		case coreusage.MetricLLMReasoningTokens:
			m.ReasoningTokens += measurement.Quantity
		case coreusage.MetricLLMTotalTokens:
			m.TotalTokens += measurement.Quantity
			m.explicitTotalTokens = true
		case coreusage.MetricWallTime:
			if measurement.Unit == coreusage.UnitMillisecond {
				m.RuntimeMS += int64(measurement.Quantity)
			}
		}
	}
	if !m.explicitTotalTokens {
		m.TotalTokens = m.InputTokens + m.OutputTokens + m.ReasoningTokens
	}
}
