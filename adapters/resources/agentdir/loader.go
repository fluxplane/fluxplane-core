package agentdir

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/skill"
	"github.com/fluxplane/fluxplane-core/core/workflow"
	invjsonschema "github.com/invopop/jsonschema"
	santhoshjsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

const (
	dirNameAgents    = "agents"
	dirNameCommands  = "commands"
	dirNameSkills    = "skills"
	dirNameWorkflows = "workflows"
)

// LoadDir loads the engineer subset of a .agents resource directory.
func LoadDir(ctx context.Context, dir string) (resource.ContributionBundle, error) {
	return LoadDirWithOptions(ctx, dir, LoadOptions{})
}

// LoadOptions controls agentdir resource loading behavior.
type LoadOptions struct {
	ContinueOnError bool
	Events          event.Sink
}

// LoadDirWithOptions loads a .agents-compatible resource directory.
func LoadDirWithOptions(ctx context.Context, dir string, opts LoadOptions) (resource.ContributionBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return resource.ContributionBundle{}, err
	}
	root, err := resolveRoot(dir)
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	bundle := resource.ContributionBundle{
		Source: resource.SourceRef{
			ID:        "agentdir:" + filepath.Clean(root),
			Ecosystem: "agentdir",
			Scope:     resource.ScopeProject,
			Location:  filepath.Clean(root),
			Trust: policy.Trust{
				Kind:  policy.TrustSource,
				Level: policy.TrustVerified,
			},
		},
	}
	loaders := []func(context.Context, string, *resource.ContributionBundle, LoadOptions) error{
		loadAgents,
		loadCommands,
		loadWorkflows,
		loadSkills,
	}
	for _, load := range loaders {
		if err := ctx.Err(); err != nil {
			return resource.ContributionBundle{}, err
		}
		if err := load(ctx, root, &bundle, opts); err != nil {
			return resource.ContributionBundle{}, err
		}
	}
	return bundle, nil
}

// LoadFS loads a .agents-compatible tree from fsys rooted at root. It is used
// by embedded first-party apps and tests.
func LoadFS(ctx context.Context, fsys fs.FS, root string, source resource.SourceRef) (resource.ContributionBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if fsys == nil {
		return resource.ContributionBundle{}, fmt.Errorf("agentdir: fs is nil")
	}
	root = cleanFSRoot(root)
	if source.ID == "" {
		source = resource.SourceRef{
			ID:        "agentdir:" + root,
			Ecosystem: "agentdir",
			Scope:     resource.ScopeEmbedded,
			Location:  root,
			Trust: policy.Trust{
				Kind:  policy.TrustSource,
				Level: policy.TrustVerified,
			},
		}
	}
	bundle := resource.ContributionBundle{Source: source}
	loaders := []func(context.Context, fs.FS, string, *resource.ContributionBundle, LoadOptions) error{
		loadAgentsFS,
		loadCommandsFS,
		loadWorkflowsFS,
		loadSkillsFS,
	}
	for _, load := range loaders {
		if err := ctx.Err(); err != nil {
			return resource.ContributionBundle{}, err
		}
		if err := load(ctx, fsys, root, &bundle, LoadOptions{}); err != nil {
			return resource.ContributionBundle{}, err
		}
	}
	return bundle, nil
}

func resolveRoot(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("agentdir: directory is empty")
	}
	clean := filepath.Clean(dir)
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("agentdir: stat directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("agentdir: %s is not a directory", clean)
	}
	if filepath.Base(clean) == ".agents" {
		return clean, nil
	}
	candidate := filepath.Join(clean, ".agents")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate, nil
	}
	return clean, nil
}

func loadAgents(ctx context.Context, root string, bundle *resource.ContributionBundle, opts LoadOptions) error {
	files, err := sortedGlob(filepath.Join(root, dirNameAgents, "*.md"))
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		spec, err := DecodeAgentFile(path)
		if err != nil {
			if addLoadError(bundle, opts, "agent", path, err) {
				continue
			}
			return err
		}
		bundle.Agents = append(bundle.Agents, spec)
	}
	return nil
}

