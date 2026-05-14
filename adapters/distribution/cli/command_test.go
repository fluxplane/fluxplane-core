package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
)

func TestDescribeCommandRendersWithoutRuntime(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:        "sample",
			Title:       "Sample",
			Description: "Sample distribution.",
		},
		Bundles: []resource.ContributionBundle{{
			Source: resource.SourceRef{ID: "sample/source"},
		}},
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"describe", "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := out.String()
	for _, want := range []string{`"distribution"`, `"name": "sample"`, `"sources"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("describe output missing %q:\n%s", want, text)
		}
	}
}

func TestDescribeCommandRejectsUnsupportedOutput(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{Name: "sample"},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"describe", "-o", "xml"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unsupported output "xml"`) {
		t.Fatalf("Execute error = %v, want unsupported output", err)
	}
}

func TestDescribeAgentCommandRejectsUnsupportedOutput(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{Name: "sample"},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"describe", "agent", "assistant", "-o", "xml"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unsupported output "xml"`) {
		t.Fatalf("Execute error = %v, want unsupported output", err)
	}
}

func TestModelsCommandRendersDistributionModels(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{Name: "sample"},
		Bundles: []resource.ContributionBundle{{
			LLMProviders: []corellm.ProviderSpec{{
				Name:        "localai",
				DisplayName: "Local AI",
				Models: []corellm.ModelSpec{{
					Ref:           corellm.ModelRef{Name: "local-model"},
					ContextTokens: 1234,
				}},
			}},
		}},
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"models"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := out.String()
	for _, want := range []string{"Providers:", "localai", "Local AI", "local-model", "context 1234"} {
		if !strings.Contains(text, want) {
			t.Fatalf("models output missing %q:\n%s", want, text)
		}
	}
}

func TestModelsCommandRendersJSON(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{Name: "sample"},
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"models", "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var providers []corellm.ProviderSpec
	if err := json.Unmarshal(out.Bytes(), &providers); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if len(providers) == 0 {
		t.Fatalf("providers is empty")
	}
}

func TestModelsCommandRejectsUnsupportedOutput(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{Name: "sample"},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"models", "-o", "xml"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `models: unsupported output "xml"`) {
		t.Fatalf("Execute error = %v, want unsupported output", err)
	}
}

func TestCommandPropagatesReasoningFlags(t *testing.T) {
	runtime := &captureRuntime{}
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "coder",
			DefaultSession:      coresession.Ref{Name: "coder"},
			DefaultConversation: channel.ConversationRef{ID: "coder"},
		},
		Runtime: runtime,
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--input", "hello", "--thinking", "on", "--effort", "high"})

	err := cmd.Execute()
	if !errors.Is(err, errStopOpen) {
		t.Fatalf("Execute error = %v, want stop open", err)
	}
	if runtime.request.Thinking != "on" || !runtime.request.ThinkingSet || runtime.request.Effort != "high" || !runtime.request.EffortSet {
		t.Fatalf("request = %#v, want reasoning flags", runtime.request)
	}
}

func TestCommandDoesNotSetDefaultEffort(t *testing.T) {
	runtime := &captureRuntime{}
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "coder",
			DefaultSession:      coresession.Ref{Name: "coder"},
			DefaultConversation: channel.ConversationRef{ID: "coder"},
		},
		Runtime: runtime,
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--input", "hello"})

	err := cmd.Execute()
	if !errors.Is(err, errStopOpen) {
		t.Fatalf("Execute error = %v, want stop open", err)
	}
	if runtime.request.Effort != "" || runtime.request.EffortSet {
		t.Fatalf("request = %#v, want no default effort", runtime.request)
	}
}

func TestCommandRejectsInvalidEffort(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{Spec: coredistribution.Spec{Name: "coder"}})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--input", "hello", "--effort", "extreme"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `invalid --effort "extreme"`) {
		t.Fatalf("Execute error = %v, want invalid effort", err)
	}
}

var errStopOpen = errors.New("stop open")

type captureRuntime struct {
	request distribution.OpenRequest
}

func (r *captureRuntime) OpenSession(_ context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	r.request = req
	return nil, errStopOpen
}
