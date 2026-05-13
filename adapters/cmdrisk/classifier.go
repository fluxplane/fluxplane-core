package cmdrisk

import (
	"strings"

	codewandlercmdrisk "github.com/codewandler/cmdrisk"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const (
	defaultMaxCommandLength = 16 * 1024

	// EventAssessedName is emitted after cmdrisk evaluates an operation.
	EventAssessedName event.Name = "cmdrisk.assessed"
)

// Config configures a cmdrisk-backed operation risk classifier.
type Config struct {
	Analyzer *codewandlercmdrisk.Analyzer

	Environment             string
	User                    string
	WorkingDirectory        string
	WorkspacePathPrefixes   []string
	SensitivePathPrefixes   []string
	SecretPathPrefixes      []string
	TrustedSourceHosts      []string
	TrustedURLDomains       []string
	Sandboxed               bool
	Disposable              bool
	Interactive             bool
	CommandOrigin           string
	MaxCommandLength        int
	NetworkApprovalAsMedium bool
}

// Classifier implements runtime operation command-risk classification.
type Classifier struct {
	analyzer                *codewandlercmdrisk.Analyzer
	base                    codewandlercmdrisk.Context
	maxCommandLength        int
	networkApprovalAsMedium bool
}

// Assessed is the runtime event emitted for cmdrisk decisions.
type Assessed struct {
	Operation      operation.Ref                      `json:"operation"`
	Level          operation.RiskLevel                `json:"level"`
	Action         string                             `json:"action"`
	Rationale      string                             `json:"rationale,omitempty"`
	Reasons        []string                           `json:"reasons,omitempty"`
	Behaviors      []string                           `json:"behaviors,omitempty"`
	Targets        []codewandlercmdrisk.Target        `json:"targets,omitempty"`
	RiskDimensions []codewandlercmdrisk.RiskDimension `json:"risk_dimensions,omitempty"`
}

// EventName returns the typed event name.
func (Assessed) EventName() event.Name { return EventAssessedName }

// New constructs a cmdrisk classifier.
func New(cfg Config) Classifier {
	analyzer := cfg.Analyzer
	if analyzer == nil {
		analyzer = codewandlercmdrisk.New(codewandlercmdrisk.Config{})
	}
	maxCommandLength := cfg.MaxCommandLength
	if maxCommandLength <= 0 {
		maxCommandLength = defaultMaxCommandLength
	}
	return Classifier{
		analyzer: analyzer,
		base: codewandlercmdrisk.Context{
			Environment:      codewandlercmdrisk.Environment(defaultString(cfg.Environment, string(codewandlercmdrisk.EnvironmentDeveloperWorkstation))),
			User:             cfg.User,
			Sandboxed:        cfg.Sandboxed,
			Disposable:       cfg.Disposable,
			Interactive:      cfg.Interactive,
			WorkingDirectory: cfg.WorkingDirectory,
			Asset: codewandlercmdrisk.AssetContext{
				SensitivePathPrefixes: append([]string(nil), cfg.SensitivePathPrefixes...),
				SecretPathPrefixes:    append([]string(nil), cfg.SecretPathPrefixes...),
				WorkspacePathPrefixes: append([]string(nil), cfg.WorkspacePathPrefixes...),
			},
			Trust: codewandlercmdrisk.TrustContext{
				CommandOrigin:      codewandlercmdrisk.CommandOrigin(defaultString(cfg.CommandOrigin, string(codewandlercmdrisk.CommandOriginMachineGenerated))),
				TrustedSourceHosts: append([]string(nil), cfg.TrustedSourceHosts...),
				TrustedURLDomains:  append([]string(nil), cfg.TrustedURLDomains...),
			},
		},
		maxCommandLength:        maxCommandLength,
		networkApprovalAsMedium: cfg.NetworkApprovalAsMedium,
	}
}

// Classify evaluates operation input against cmdrisk.
func (c Classifier) Classify(ctx operation.Context, spec operation.Spec, input operation.Value) (operationruntime.CommandRisk, error) {
	switch {
	case spec.Semantics.Effects.Has(operation.EffectProcess):
		if hasCommandIntent(spec, input) {
			return c.classifyCommand(ctx, spec, input)
		}
		return c.declaredRisk(ctx, spec), nil
	case spec.Semantics.Effects.Has(operation.EffectNetwork):
		if hasNetworkIntent(input) {
			return c.classifyNetworkIntent(ctx, spec, input)
		}
		return c.declaredRisk(ctx, spec), nil
	default:
		return operationruntime.CommandRisk{Level: spec.Semantics.Risk}, nil
	}
}

func (c Classifier) declaredRisk(ctx operation.Context, spec operation.Spec) operationruntime.CommandRisk {
	const reason = "declared operation risk"
	ctx.Events().Emit(Assessed{
		Operation: spec.Ref,
		Level:     spec.Semantics.Risk,
		Action:    "declared_risk",
		Rationale: reason,
	})
	return operationruntime.CommandRisk{Level: spec.Semantics.Risk, Reason: reason}
}

func (c Classifier) classifyCommand(ctx operation.Context, spec operation.Spec, input operation.Value) (operationruntime.CommandRisk, error) {
	command := commandString(spec, input)
	if strings.TrimSpace(command) == "" {
		return operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "empty command"}, nil
	}
	if len(command) > c.maxCommandLength {
		return operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "command is too large to assess"}, nil
	}
	assessment, err := c.analyzer.Assess(ctx, codewandlercmdrisk.Request{
		Command: command,
		Context: c.contextFor(input),
	})
	if err != nil {
		return operationruntime.CommandRisk{}, err
	}
	return c.result(ctx, spec, assessment, false), nil
}