func loadCommands(ctx context.Context, root string, bundle *resource.ContributionBundle, opts LoadOptions) error {
	mdFiles, err := sortedGlob(filepath.Join(root, dirNameCommands, "*.md"))
	if err != nil {
		return err
	}
	for _, path := range mdFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		spec, err := DecodePromptCommandFile(path)
		if err != nil {
			if addLoadError(bundle, opts, "command", path, err) {
				continue
			}
			return err
		}
		bundle.Commands = append(bundle.Commands, spec)
	}
	yamlFiles, err := sortedYAMLFiles(filepath.Join(root, dirNameCommands))
	if err != nil {
		return err
	}
	for _, path := range yamlFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		spec, err := DecodeCommandFile(path)
		if err != nil {
			if addLoadError(bundle, opts, "command", path, err) {
				continue
			}
			return err
		}
		bundle.Commands = append(bundle.Commands, spec)
	}
	return nil
}

func loadWorkflows(ctx context.Context, root string, bundle *resource.ContributionBundle, opts LoadOptions) error {
	files, err := sortedYAMLFiles(filepath.Join(root, dirNameWorkflows))
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		spec, err := DecodeWorkflowFile(path)
		if err != nil {
			if addLoadError(bundle, opts, "workflow", path, err) {
				continue
			}
			return err
		}
		bundle.Workflows = append(bundle.Workflows, spec)
	}
	return nil
}

func loadSkills(ctx context.Context, root string, bundle *resource.ContributionBundle, opts LoadOptions) error {
	files, err := sortedGlob(filepath.Join(root, dirNameSkills, "*", "SKILL.md"))
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		spec, err := DecodeSkillFile(path)
		if err != nil {
			if addLoadError(bundle, opts, "skill", path, err) {
				continue
			}
			return err
		}
		refs, err := loadSkillReferencesDir(filepath.Dir(path))
		if err != nil {
			if addLoadError(bundle, opts, "skill_reference", path, err) {
				continue
			}
			return err
		}
		spec.References = refs
		bundle.Skills = append(bundle.Skills, spec)
	}
	return nil
}

func addLoadError(bundle *resource.ContributionBundle, opts LoadOptions, kind, itemPath string, err error) bool {
	if !opts.ContinueOnError || bundle == nil || err == nil {
		return false
	}
	source := bundle.Source
	if strings.TrimSpace(itemPath) != "" {
		source.Location = itemPath
	}
	loadErr := resource.LoadError{
		Source:    source,
		Kind:      kind,
		Path:      itemPath,
		Severity:  resource.SeverityError,
		Message:   err.Error(),
		Continued: true,
	}
	if opts.Events != nil {
		opts.Events.Emit(loadErr)
	}
	bundle.Diagnostics = append(bundle.Diagnostics, resource.Diagnostic{
		Severity: loadErr.Severity,
		Source:   loadErr.Source,
		Message:  loadErr.Message,
	})
	return true
}

func loadAgentsFS(ctx context.Context, fsys fs.FS, root string, bundle *resource.ContributionBundle, opts LoadOptions) error {
	files, err := sortedFSGlob(fsys, path.Join(root, dirNameAgents, "*.md"))
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("agentdir: read agent %s: %w", file, err)
		}
		spec, err := DecodeAgent(path.Base(file), data)
		if err != nil {
			return fmt.Errorf("agentdir: decode agent %s: %w", file, err)
		}
		bundle.Agents = append(bundle.Agents, spec)
	}
	return nil
}

func loadCommandsFS(ctx context.Context, fsys fs.FS, root string, bundle *resource.ContributionBundle, opts LoadOptions) error {
	mdFiles, err := sortedFSGlob(fsys, path.Join(root, dirNameCommands, "*.md"))
	if err != nil {
		return err
	}
	for _, file := range mdFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("agentdir: read prompt command %s: %w", file, err)
		}
		spec, err := DecodePromptCommand(path.Base(file), data)
		if err != nil {
			return fmt.Errorf("agentdir: decode prompt command %s: %w", file, err)
		}
		bundle.Commands = append(bundle.Commands, spec)
	}
	yamlFiles, err := sortedFSYAMLFiles(fsys, path.Join(root, dirNameCommands))
	if err != nil {
		return err
	}
	for _, file := range yamlFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("agentdir: read command %s: %w", file, err)
		}
		spec, err := DecodeCommand(path.Base(file), data)
		if err != nil {
			return fmt.Errorf("agentdir: decode command %s: %w", file, err)
		}
		bundle.Commands = append(bundle.Commands, spec)
	}
	return nil
}

