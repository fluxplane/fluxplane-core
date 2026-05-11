package event

import (
	"time"

	"github.com/fluxplane/agentruntime/core/policy"
)

// Source describes the component that produced a record.
type Source struct {
	Component string `json:"component,omitempty"`
	Instance  string `json:"instance,omitempty"`
}

// Scope identifies the runtime scope in which an event happened.
type Scope struct {
	TenantID        string `json:"tenant_id,omitempty"`
	AppID           string `json:"app_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	UserID          string `json:"user_id,omitempty"`
	ChannelID       string `json:"channel_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	AgentInstanceID string `json:"agent_instance_id,omitempty"`
	ThreadID        string `json:"thread_id,omitempty"`
	TurnID          string `json:"turn_id,omitempty"`
	WorkflowID      string `json:"workflow_id,omitempty"`
	RunID           string `json:"run_id,omitempty"`
	StepID          string `json:"step_id,omitempty"`
	OperationID     string `json:"operation_id,omitempty"`
}

// Record is the delivery/persistence envelope for one typed event payload.
type Record struct {
	ID            string             `json:"id,omitempty"`
	Name          Name               `json:"name"`
	SchemaVersion int                `json:"schema_version,omitempty"`
	Time          time.Time          `json:"time,omitempty"`
	Source        Source             `json:"source,omitempty"`
	Scope         Scope              `json:"scope,omitempty"`
	Attributes    map[string]string  `json:"attributes,omitempty"`
	Sensitivity   policy.Sensitivity `json:"sensitivity,omitempty"`
	CorrelationID string             `json:"correlation_id,omitempty"`
	CausationID   string             `json:"causation_id,omitempty"`
	Payload       Event              `json:"payload,omitempty"`
}