func (c Classifier) classifyNetworkIntent(ctx operation.Context, spec operation.Spec, input operation.Value) (operationruntime.CommandRisk, error) {
	target := urlString(input)
	if strings.TrimSpace(target) == "" {
		return operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "empty network target"}, nil
	}
	assessment, err := c.analyzer.AssessIntent(ctx, codewandlercmdrisk.IntentRequest{
		Context: c.contextFor(input),
		Operations: []codewandlercmdrisk.IntentOperation{{
			Behavior: "network_fetch",
			Target:   target,
			Role:     "network_target",
			Category: "url",
			Certain:  true,
		}},
	})
	if err != nil {
		return operationruntime.CommandRisk{}, err
	}
	return c.result(ctx, spec, assessment, true), nil
}

func (c Classifier) result(ctx operation.Context, spec operation.Spec, assessment codewandlercmdrisk.Assessment, structuredNetwork bool) operationruntime.CommandRisk {
	level := riskFromAssessment(spec.Semantics.Risk, assessment, structuredNetwork && c.networkApprovalAsMedium)
	reason := assessment.Decision.Rationale
	if reason == "" {
		reason = string(assessment.Decision.Action)
	}
	ctx.Events().Emit(Assessed{
		Operation:      spec.Ref,
		Level:          level,
		Action:         string(assessment.Decision.Action),
		Rationale:      assessment.Decision.Rationale,
		Reasons:        append([]string(nil), assessment.Decision.Reasons...),
		Behaviors:      append([]string(nil), assessment.Behaviors...),
		Targets:        append([]codewandlercmdrisk.Target(nil), assessment.Targets...),
		RiskDimensions: append([]codewandlercmdrisk.RiskDimension(nil), assessment.RiskDimensions...),
	})
	return operationruntime.CommandRisk{Level: level, Reason: reason}
}

func (c Classifier) contextFor(input operation.Value) codewandlercmdrisk.Context {
	out := c.base
	if wd := stringField(input, "workdir"); wd != "" {
		out.WorkingDirectory = wd
	}
	return out
}