func loadWorkflowsFS(ctx context.Context, fsys fs.FS, root string, bundle *resource.ContributionBundle, opts LoadOptions) error {
	files, err := sortedFSYAMLFiles(fsys, path.Join(root, dirNameWorkflows))
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("agentdir: read workflow %s: %w", file, err)
		}
		spec, err := DecodeWorkflow(path.Base(file), data)
		if err != nil {
			return fmt.Errorf("agentdir: decode workflow %s: %w", file, err)
		}
		bundle.Workflows = append(bundle.Workflows, spec)
	}
	return nil
}

func loadSkillsFS(ctx context.Context, fsys fs.FS, root string, bundle *resource.ContributionBundle, opts LoadOptions) error {
	files, err := sortedFSGlob(fsys, path.Join(root, dirNameSkills, "*", "SKILL.md"))
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("agentdir: read skill %s: %w", file, err)
		}
		spec, err := DecodeSkill(path.Base(path.Dir(file)), "fs://"+file, data)
		if err != nil {
			return fmt.Errorf("agentdir: decode skill %s: %w", file, err)
		}
		refs, err := loadSkillReferencesFS(fsys, path.Dir(file))
		if err != nil {
			return err
		}
		spec.References = refs
		bundle.Skills = append(bundle.Skills, spec)
	}
	return nil
}

// DecodeAgentFile decodes one markdown agent resource.
func DecodeAgentFile(path string) (agent.Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return agent.Spec{}, fmt.Errorf("agentdir: read agent %s: %w", path, err)
	}
	spec, err := DecodeAgent(filepath.Base(path), data)
	if err != nil {
		return agent.Spec{}, fmt.Errorf("agentdir: decode agent %s: %w", path, err)
	}
	return spec, nil
}

// DecodeAgent decodes one markdown agent resource.
func DecodeAgent(filename string, data []byte) (agent.Spec, error) {
	var fm agentFrontmatter
	body, err := decodeFrontmatter(data, &fm)
	if err != nil {
		return agent.Spec{}, err
	}
	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = filenameStem(filename)
	}
	spec := agent.Spec{
		Name:        agent.Name(name),
		Description: strings.TrimSpace(fm.Description),
		System:      strings.TrimSpace(body),
		Inference: agent.InferenceSpec{
			Model:           strings.TrimSpace(fm.Model),
			MaxOutputTokens: fm.MaxTokens,
			Temperature:     fm.Temperature,
			Thinking:        strings.TrimSpace(fm.Thinking),
			ReasoningEffort: strings.TrimSpace(fm.Effort),
		},
		Turns: agent.TurnPolicy{
			MaxSteps: fm.Turns.MaxSteps,
			Continuation: agent.ContinuationPolicy{
				MaxContinuations: fm.Turns.Continuation.MaxContinuations,
				ContextPolicy:    strings.TrimSpace(fm.Turns.Continuation.ContextPolicy),
				StopCondition:    fm.Turns.Continuation.StopCondition.agentSpec(),
			},
		},
		Tools:       toToolRefs(frontmatterStrings(fm.Tools)),
		Commands:    toCommandRefs(frontmatterStrings(fm.Commands)),
		Skills:      toSkillRefs(frontmatterStrings(fm.Skills)),
		Annotations: stringListAnnotations("capabilities", frontmatterStrings(fm.Capabilities)),
	}
	if err := spec.Validate(); err != nil {
		return agent.Spec{}, err
	}
	return spec, nil
}

