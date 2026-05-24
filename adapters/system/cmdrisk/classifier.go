package cmdrisk

import (
	"path/filepath"
	"strings"

	codewandlercmdrisk "github.com/codewandler/cmdrisk"
	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
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

// Classify evaluates typed operation intent against cmdrisk.
func (c Classifier) Classify(ctx operation.Context, spec operation.Spec, intents operation.IntentSet) (operationruntime.CommandRisk, error) {
	operations := c.intentOperations(intents)
	if len(operations) == 0 {
		return c.declaredRisk(ctx, spec), nil
	}
	if command, ok := processOnlyCommand(intents); ok {
		if strings.TrimSpace(command) == "" {
			return operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "empty command"}, nil
		}
		if len(command) > c.maxCommandLength {
			return operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "command is too large to assess"}, nil
		}
		assessment, err := c.analyzer.Assess(ctx, codewandlercmdrisk.Request{
			Command: command,
			Context: c.base,
		})
		if err != nil {
			return operationruntime.CommandRisk{}, err
		}
		return c.result(ctx, spec, assessment, false), nil
	}
	for _, op := range operations {
		if op.Category == "command" && len(op.Target) > c.maxCommandLength {
			return operationruntime.CommandRisk{Level: operation.RiskHigh, Reason: "command is too large to assess"}, nil
		}
	}
	assessment, err := c.analyzer.AssessIntent(ctx, codewandlercmdrisk.IntentRequest{
		Context:    c.base,
		Operations: operations,
	})
	if err != nil {
		return operationruntime.CommandRisk{}, err
	}
	return c.result(ctx, spec, assessment, networkOnly(intents) && c.networkApprovalAsMedium), nil
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

func (c Classifier) result(ctx operation.Context, spec operation.Spec, assessment codewandlercmdrisk.Assessment, structuredNetwork bool) operationruntime.CommandRisk {
	level, requiresApproval := riskFromAssessment(spec.Semantics.Risk, assessment, structuredNetwork && c.networkApprovalAsMedium)
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
	return operationruntime.CommandRisk{Level: level, Reason: reason, RequiresApproval: requiresApproval}
}

func riskFromAssessment(declared operation.RiskLevel, assessment codewandlercmdrisk.Assessment, approvalAsMedium bool) (operation.RiskLevel, bool) {
	var level operation.RiskLevel
	requiresApproval := false
	switch assessment.Decision.Action {
	case codewandlercmdrisk.ActionReject, codewandlercmdrisk.ActionError:
		level = operation.RiskCritical
	case codewandlercmdrisk.ActionRequiresApproval:
		if approvalAsMedium {
			level = operation.RiskMedium
		} else {
			level = operation.RiskHigh
			requiresApproval = true
		}
	case codewandlercmdrisk.ActionAllow:
		level = riskFromDimensions(assessment.RiskDimensions)
	default:
		level = operation.RiskHigh
	}
	return maxRisk(declared, level), requiresApproval
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

func (c Classifier) intentOperations(intents operation.IntentSet) []codewandlercmdrisk.IntentOperation {
	out := make([]codewandlercmdrisk.IntentOperation, 0, len(intents.Operations))
	for _, intent := range intents.Operations {
		converted, ok := c.intentOperation(intent)
		if ok {
			out = append(out, converted)
		}
	}
	return out
}

func (c Classifier) intentOperation(intent operation.IntentOperation) (codewandlercmdrisk.IntentOperation, bool) {
	target, category, ok := c.intentTarget(intent.Target)
	if !ok || strings.TrimSpace(target) == "" {
		return codewandlercmdrisk.IntentOperation{}, false
	}
	return codewandlercmdrisk.IntentOperation{
		Behavior: string(intent.Behavior),
		Target:   target,
		Role:     string(intent.Role),
		Category: category,
		Certain:  intent.Certainty != operation.IntentPotential,
	}, true
}

func (c Classifier) intentTarget(target operation.IntentTarget) (string, string, bool) {
	switch typed := target.(type) {
	case operation.PathTarget:
		return pathTarget(c.base.WorkingDirectory, typed.Path), "path", true
	case operation.URLTarget:
		return string(typed.URL), "url", true
	case operation.ProcessTarget:
		return processTarget(typed), "command", true
	case operation.BrowserTarget:
		if typed.URL != "" {
			return string(typed.URL), "url", true
		}
		if typed.SessionID != "" {
			return string(typed.SessionID), "session", true
		}
		return "", "", false
	default:
		return "", "", false
	}
}

func pathTarget(root string, path operation.Path) string {
	text := strings.TrimSpace(string(path))
	if root == "" || filepath.IsAbs(text) {
		if text == "" {
			return root
		}
		return text
	}
	if text == "" || text == "." {
		return root
	}
	return filepath.Join(root, text)
}

func processTarget(target operation.ProcessTarget) string {
	command := string(target.Command)
	args := make([]string, 0, len(target.Args))
	for _, arg := range target.Args {
		args = append(args, string(arg))
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(command))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func processOnlyCommand(intents operation.IntentSet) (string, bool) {
	if len(intents.Operations) != 1 || intents.Operations[0].Behavior != operation.IntentCommandExecution {
		return "", false
	}
	target, ok := intents.Operations[0].Target.(operation.ProcessTarget)
	if !ok {
		return "", false
	}
	return processTarget(target), true
}

func networkOnly(intents operation.IntentSet) bool {
	if len(intents.Operations) == 0 {
		return false
	}
	for _, intent := range intents.Operations {
		if intent.Behavior != operation.IntentNetworkFetch {
			return false
		}
	}
	return true
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
