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
	sessionruntime "github.com/fluxplane/agentruntime/orchestration/session"
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

func TestCommandPropagatesYoloFlag(t *testing.T) {
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
	cmd.SetArgs([]string{"--input", "hello", "--yolo"})

	err := cmd.Execute()
	if !errors.Is(err, errStopOpen) {
		t.Fatalf("Execute error = %v, want stop open", err)
	}
	if !runtime.request.Yolo {
		t.Fatalf("request = %#v, want yolo", runtime.request)
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

func TestRunREPLHandlesUIReasoningLocally(t *testing.T) {
	session := &captureSession{}
	runtime := &sessionRuntime{session: session}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "coder",
			DefaultSession:      coresession.Ref{Name: "coder"},
			DefaultConversation: channel.ConversationRef{ID: "coder"},
		},
		Runtime: runtime,
	}, RunOptions{
		In:  strings.NewReader("/ui:reasoning on\n/exit\n"),
		Out: &out,
		Err: &errOut,
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if session.submits != 0 {
		t.Fatalf("submits = %d, want local UI command not submitted", session.submits)
	}
	if !strings.Contains(errOut.String(), "ui: reasoning on") {
		t.Fatalf("err = %q, want UI status", errOut.String())
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

func TestRunGoalSubmitsGoalCommand(t *testing.T) {
	session := &captureSession{}
	runtime := &sessionRuntime{session: session}
	err := Run(context.Background(), distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "coder",
			DefaultSession:      coresession.Ref{Name: "coder"},
			DefaultConversation: channel.ConversationRef{ID: "coder"},
		},
		Runtime: runtime,
	}, RunOptions{
		Goal:             "Test coverage has increased to 90%",
		MaxContinuations: 20,
		Out:              &bytes.Buffer{},
		Err:              &bytes.Buffer{},
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submissions = %d, want goal command", len(session.submissions))
	}
	submission := session.submissions[0]
	if submission.Command == nil || submission.Command.Path.String() != "/goal" {
		t.Fatalf("submission = %#v, want /goal command", submission)
	}
	if len(submission.Command.Args) != 1 || submission.Command.Args[0] != "Test coverage has increased to 90%" {
		t.Fatalf("args = %#v, want goal arg", submission.Command.Args)
	}
	input := submission.Command.Input.(map[string]any)
	if input["max"] != 20 {
		t.Fatalf("goal input = %#v, want cap 20", input)
	}
}

func TestRunGoalForwardsCommandPayloadWithoutSemanticValidation(t *testing.T) {
	session := &captureSession{}
	runtime := &sessionRuntime{session: session}
	err := Run(context.Background(), distribution.Distribution{
		Spec:    coredistribution.Spec{Name: "coder"},
		Runtime: runtime,
	}, RunOptions{
		GoalSet:             true,
		MaxContinuations:    0,
		MaxContinuationsSet: true,
		Out:                 &bytes.Buffer{},
		Err:                 &bytes.Buffer{},
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submissions = %d, want goal command", len(session.submissions))
	}
	submission := session.submissions[0]
	if submission.Command == nil || submission.Command.Path.String() != "/goal" {
		t.Fatalf("submission = %#v, want /goal command", submission)
	}
	if len(submission.Command.Args) != 1 || submission.Command.Args[0] != "" {
		t.Fatalf("args = %#v, want empty goal forwarded", submission.Command.Args)
	}
	input := submission.Command.Input.(map[string]any)
	if input["max"] != 0 {
		t.Fatalf("goal input = %#v, want explicit cap 0 forwarded", input)
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

type sessionRuntime struct {
	session clientapi.SessionHandle
}

func (r *sessionRuntime) OpenSession(context.Context, distribution.OpenRequest) (clientapi.SessionHandle, error) {
	return r.session, nil
}

type captureSession struct {
	submits     int
	submissions []clientapi.Submission
}

func (s *captureSession) Info() clientapi.SessionInfo { return clientapi.SessionInfo{} }

func (s *captureSession) Submit(_ context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	s.submits++
	s.submissions = append(s.submissions, submission)
	return captureRun{submission: submission}, nil
}

func (s *captureSession) Events(context.Context, clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch, func() {}, nil
}

func (s *captureSession) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	return func() {}, nil
}

func (s *captureSession) Close(context.Context) error { return nil }

type captureRun struct {
	submission clientapi.Submission
}

func (r captureRun) ID() clientapi.RunID { return r.submission.ID }

func (r captureRun) Session() clientapi.SessionInfo { return clientapi.SessionInfo{} }

func (r captureRun) Submission() clientapi.Submission { return r.submission }

func (r captureRun) Events() <-chan clientapi.Event {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch
}

func (r captureRun) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (r captureRun) Err() error { return nil }

func (r captureRun) Wait(context.Context) (clientapi.Result, error) {
	return clientapi.Result{
		Submission: r.submission,
		Command:    &sessionruntime.CommandResult{Status: sessionruntime.CommandStatusOK},
	}, nil
}