func riskFromAssessment(declared operation.RiskLevel, assessment codewandlercmdrisk.Assessment, approvalAsMedium bool) operation.RiskLevel {
	var level operation.RiskLevel
	switch assessment.Decision.Action {
	case codewandlercmdrisk.ActionReject, codewandlercmdrisk.ActionError:
		level = operation.RiskCritical
	case codewandlercmdrisk.ActionRequiresApproval:
		if approvalAsMedium {
			level = operation.RiskMedium
		} else {
			level = operation.RiskHigh
		}
	case codewandlercmdrisk.ActionAllow:
		level = riskFromDimensions(assessment.RiskDimensions)
	default:
		level = operation.RiskHigh
	}
	return maxRisk(declared, level)
}

func riskFromDimensions(dimensions []codewandlercmdrisk.RiskDimension) operation.RiskLevel {
	maxSeverity := 0
	for _, dimension := range dimensions {
		if dimension.Severity > maxSeverity {
			maxSeverity = dimension.Severity
		}
	}
	switch {
	case maxSeverity >= 4:
		return operation.RiskCritical
	case maxSeverity >= 3:
		return operation.RiskHigh
	case maxSeverity >= 2:
		return operation.RiskMedium
	default:
		return operation.RiskLow
	}
}

func maxRisk(a, b operation.RiskLevel) operation.RiskLevel {
	if riskRank(a) >= riskRank(b) {
		return a
	}
	return b
}

func riskRank(risk operation.RiskLevel) int {
	switch risk {
	case operation.RiskCritical:
		return 4
	case operation.RiskHigh:
		return 3
	case operation.RiskMedium:
		return 2
	case operation.RiskLow:
		return 1
	default:
		return 0
	}
}

func commandString(spec operation.Spec, input operation.Value) string {
	command := stringField(input, "command")
	args := stringSliceField(input, "args")
	if len(args) == 0 {
		args = stringSliceField(input, "command")
		if len(args) > 0 {
			command = args[0]
			args = args[1:]
		}
	}
	if len(args) == 0 {
		if command == "" {
			switch spec.Ref.Name {
			case "git_status":
				return "git status --short --branch"
			case "git_diff":
				return "git diff"
			case "git_add":
				return "git add"
			case "git_commit":
				return "git -c core.hooksPath=/dev/null commit --no-verify --no-gpg-sign"
			case "code_execute":
				return "docker run --rm --network none <scratch-code>"
			}
		}
		return command
	}
	quoted := make([]string, 0, len(args)+1)
	quoted = append(quoted, shellQuote(command))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func hasCommandIntent(spec operation.Spec, input operation.Value) bool {
	switch spec.Ref.Name {
	case "shell_exec", "process_start", "git_status", "git_diff", "git_add", "git_commit", "code_execute":
		return true
	}
	if stringField(input, "command") != "" {
		return true
	}
	return len(stringSliceField(input, "command")) > 0 || len(stringSliceField(input, "args")) > 0
}

func hasNetworkIntent(input operation.Value) bool {
	return strings.TrimSpace(urlString(input)) != ""
}

func urlString(input operation.Value) string {
	if value, ok := input.(string); ok {
		return value
	}
	return stringField(input, "url")
}

func stringField(input operation.Value, key string) string {
	switch value := input.(type) {
	case map[string]any:
		text, _ := value[key].(string)
		return strings.TrimSpace(text)
	case string:
		if key == "command" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringSliceField(input operation.Value, key string) []string {
	value, ok := input.(map[string]any)
	if !ok {
		return nil
	}
	switch raw := value[key].(type) {
	case []string:
		return append([]string(nil), raw...)
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			text, ok := item.(string)
			if ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return r != '_' && r != '-' && r != '.' && r != '/' && r != ':' && r != '=' &&
			(r < '0' || r > '9') &&
			(r < 'A' || r > 'Z') &&
			(r < 'a' || r > 'z')
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

var _ operationruntime.CommandRiskClassifier = Classifier{}
