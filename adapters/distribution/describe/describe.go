// Package describe renders static distribution metadata and bundled resources.
package describe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fluxplane/agentruntime/adapters/resources/resourceview"
	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
)

var (
	// ErrAgentNotFound reports that no bundled static agent matched the ref.
	ErrAgentNotFound = errors.New("distribution describe: agent not found")
	// ErrAgentAmbiguous reports that a ref matched multiple bundled agents.
	ErrAgentAmbiguous = errors.New("distribution describe: agent is ambiguous")
)

// Output is the structured static description of a distribution.
type Output struct {
	Distribution        coredistribution.Spec `json:"distribution" yaml:"distribution"`
	resourceview.Output `yaml:",inline"`
}

// AgentOutput is the structured static description of one bundled agent.
type AgentOutput struct {
	Distribution coredistribution.Spec `json:"distribution" yaml:"distribution"`
	Resource     resource.ResourceID   `json:"resource" yaml:"resource"`
	Source       resource.SourceRef    `json:"source" yaml:"source"`
	Agent        agent.Spec            `json:"agent" yaml:"agent"`
	Apps         []coreapp.Spec        `json:"apps,omitempty" yaml:"apps,omitempty"`
	Sessions     []coresession.Spec    `json:"sessions,omitempty" yaml:"sessions,omitempty"`
}

// NewOutput builds a static description from distribution metadata and bundles.
func NewOutput(dist distribution.Distribution) Output {
	return Output{Distribution: dist.Spec, Output: resourceview.NewOutput(dist.Bundles, nil)}
}

// RenderTree renders distribution metadata and bundled resources for humans.
func RenderTree(out io.Writer, dist distribution.Distribution) error {
	w := treeWriter{out: out}
	renderDistribution(&w, dist.Spec)
	if w.err != nil {
		return w.err
	}
	w.println()
	if w.err != nil {
		return w.err
	}
	return resourceview.RenderTree(out, dist.Bundles, nil)
}

// RenderJSON renders machine-readable distribution description JSON.
func RenderJSON(out io.Writer, dist distribution.Distribution) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(NewOutput(dist))
}

// RenderYAML renders machine-readable distribution description YAML.
func RenderYAML(out io.Writer, dist distribution.Distribution) error {
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	return enc.Encode(NewOutput(dist))
}

// RenderAgentTree renders one bundled agent in a detailed human-readable form.
func RenderAgentTree(out io.Writer, dist distribution.Distribution, ref string) error {
	desc, err := Agent(dist, ref)
	if err != nil {
		return err
	}
	w := treeWriter{out: out}
	renderAgentDetail(&w, desc)
	return w.err
}

// RenderAgentJSON renders one bundled agent as machine-readable JSON.
func RenderAgentJSON(out io.Writer, dist distribution.Distribution, ref string) error {
	desc, err := Agent(dist, ref)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(desc)
}

// RenderAgentYAML renders one bundled agent as machine-readable YAML.
func RenderAgentYAML(out io.Writer, dist distribution.Distribution, ref string) error {
	desc, err := Agent(dist, ref)
	if err != nil {
		return err
	}
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	return enc.Encode(desc)
}

// Agent returns one bundled agent description by local name or resource ref.
func Agent(dist distribution.Distribution, ref string) (AgentOutput, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return AgentOutput{}, fmt.Errorf("%w: empty ref", ErrAgentNotFound)
	}
	var matches []AgentOutput
	for _, bundle := range dist.Bundles {
		for _, spec := range bundle.Agents {
			id := resource.DeriveResourceID(bundle.Source, "agent", string(spec.Name))
			if !agentMatches(id, spec, ref) {
				continue
			}
			matches = append(matches, AgentOutput{
				Distribution: dist.Spec,
				Resource:     id,
				Source:       bundle.Source,
				Agent:        spec,
				Apps:         appsForAgent(bundle, spec),
				Sessions:     sessionsForAgent(bundle, spec),
			})
		}
	}
	switch len(matches) {
	case 0:
		return AgentOutput{}, fmt.Errorf("%w: %s", ErrAgentNotFound, ref)
	case 1:
		return matches[0], nil
	default:
		return AgentOutput{}, fmt.Errorf("%w: %s matches %s", ErrAgentAmbiguous, ref, agentAddresses(matches))
	}
}