// DecodePromptCommandFile decodes one markdown prompt command resource.
func DecodePromptCommandFile(path string) (command.Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return command.Spec{}, fmt.Errorf("agentdir: read prompt command %s: %w", path, err)
	}
	spec, err := DecodePromptCommand(filepath.Base(path), data)
	if err != nil {
		return command.Spec{}, fmt.Errorf("agentdir: decode prompt command %s: %w", path, err)
	}
	return spec, nil
}

// DecodePromptCommand decodes one markdown prompt command resource.
func DecodePromptCommand(filename string, data []byte) (command.Spec, error) {
	var fm promptCommandFrontmatter
	body, err := decodeFrontmatter(data, &fm)
	if err != nil {
		return command.Spec{}, err
	}
	annotations := map[string]string{}
	if hint := strings.TrimSpace(fm.ArgumentHint); hint != "" {
		annotations["argument_hint"] = hint
	}
	spec := command.Spec{
		Path:        command.Path{filenameStem(filename)},
		Description: strings.TrimSpace(fm.Description),
		Target: invocation.Target{
			Kind:   invocation.TargetPrompt,
			Prompt: strings.TrimSpace(body),
		},
		Annotations: annotationsOrNil(annotations),
	}
	if err := validateCommand(spec); err != nil {
		return command.Spec{}, err
	}
	return spec, nil
}

// DecodeCommandFile decodes one YAML command resource.
func DecodeCommandFile(path string) (command.Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return command.Spec{}, fmt.Errorf("agentdir: read command %s: %w", path, err)
	}
	spec, err := DecodeCommand(filepath.Base(path), data)
	if err != nil {
		return command.Spec{}, fmt.Errorf("agentdir: decode command %s: %w", path, err)
	}
	return spec, nil
}

// DecodeCommand decodes one YAML command resource.
func DecodeCommand(filename string, data []byte) (command.Spec, error) {
	var raw yamlCommand
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return command.Spec{}, err
	}
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = filenameStem(filename)
	}
	annotations := map[string]string{}
	var input operation.Type
	if raw.InputSchema != nil {
		schema, err := json.Marshal(raw.InputSchema)
		if err != nil {
			return command.Spec{}, fmt.Errorf("marshal input_schema: %w", err)
		}
		input.Schema = operation.Schema{Format: "json-schema", Data: schema}
		annotations["input_schema"] = string(schema)
	}
	if raw.Policy.AgentCallable != nil {
		annotations["policy.agent_callable"] = fmt.Sprintf("%t", *raw.Policy.AgentCallable)
	}
	target, err := raw.Target.invocationTarget()
	if err != nil {
		return command.Spec{}, err
	}
	spec := command.Spec{
		Path:        command.Path{name},
		Description: strings.TrimSpace(raw.Description),
		Target:      target,
		Input:       input,
		Annotations: annotationsOrNil(annotations),
	}
	if raw.Policy.AgentCallable != nil && *raw.Policy.AgentCallable {
		spec.Policy.AllowedCallers = []policy.CallerKind{policy.CallerUser, policy.CallerAgent}
	}
	if err := validateCommand(spec); err != nil {
		return command.Spec{}, err
	}
	return spec, nil
}

// DecodeWorkflowFile decodes one YAML workflow resource.
func DecodeWorkflowFile(path string) (workflow.Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return workflow.Spec{}, fmt.Errorf("agentdir: read workflow %s: %w", path, err)
	}
	spec, err := DecodeWorkflow(filepath.Base(path), data)
	if err != nil {
		return workflow.Spec{}, fmt.Errorf("agentdir: decode workflow %s: %w", path, err)
	}
	return spec, nil
}

