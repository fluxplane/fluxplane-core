package llm

// Capability describes one provider/model feature relevant to routing,
// prompting, safety, or cost planning.
type Capability string

const (
	CapabilityToolCalling    Capability = "tool_calling"
	CapabilityParallelTools  Capability = "parallel_tools"
	CapabilityStreaming      Capability = "streaming"
	CapabilityReasoning      Capability = "reasoning"
	CapabilityThinking       Capability = "thinking"
	CapabilityPromptCaching  Capability = "prompt_caching"
	CapabilityStructuredJSON Capability = "structured_json"
	CapabilityVision         Capability = "vision"
	CapabilityComputerUse    Capability = "computer_use"
	CapabilityFileSearch     Capability = "file_search"
	CapabilityWebSearch      Capability = "web_search"
)

// CapabilitySet is an inert set of model capabilities.
type CapabilitySet []Capability

// Has reports whether capability is declared.
func (s CapabilitySet) Has(capability Capability) bool {
	for _, current := range s {
		if current == capability {
			return true
		}
	}
	return false
}