// ResourceIDs derives canonical resource IDs for every static bundle resource.
func ResourceIDs(bundles []resource.ContributionBundle) []resource.ResourceID {
	return resourceview.ResourceIDs(bundles)
}

type treeWriter struct {
	out io.Writer
	err error
}

func (w *treeWriter) printf(format string, args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintf(w.out, format, args...)
}

func (w *treeWriter) println(args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintln(w.out, args...)
}

func renderDistribution(out *treeWriter, spec coredistribution.Spec) {
	out.println("Distribution:")
	line(out, "name", spec.Name)
	line(out, "title", spec.Title)
	line(out, "description", spec.Description)
	line(out, "author", spec.Author)
	line(out, "version", spec.Version)
	line(out, "default session", string(spec.DefaultSession.Name))
	line(out, "default conversation", spec.DefaultConversation.ID)
	if model := modelLabel(spec.DefaultModel); model != "" {
		line(out, "default model", model)
	}
	if surfaces := surfaceNames(spec.Surfaces); len(surfaces) > 0 {
		line(out, "surfaces", strings.Join(surfaces, ", "))
	}
	if len(spec.Build.Assets) > 0 {
		line(out, "build assets", strings.Join(spec.Build.Assets, ", "))
	}
	if spec.Build.Docker != nil {
		line(out, "docker", dockerBuildLabel(*spec.Build.Docker))
	}
	if len(spec.Commands) > 0 {
		var names []string
		for _, command := range spec.Commands {
			names = append(names, command.Name)
		}
		sort.Strings(names)
		line(out, "commands", strings.Join(names, ", "))
	}
}

func dockerBuildLabel(spec coredistribution.DockerBuildSpec) string {
	var parts []string
	if spec.Image != "" {
		parts = append(parts, "image "+spec.Image)
	}
	if len(spec.Tags) > 0 {
		parts = append(parts, "tags "+strings.Join(spec.Tags, ","))
	}
	if spec.Dockerfile != "" {
		parts = append(parts, "dockerfile "+spec.Dockerfile)
	}
	if spec.Context != "" {
		parts = append(parts, "context "+spec.Context)
	}
	if len(spec.Platforms) > 0 {
		parts = append(parts, "platforms "+strings.Join(spec.Platforms, ","))
	}
	if len(parts) == 0 {
		return "enabled"
	}
	return strings.Join(parts, "; ")
}

func renderAgentDetail(out *treeWriter, desc AgentOutput) {
	spec := desc.Agent
	out.println("Agent:")
	line(out, "name", string(spec.Name))
	line(out, "resource", desc.Resource.Address())
	line(out, "source", resourceview.SourceLabel(desc.Source))
	line(out, "description", spec.Description)
	if spec.Driver.Kind != "" {
		line(out, "driver", string(spec.Driver.Kind))
	}
	if spec.Inference.Model != "" {
		line(out, "model", spec.Inference.Model)
	}
	if spec.Inference.MaxOutputTokens > 0 {
		line(out, "max output tokens", fmt.Sprint(spec.Inference.MaxOutputTokens))
	}
	if spec.Turns.MaxSteps > 0 {
		line(out, "max steps", fmt.Sprint(spec.Turns.MaxSteps))
	}
	if spec.Turns.Continuation.MaxContinuations > 0 {
		line(out, "max continuations", fmt.Sprint(spec.Turns.Continuation.MaxContinuations))
	}
	if agency := agencyLabel(spec.Agency); agency != "" {
		line(out, "agency", agency)
	}
	if spec.Objective.Role != "" || spec.Objective.Instructions != "" || spec.Objective.Success != "" {
		out.println("\nObjective:")
		line(out, "role", spec.Objective.Role)
		line(out, "instructions", spec.Objective.Instructions)
		line(out, "success", spec.Objective.Success)
	}
	renderStringList(out, "Operations", operationNames(spec.Operations))
	renderStringList(out, "Tools", agentToolNames(spec))
	renderStringList(out, "Commands", agentCommandNames(spec))
	renderStringList(out, "Datasources", agentDatasourceNames(spec))
	renderStringList(out, "Skills", agentSkillNames(spec))
	renderStringList(out, "Context Providers", agentContextNames(spec))
	renderRelated(out, desc)
	if strings.TrimSpace(spec.System) != "" {
		out.println("\nSystem:")
		for _, line := range strings.Split(spec.System, "\n") {
			out.printf("  %s\n", line)
		}
	}
}