// DecodeWorkflow decodes one YAML workflow resource.
func DecodeWorkflow(filename string, data []byte) (workflow.Spec, error) {
	var raw yamlWorkflow
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return workflow.Spec{}, err
	}
	if strings.TrimSpace(raw.Name) == "" {
		raw.Name = filenameStem(filename)
	}
	var rawMap map[string]any
	if err := yaml.Unmarshal(data, &rawMap); err != nil {
		return workflow.Spec{}, err
	}
	spec := workflow.Spec{
		Name:        workflow.Name(strings.TrimSpace(raw.Name)),
		Description: strings.TrimSpace(raw.Description),
		Raw:         rawMap,
	}
	for _, rawStep := range raw.Steps {
		kind := workflow.StepKind(strings.TrimSpace(rawStep.Kind))
		step := workflow.Step{
			ID:        workflow.StepID(strings.TrimSpace(rawStep.ID)),
			Kind:      kind,
			DependsOn: toStepIDs(rawStep.DependsOn),
			Input:     rawStep.Input,
			Raw:       rawStep.Raw,
		}
		if agentName := strings.TrimSpace(rawStep.Agent); agentName != "" {
			step.Agent = agent.Ref{Name: agent.Name(agentName)}
			if step.Kind == "" {
				step.Kind = workflow.StepAgent
			}
		}
		if operationName := strings.TrimSpace(rawStep.Operation); operationName != "" {
			step.Operation = operation.Ref{Name: operation.Name(operationName)}
			if step.Kind == "" {
				step.Kind = workflow.StepOperation
			}
		}
		if policy := firstNonEmpty(strings.TrimSpace(rawStep.ErrorPolicy), strings.TrimSpace(rawStep.ErrorPolicySnake)); policy != "" {
			step.ErrorPolicy = workflow.StepErrorPolicy(policy)
		}
		spec.Steps = append(spec.Steps, step)
	}
	if err := spec.Validate(); err != nil {
		return workflow.Spec{}, err
	}
	return spec, nil
}

// DecodeSkillFile decodes one skill resource.
func DecodeSkillFile(path string) (skill.Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return skill.Spec{}, fmt.Errorf("agentdir: read skill %s: %w", path, err)
	}
	spec, err := DecodeSkill(filepath.Base(filepath.Dir(path)), "file://"+filepath.Clean(path), data)
	if err != nil {
		return skill.Spec{}, fmt.Errorf("agentdir: decode skill %s: %w", path, err)
	}
	return spec, nil
}

// DecodeSkill decodes one markdown skill resource.
func DecodeSkill(defaultName, sourceURI string, data []byte) (skill.Spec, error) {
	var fm skillFrontmatter
	body, err := decodeFrontmatterRelaxed(data, &fm)
	if err != nil {
		return skill.Spec{}, err
	}
	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = strings.TrimSpace(defaultName)
	}
	spec := skill.Spec{
		Name:        skill.Name(name),
		Description: strings.TrimSpace(fm.Description),
		Body:        strings.TrimSpace(body),
		Source: skill.SourceRef{
			URI:  sourceURI,
			Kind: "agentdir",
		},
		Triggers:    skillTriggers(fm),
		Metadata:    skillMetadata(fm),
		Annotations: stringListAnnotations("allowed_tools", frontmatterStrings(fm.AllowedTools)),
	}
	if err := spec.Validate(); err != nil {
		return skill.Spec{}, err
	}
	return spec, nil
}

type agentFrontmatter struct {
	Name         string    `yaml:"name"`
	Description  string    `yaml:"description"`
	Model        string    `yaml:"model"`
	MaxTokens    int       `yaml:"max-tokens"`
	Turns        turnsYAML `yaml:"turns"`
	Temperature  float64   `yaml:"temperature"`
	Thinking     string    `yaml:"thinking"`
	Effort       string    `yaml:"effort"`
	Memory       string    `yaml:"memory"`
	Tools        any       `yaml:"tools"`
	Skills       any       `yaml:"skills"`
	Commands     any       `yaml:"commands"`
	Capabilities any       `yaml:"capabilities"`
}

type turnsYAML struct {
	MaxSteps     int              `yaml:"max-steps"`
	Continuation continuationYAML `yaml:"continuation"`
}

type continuationYAML struct {
	MaxContinuations int               `yaml:"max-continuations"`
	ContextPolicy    string            `yaml:"context-policy"`
	StopCondition    stopConditionYAML `yaml:"stop-condition"`
}

type stopConditionYAML struct {
	Type        string               `yaml:"type"`
	Max         int                  `yaml:"max"`
	Prompt      string               `yaml:"prompt"`
	Session     string               `yaml:"session"`
	Conditions  []*stopConditionYAML `yaml:"conditions"`
	Annotations map[string]string    `yaml:"annotations"`
}

