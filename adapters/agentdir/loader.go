package agentdir

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/workflow"
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
	loaders := []func(context.Context, string, *resource.ContributionBundle) error{
		loadAgents,
		loadCommands,
		loadWorkflows,
		loadSkills,
	}
	for _, load := range loaders {
		if err := ctx.Err(); err != nil {
			return resource.ContributionBundle{}, err
		}
		if err := load(ctx, root, &bundle); err != nil {
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

func loadAgents(ctx context.Context, root string, bundle *resource.ContributionBundle) error {
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
			return err
		}
		bundle.Agents = append(bundle.Agents, spec)
	}
	return nil
}

func loadCommands(ctx context.Context, root string, bundle *resource.ContributionBundle) error {
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
			return err
		}
		bundle.Commands = append(bundle.Commands, spec)
	}
	return nil
}

func loadWorkflows(ctx context.Context, root string, bundle *resource.ContributionBundle) error {
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
			return err
		}
		bundle.Workflows = append(bundle.Workflows, spec)
	}
	return nil
}

func loadSkills(ctx context.Context, root string, bundle *resource.ContributionBundle) error {
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
			return err
		}
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
		Stop:        fm.StopCondition.agentSpec(),
		Policy:      agent.Policy{MaxSteps: fm.MaxSteps},
		Tools:       toToolRefs(fm.Tools),
		Commands:    toCommandRefs(fm.Commands),
		Skills:      toSkillRefs(fm.Skills),
		Annotations: stringListAnnotations("capabilities", fm.Capabilities),
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
	spec := command.Spec{
		Path:        command.Path{name},
		Description: strings.TrimSpace(raw.Description),
		Target: invocation.Target{
			Kind:     invocation.TargetWorkflow,
			Workflow: workflow.Name(strings.TrimSpace(raw.Target.Workflow)),
			Input:    raw.Target.Input,
		},
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
		step := workflow.Step{
			ID:        workflow.StepID(strings.TrimSpace(rawStep.ID)),
			Kind:      workflow.StepAgent,
			Agent:     agent.Ref{Name: agent.Name(strings.TrimSpace(rawStep.Agent))},
			DependsOn: toStepIDs(rawStep.DependsOn),
			Raw:       rawStep.Raw,
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
	_, err := decodeFrontmatter(data, &fm)
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
		Source: skill.SourceRef{
			URI:  sourceURI,
			Kind: "agentdir",
		},
	}
	if err := spec.Validate(); err != nil {
		return skill.Spec{}, err
	}
	return spec, nil
}

type agentFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	Model         string            `yaml:"model"`
	MaxTokens     int               `yaml:"max-tokens"`
	MaxSteps      int               `yaml:"max-steps"`
	Temperature   float64           `yaml:"temperature"`
	Thinking      string            `yaml:"thinking"`
	Effort        string            `yaml:"effort"`
	Tools         []string          `yaml:"tools"`
	Skills        []string          `yaml:"skills"`
	Commands      []string          `yaml:"commands"`
	Capabilities  []string          `yaml:"capabilities"`
	StopCondition stopConditionYAML `yaml:"stop-condition"`
}

type stopConditionYAML struct {
	Type       string              `yaml:"type"`
	Max        int                 `yaml:"max"`
	Prompt     string              `yaml:"prompt"`
	Conditions []stopConditionYAML `yaml:"conditions"`
}

func (s stopConditionYAML) agentSpec() agent.StopConditionSpec {
	out := agent.StopConditionSpec{
		Type:   strings.TrimSpace(s.Type),
		Max:    s.Max,
		Prompt: strings.TrimSpace(s.Prompt),
	}
	for _, condition := range s.Conditions {
		out.Conditions = append(out.Conditions, condition.agentSpec())
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
	Input    any    `yaml:"input"`
}

type yamlWorkflow struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Steps       []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	ID        string         `yaml:"id"`
	Agent     string         `yaml:"agent"`
	DependsOn []string       `yaml:"depends-on"`
	Raw       map[string]any `yaml:",inline"`
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func decodeFrontmatter(data []byte, out any) (string, error) {
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
	if err := yaml.Unmarshal([]byte(fm.String()), out); err != nil {
		return "", err
	}
	return body.String(), nil
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