func renderStringList(out *treeWriter, title string, values []string) {
	if len(values) == 0 {
		return
	}
	sort.Strings(values)
	out.printf("\n%s:\n", title)
	for _, value := range values {
		out.printf("  - %s\n", value)
	}
}

func renderRelated(out *treeWriter, desc AgentOutput) {
	if len(desc.Apps) == 0 && len(desc.Sessions) == 0 {
		return
	}
	out.println("\nRelated:")
	if len(desc.Apps) > 0 {
		var names []string
		for _, spec := range desc.Apps {
			names = append(names, firstNonEmpty(string(spec.Name), "app"))
		}
		sort.Strings(names)
		out.printf("  apps: %s\n", strings.Join(names, ", "))
	}
	if len(desc.Sessions) > 0 {
		var names []string
		for _, spec := range desc.Sessions {
			names = append(names, string(spec.Name))
		}
		sort.Strings(names)
		out.printf("  sessions: %s\n", strings.Join(names, ", "))
	}
}

func line(out *treeWriter, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	out.printf("  %-20s %s\n", key+":", value)
}

func modelLabel(model coredistribution.ModelDefault) string {
	switch {
	case model.Provider != "" && model.Model != "" && model.UseCase != "":
		return model.Provider + "/" + model.Model + " (" + model.UseCase + ")"
	case model.Provider != "" && model.Model != "":
		return model.Provider + "/" + model.Model
	case model.Model != "":
		return model.Model
	default:
		return model.Provider
	}
}

func surfaceNames(s coredistribution.Surfaces) []string {
	var out []string
	if s.CLI {
		out = append(out, "cli")
	}
	if s.REPL {
		out = append(out, "repl")
	}
	if s.OneShot {
		out = append(out, "one-shot")
	}
	if s.Serve {
		out = append(out, "serve")
	}
	if s.Deploy {
		out = append(out, "deploy")
	}
	if s.Validate {
		out = append(out, "validate")
	}
	if s.Status {
		out = append(out, "status")
	}
	if s.Discover {
		out = append(out, "discover")
	}
	return out
}

func agentToolNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Tools))
	for _, ref := range spec.Tools {
		out = append(out, ref.Name)
	}
	return out
}

func agentCommandNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Commands))
	for _, ref := range spec.Commands {
		out = append(out, ref.Name)
	}
	return out
}

func agentSkillNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Skills))
	for _, ref := range spec.Skills {
		out = append(out, string(ref.Name))
	}
	return out
}

func agentDatasourceNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Datasources))
	for _, ref := range spec.Datasources {
		out = append(out, string(ref.Name))
	}
	return out
}

func agentContextNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Context))
	for _, ref := range spec.Context {
		out = append(out, string(ref.Name))
	}
	return out
}

func operationNames(refs []operation.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.String())
	}
	return out
}

func agencyLabel(agency agent.AgencyProfile) string {
	var parts []string
	if agency.Autonomy != "" {
		parts = append(parts, string(agency.Autonomy))
	}
	if agency.Reactive {
		parts = append(parts, "reactive")
	}
	if agency.Proactive {
		parts = append(parts, "proactive")
	}
	if agency.Social {
		parts = append(parts, "social")
	}
	if agency.Stateful {
		parts = append(parts, "stateful")
	}
	if agency.Learning {
		parts = append(parts, "learning")
	}
	return strings.Join(parts, ", ")
}

func agentMatches(id resource.ResourceID, spec agent.Spec, ref string) bool {
	return string(spec.Name) == ref || id.Address() == ref || id.MatchesRef(ref)
}

func appsForAgent(bundle resource.ContributionBundle, spec agent.Spec) []coreapp.Spec {
	var out []coreapp.Spec
	for _, appSpec := range bundle.Apps {
		if appSpec.DefaultAgent.Name == spec.Name {
			out = append(out, appSpec)
		}
	}
	return out
}

func sessionsForAgent(bundle resource.ContributionBundle, spec agent.Spec) []coresession.Spec {
	var out []coresession.Spec
	for _, sessionSpec := range bundle.Sessions {
		if sessionSpec.Agent.Name == spec.Name {
			out = append(out, sessionSpec)
		}
	}
	return out
}

func agentAddresses(matches []AgentOutput) string {
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.Resource.Address())
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