func (s stopConditionYAML) agentSpec() agent.StopConditionSpec {
	out := agent.StopConditionSpec{
		Type:        strings.TrimSpace(s.Type),
		Max:         s.Max,
		Prompt:      strings.TrimSpace(s.Prompt),
		Session:     strings.TrimSpace(s.Session),
		Annotations: cloneStringMap(s.Annotations),
	}
	for _, condition := range s.Conditions {
		if condition == nil {
			continue
		}
		out.Conditions = append(out.Conditions, condition.agentSpec())
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

type promptCommandFrontmatter struct {
	Description  string `yaml:"description"`
	ArgumentHint string `yaml:"argument-hint"`
}

type yamlCommand struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description"`
	Policy      commandPolicy `yaml:"policy"`
	InputSchema any           `yaml:"input_schema"`
	Target      commandTarget `yaml:"target"`
}

type commandPolicy struct {
	AgentCallable *bool `yaml:"agent_callable"`
}

type commandTarget struct {
	Workflow string `yaml:"workflow"`
	Prompt   string `yaml:"prompt"`
	Input    any    `yaml:"input"`
}

func (t commandTarget) invocationTarget() (invocation.Target, error) {
	targets := 0
	if strings.TrimSpace(t.Workflow) != "" {
		targets++
	}
	if strings.TrimSpace(t.Prompt) != "" {
		targets++
	}
	if targets == 0 {
		return invocation.Target{}, fmt.Errorf("command: target is empty")
	}
	if targets > 1 {
		return invocation.Target{}, fmt.Errorf("command: target must specify exactly one of workflow or prompt")
	}
	switch {
	case strings.TrimSpace(t.Workflow) != "":
		return invocation.Target{
			Kind:     invocation.TargetWorkflow,
			Workflow: workflow.Name(strings.TrimSpace(t.Workflow)),
			Input:    t.Input,
		}, nil
	case strings.TrimSpace(t.Prompt) != "":
		return invocation.Target{
			Kind:   invocation.TargetPrompt,
			Prompt: strings.TrimSpace(t.Prompt),
			Input:  t.Input,
		}, nil
	default:
		return invocation.Target{}, nil
	}
}

type yamlWorkflow struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Steps       []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	ID               string         `yaml:"id"`
	Kind             string         `yaml:"kind"`
	Agent            string         `yaml:"agent"`
	Operation        string         `yaml:"operation"`
	Input            any            `yaml:"input"`
	DependsOn        []string       `yaml:"depends-on"`
	ErrorPolicy      string         `yaml:"error-policy"`
	ErrorPolicySnake string         `yaml:"error_policy"`
	Raw              map[string]any `yaml:",inline"`
}

type skillFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	Triggers      any               `yaml:"triggers"`
	AllowedTools  any               `yaml:"allowed-tools"`
	UserInvocable bool              `yaml:"user-invocable"`
	License       string            `yaml:"license"`
	Risk          string            `yaml:"risk"`
	Compatibility string            `yaml:"compatibility"`
	Domain        string            `yaml:"domain"`
	Role          string            `yaml:"role"`
	Metadata      map[string]string `yaml:"metadata"`
}

type referenceFrontmatter struct {
	Trigger  string `yaml:"trigger"`
	Triggers any    `yaml:"triggers"`
}

func decodeFrontmatterRelaxed[T any](data []byte, out *T) (string, error) {
	return decodeFrontmatterWithOptions(data, out, true)
}

func decodeFrontmatter[T any](data []byte, out *T) (string, error) {
	return decodeFrontmatterWithOptions(data, out, false)
}

func decodeFrontmatterWithOptions[T any](data []byte, out *T, allowUnknown bool) (string, error) {
	text := strings.TrimPrefix(string(data), "\ufeff")
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return text, nil
	}
	lines := strings.SplitAfter(text, "\n")
	var fm strings.Builder
	var body strings.Builder
	end := false
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			end = true
			for _, rest := range lines[i+1:] {
				body.WriteString(rest)
			}
			break
		}
		fm.WriteString(line)
	}
	if !end {
		return "", fmt.Errorf("frontmatter is not closed")
	}
	frontmatter := []byte(fm.String())
	if err := validateYAMLBytes[T](frontmatter, allowUnknown); err != nil {
		return "", fmt.Errorf("frontmatter schema: %w", err)
	}
	if err := yaml.Unmarshal(frontmatter, out); err != nil {
		return "", err
	}
	return body.String(), nil
}

