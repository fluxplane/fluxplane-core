package usage

import "github.com/fluxplane/fluxplane-core/core/event"

const (
	// EventRecordedName is emitted when a runtime component observes resource
	// consumption.
	EventRecordedName event.Name = "usage.recorded"
)

// SubjectKind classifies the consumed resource domain.
type SubjectKind string

const (
	SubjectLLM     SubjectKind = "llm"
	SubjectNetwork SubjectKind = "network"
	SubjectFile    SubjectKind = "file"
	SubjectProcess SubjectKind = "process"
	SubjectRuntime SubjectKind = "runtime"
	SubjectMoney   SubjectKind = "money"
)

// Subject identifies the resource being metered without binding it to an
// implementation.
type Subject struct {
	Kind       SubjectKind       `json:"kind"`
	Provider   string            `json:"provider,omitempty"`
	Name       string            `json:"name,omitempty"`
	ID         string            `json:"id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Unit names the unit used by a measurement.
type Unit string

const (
	UnitToken       Unit = "token"
	UnitByte        Unit = "byte"
	UnitRequest     Unit = "request"
	UnitMillisecond Unit = "millisecond"
	UnitCount       Unit = "count"
	UnitCurrency    Unit = "currency"
)

// Direction describes the flow or role of consumed usage.
type Direction string

const (
	DirectionInput    Direction = "input"
	DirectionOutput   Direction = "output"
	DirectionCached   Direction = "cached"
	DirectionRead     Direction = "read"
	DirectionWrite    Direction = "write"
	DirectionUpload   Direction = "upload"
	DirectionDownload Direction = "download"
)

// MetricName identifies a concrete meter such as "llm.input_tokens" or
// "network.download_bytes".
type MetricName string

const (
	MetricLLMInputTokens     MetricName = "llm.input_tokens"
	MetricLLMCachedTokens    MetricName = "llm.cached_input_tokens"
	MetricLLMOutputTokens    MetricName = "llm.output_tokens"
	MetricLLMReasoningTokens MetricName = "llm.reasoning_tokens"
	MetricLLMTotalTokens     MetricName = "llm.total_tokens"
	MetricNetworkBytes       MetricName = "network.bytes"
	MetricFileBytes          MetricName = "file.bytes"
	MetricRequests           MetricName = "requests"
	MetricWallTime           MetricName = "wall_time"
	MetricCost               MetricName = "cost"
)

// Measurement records one quantity for one metric.
type Measurement struct {
	Metric     MetricName        `json:"metric"`
	Quantity   float64           `json:"quantity"`
	Unit       Unit              `json:"unit"`
	Direction  Direction         `json:"direction,omitempty"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
}

// Recorded is the generic usage event emitted by runtime and adapter
// boundaries. Cost evaluators and budget guards consume this event instead of
// depending on provider-native response shapes.
type Recorded struct {
	Source       string        `json:"source,omitempty"`
	Subject      Subject       `json:"subject"`
	Measurements []Measurement `json:"measurements,omitempty"`
}

// EventName returns the typed event name.
func (Recorded) EventName() event.Name { return EventRecordedName }

// Empty reports whether the event carries no measurements.
func (r Recorded) Empty() bool {
	return len(r.Measurements) == 0
}
