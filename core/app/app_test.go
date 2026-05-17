package app

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
)

func TestSpecValidateAllowsEngineerManifestShape(t *testing.T) {
	approvedOnly := true
	spec := Spec{
		Name:         "dev",
		DefaultAgent: agent.Ref{Name: "main"},
		Sources:      []SourceSpec{{Location: ".agents", Scope: "embedded", Ecosystem: "agents"}},
		Discovery: DiscoveryPolicy{
			IncludeGlobalUserResources: true,
			TrustStoreDir:              ".agentsdk",
		},
		Model: ModelPolicy{
			UseCase:      "agentic_coding",
			SourceAPI:    "auto",
			ApprovedOnly: &approvedOnly,
		},
		Plugins: []PluginRef{{Name: "browser"}, {Name: "memory"}},
	}

	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSpecValidateRejectsEmptySource(t *testing.T) {
	err := Spec{Sources: []SourceSpec{{}}}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want empty source error")
	}
}

func TestSpecValidateRejectsEmptyPlugin(t *testing.T) {
	err := Spec{Plugins: []PluginRef{{}}}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want empty plugin error")
	}
}

func TestSpecValidateRejectsInvalidPluginInstance(t *testing.T) {
	err := Spec{Plugins: []PluginRef{{Name: "gitlab", Instance: "company/a"}}}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want invalid plugin instance error")
	}
}