func validateYAMLBytes[T any](data []byte, allowUnknown bool) error {
	var decoded any
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		return err
	}
	jsonData, err := json.Marshal(decoded)
	if err != nil {
		return fmt.Errorf("marshal JSON value: %w", err)
	}
	var value any
	if err := json.Unmarshal(jsonData, &value); err != nil {
		return fmt.Errorf("unmarshal JSON value: %w", err)
	}
	schemaData, err := schemaDataFor[T](allowUnknown)
	if err != nil {
		return err
	}
	var schemaValue any
	if err := json.Unmarshal(schemaData, &schemaValue); err != nil {
		return fmt.Errorf("decode schema resource: %w", err)
	}
	compiler := santhoshjsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaValue); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
}

func schemaDataFor[T any](allowUnknown bool) ([]byte, error) {
	typ := reflect.TypeOf((*T)(nil)).Elem()
	reflector := invjsonschema.Reflector{
		DoNotReference:             false,
		ExpandedStruct:             true,
		AllowAdditionalProperties:  allowUnknown,
		RequiredFromJSONSchemaTags: true,
		FieldNameTag:               "yaml",
	}
	ptr := reflect.New(typ)
	if typ.Kind() == reflect.Ptr {
		ptr = reflect.New(typ.Elem())
	}
	schema := reflector.Reflect(ptr.Interface())
	if schema == nil {
		return nil, fmt.Errorf("schema is nil")
	}
	schema.Version = invjsonschema.Version
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return data, nil
}

func sortedGlob(pattern string) ([]string, error) {
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func sortedYAMLFiles(dir string) ([]string, error) {
	yml, err := sortedGlob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return nil, err
	}
	yamlFiles, err := sortedGlob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	files := append(yml, yamlFiles...)
	sort.Strings(files)
	return files, nil
}

func sortedFSGlob(fsys fs.FS, pattern string) ([]string, error) {
	files, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func sortedFSYAMLFiles(fsys fs.FS, dir string) ([]string, error) {
	yml, err := sortedFSGlob(fsys, path.Join(dir, "*.yml"))
	if err != nil {
		return nil, err
	}
	yamlFiles, err := sortedFSGlob(fsys, path.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	files := append(yml, yamlFiles...)
	sort.Strings(files)
	return files, nil
}

func loadSkillReferencesDir(skillDir string) ([]skill.ReferenceSpec, error) {
	files, err := sortedGlob(filepath.Join(skillDir, "references", "*.md"))
	if err != nil {
		return nil, err
	}
	refs := make([]skill.ReferenceSpec, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("agentdir: read skill reference %s: %w", file, err)
		}
		ref, err := DecodeSkillReference(filepath.ToSlash(filepath.Join("references", filepath.Base(file))), data)
		if err != nil {
			return nil, fmt.Errorf("agentdir: decode skill reference %s: %w", file, err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func loadSkillReferencesFS(fsys fs.FS, skillDir string) ([]skill.ReferenceSpec, error) {
	files, err := sortedFSGlob(fsys, path.Join(skillDir, "references", "*.md"))
	if err != nil {
		return nil, err
	}
	refs := make([]skill.ReferenceSpec, 0, len(files))
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("agentdir: read skill reference %s: %w", file, err)
		}
		ref, err := DecodeSkillReference(path.Join("references", path.Base(file)), data)
		if err != nil {
			return nil, fmt.Errorf("agentdir: decode skill reference %s: %w", file, err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// DecodeSkillReference decodes one skill reference markdown file.
func DecodeSkillReference(refPath string, data []byte) (skill.ReferenceSpec, error) {
	var fm referenceFrontmatter
	body, err := decodeFrontmatter(data, &fm)
	if err != nil {
		return skill.ReferenceSpec{}, err
	}
	refPath = strings.TrimSpace(strings.ReplaceAll(refPath, "\\", "/"))
	spec := skill.ReferenceSpec{
		Path:     refPath,
		Body:     strings.TrimSpace(body),
		Triggers: referenceTriggers(fm),
	}
	if !skill.ValidReferencePath(spec.Path) {
		return skill.ReferenceSpec{}, fmt.Errorf("skill reference path %q is invalid", refPath)
	}
	return spec, nil
}

func referenceTriggers(fm referenceFrontmatter) []string {
	if triggers := frontmatterStrings(fm.Triggers); len(triggers) > 0 {
		return triggers
	}
	if strings.TrimSpace(fm.Trigger) == "" {
		return nil
	}
	return cleanStrings(strings.Split(fm.Trigger, ","))
}

func skillTriggers(fm skillFrontmatter) []string {
	triggers := frontmatterStrings(fm.Triggers)
	if len(triggers) > 0 {
		return triggers
	}
	return frontmatterStrings(fm.Metadata["triggers"])
}

func skillMetadata(fm skillFrontmatter) map[string]string {
	values := map[string]string{
		"license":       strings.TrimSpace(fm.License),
		"risk":          strings.TrimSpace(fm.Risk),
		"compatibility": strings.TrimSpace(fm.Compatibility),
		"domain":        strings.TrimSpace(fm.Domain),
		"role":          strings.TrimSpace(fm.Role),
	}
	for key, value := range fm.Metadata {
		if _, exists := values[key]; !exists || strings.TrimSpace(values[key]) == "" {
			values[key] = strings.TrimSpace(value)
		}
	}
	if fm.UserInvocable {
		values["user_invocable"] = "true"
	}
	out := map[string]string{}
	for key, value := range values {
		if value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
func frontmatterStrings(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return cleanStrings(typed)
	case string:
		return cleanStrings(strings.Split(typed, ","))
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if item == nil {
				continue
			}
			values = append(values, fmt.Sprint(item))
		}
		return cleanStrings(values)
	default:
		return cleanStrings([]string{fmt.Sprint(typed)})
	}
}

func cleanStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cleanFSRoot(root string) string {
	root = strings.TrimSpace(strings.ReplaceAll(root, "\\", "/"))
	root = path.Clean(root)
	if root == "." || root == "" {
		return "."
	}
	return strings.Trim(root, "/")
}

func filenameStem(filename string) string {
	base := filepath.Base(filename)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func toToolRefs(values []string) []agent.ToolRef {
	out := make([]agent.ToolRef, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, agent.ToolRef{Name: value})
		}
	}
	return out
}

func toCommandRefs(values []string) []agent.CommandRef {
	out := make([]agent.CommandRef, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, agent.CommandRef{Name: value})
		}
	}
	return out
}

func toSkillRefs(values []string) []skill.Ref {
	out := make([]skill.Ref, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, skill.Ref{Name: skill.Name(value)})
		}
	}
	return out
}

func toStepIDs(values []string) []workflow.StepID {
	out := make([]workflow.StepID, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, workflow.StepID(value))
		}
	}
	return out
}

func stringListAnnotations(key string, values []string) map[string]string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			clean = append(clean, value)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	return map[string]string{key: strings.Join(clean, ",")}
}

func annotationsOrNil(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	return values
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func validateCommand(spec command.Spec) error {
	if len(spec.Path) == 0 {
		return fmt.Errorf("command: path is empty")
	}
	for i, part := range spec.Path {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("command: path[%d] is empty", i)
		}
	}
	switch spec.Target.Kind {
	case invocation.TargetPrompt:
		if strings.TrimSpace(spec.Target.Prompt) == "" {
			return fmt.Errorf("command: prompt target is empty")
		}
	case invocation.TargetWorkflow:
		if strings.TrimSpace(string(spec.Target.Workflow)) == "" {
			return fmt.Errorf("command: workflow target is empty")
		}
	default:
		return fmt.Errorf("command: target kind %q is unsupported by agentdir", spec.Target.Kind)
	}
	return nil
}
