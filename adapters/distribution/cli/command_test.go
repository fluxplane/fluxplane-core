package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/channel"
	corecommand "github.com/fluxplane/fluxplane-core/core/command"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	sessionruntime "github.com/fluxplane/fluxplane-core/orchestration/session"
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

func TestRunFlagsAreLocalToRootCommand(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{Spec: coredistribution.Spec{Name: "sample"}})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"describe", "--model", "test-model"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --model") {
		t.Fatalf("Execute error = %v, want unknown model flag", err)
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
			Name:                "assistant",
			DefaultSession:      coresession.Ref{Name: "assistant"},
			DefaultConversation: channel.ConversationRef{ID: "assistant"},
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
			Name:                "assistant",
			DefaultSession:      coresession.Ref{Name: "assistant"},
			DefaultConversation: channel.ConversationRef{ID: "assistant"},
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

func TestCommandPropagatesWorkspaceRootFlags(t *testing.T) {
	runtime := &captureRuntime{}
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "assistant",
			DefaultSession:      coresession.Ref{Name: "assistant"},
			DefaultConversation: channel.ConversationRef{ID: "assistant"},
		},
		Runtime: runtime,
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--input", "hello", "--workspace-root", "../api", "--workspace-root", "web=../web"})

	err := cmd.Execute()
	if !errors.Is(err, errStopOpen) {
		t.Fatalf("Execute error = %v, want stop open", err)
	}
	roots := runtime.request.Launch.Workspace.Roots
	if len(roots) != 2 || roots[0].Name != "api" || roots[0].Path != "../api" || roots[1].Name != "web" || roots[1].Path != "../web" {
		t.Fatalf("workspace roots = %#v", roots)
	}
}

func TestCommandPreservesStructuredWorkspaceDefaults(t *testing.T) {
	runtime := &captureRuntime{}
	cmd := NewCommandWithOptions(distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "assistant",
			DefaultSession:      coresession.Ref{Name: "assistant"},
			DefaultConversation: channel.ConversationRef{ID: "assistant"},
		},
		Runtime: runtime,
	}, CommandOptions{
		Workspace: distribution.WorkspaceConfig{
			Roots: []distribution.WorkspaceRoot{{
				Name:     "api",
				Path:     "../api",
				Access:   "read_only",
				Create:   true,
				EnvFiles: []string{"api.env"},
			}},
			EnvFiles: []string{".env"},
		},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--input", "hello", "--workspace-root", "web=../web", "--env-file", "local.env"})

	err := cmd.Execute()
	if !errors.Is(err, errStopOpen) {
		t.Fatalf("Execute error = %v, want stop open", err)
	}
	workspace := runtime.request.Launch.Workspace
	if len(workspace.Roots) != 2 || workspace.Roots[0].Name != "api" || workspace.Roots[1].Name != "web" {
		t.Fatalf("workspace roots = %#v, want config root plus flag root", workspace.Roots)
	}
	if workspace.Roots[0].Access != "read_only" || !workspace.Roots[0].Create || strings.Join(workspace.Roots[0].EnvFiles, ",") != "api.env" {
		t.Fatalf("config root = %#v, want structured metadata preserved", workspace.Roots[0])
	}
	if strings.Join(workspace.EnvFiles, ",") != ".env,local.env" {
		t.Fatalf("env files = %#v, want config plus flag env files", workspace.EnvFiles)
	}
}

func TestCommandPropagatesEnvFileFlagsToRootWorkspace(t *testing.T) {
	runtime := &captureRuntime{}
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "assistant",
			DefaultSession:      coresession.Ref{Name: "assistant"},
			DefaultConversation: channel.ConversationRef{ID: "assistant"},
		},
		Runtime: runtime,
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--input", "hello", "--env-file", ".env", "--env-file=.env.local"})

	err := cmd.Execute()
	if !errors.Is(err, errStopOpen) {
		t.Fatalf("Execute error = %v, want stop open", err)
	}
	files := runtime.request.Launch.Workspace.EnvFiles
	if len(files) != 2 || files[0] != ".env" || files[1] != ".env.local" {
		t.Fatalf("env files = %#v, want root env files", files)
	}
}

func TestCommandDoesNotSetDefaultEffort(t *testing.T) {
	runtime := &captureRuntime{}
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "assistant",
			DefaultSession:      coresession.Ref{Name: "assistant"},
			DefaultConversation: channel.ConversationRef{ID: "assistant"},
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
			Name:                "assistant",
			DefaultSession:      coresession.Ref{Name: "assistant"},
			DefaultConversation: channel.ConversationRef{ID: "assistant"},
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

func TestRunREPLCollectsSlashCommandContinuationLines(t *testing.T) {
	session := &captureSession{}
	runtime := &sessionRuntime{session: session}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                "assistant",
			DefaultSession:      coresession.Ref{Name: "assistant"},
			DefaultConversation: channel.ConversationRef{ID: "assistant"},
		},
		Runtime: runtime,
	}, RunOptions{
		In: strings.NewReader("/loop --count=5 \"find one bug,\ntrack it in bug-hunt.md\"\n/exit\n"),
		PromptHandler: func(_ context.Context, prompt string, _ clientapi.SessionHandle, _ RunOptions) (bool, error) {
			_, _, err := corecommand.ParseSlash(prompt)
			return false, err
		},
		Out: &out,
		Err: &errOut,
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submissions = %d, want one command", len(session.submissions))
	}
	submission := session.submissions[0]
	if submission.Kind != clientapi.SubmissionCommand || submission.CommandLine == "" {
		t.Fatalf("submission = %#v, want raw command line submission", submission)
	}
	want := "/loop --count=5 \"find one bug,\ntrack it in bug-hunt.md\""
	if submission.CommandLine != want {
		t.Fatalf("command line = %q, want %q", submission.CommandLine, want)
	}
	if strings.Contains(errOut.String(), "unterminated quoted string") {
		t.Fatalf("err = %q, want no unterminated quote error", errOut.String())
	}
	if !strings.Contains(out.String(), "assistant... ") {
		t.Fatalf("out = %q, want continuation prompt", out.String())
	}
}

func TestRunREPLUsesPerTurnCancelableContext(t *testing.T) {
	session := &blockingSession{started: make(chan struct{}), release: make(chan struct{})}
	runtime := &sessionRuntime{session: session}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut bytes.Buffer
	done := make(chan error, 1)

	go func() {
		done <- Run(ctx, distribution.Distribution{
			Spec: coredistribution.Spec{
				Name:                "assistant",
				DefaultSession:      coresession.Ref{Name: "assistant"},
				DefaultConversation: channel.ConversationRef{ID: "assistant"},
			},
			Runtime: runtime,
		}, RunOptions{
			In:  io.MultiReader(strings.NewReader("work\n/exit\n"), session.releaseReader()),
			Out: &out,
			Err: &errOut,
		})
	}()

	select {
	case <-session.started:
	case <-time.After(time.Second):
		t.Fatal("turn did not start")
	}
	cancel()
	session.unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if !session.waitContextCanceled {
		t.Fatal("turn Wait did not observe context cancellation")
	}
}

func TestCommandRejectsInvalidEffort(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{Spec: coredistribution.Spec{Name: "assistant"}})
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
			Name:                "assistant",
			DefaultSession:      coresession.Ref{Name: "assistant"},
			DefaultConversation: channel.ConversationRef{ID: "assistant"},
		},
		Runtime: runtime,
	}, RunOptions{
		Goal: "Test coverage has increased to 90%",
		Out:  &bytes.Buffer{},
		Err:  &bytes.Buffer{},
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
	if submission.Command.Input != nil {
		t.Fatalf("goal input = %#v, want no command input", submission.Command.Input)
	}
}

func TestRunGoalForwardsCommandPayloadWithoutSemanticValidation(t *testing.T) {
	session := &captureSession{}
	runtime := &sessionRuntime{session: session}
	err := Run(context.Background(), distribution.Distribution{
		Spec:    coredistribution.Spec{Name: "assistant"},
		Runtime: runtime,
	}, RunOptions{
		GoalSet: true,
		Out:     &bytes.Buffer{},
		Err:     &bytes.Buffer{},
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
	if submission.Command.Input != nil {
		t.Fatalf("goal input = %#v, want no command input", submission.Command.Input)
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

type blockingSession struct {
	captureSession
	started             chan struct{}
	release             chan struct{}
	waitContextCanceled bool
}

func (s *blockingSession) Submit(_ context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	s.submits++
	s.submissions = append(s.submissions, submission)
	return &blockingRun{session: s, submission: submission}, nil
}

func (s *blockingSession) releaseReader() io.Reader {
	return readerFunc(func(p []byte) (int, error) {
		<-s.release
		return 0, io.EOF
	})
}

func (s *blockingSession) unblock() {
	select {
	case <-s.release:
	default:
		close(s.release)
	}
}

type blockingRun struct {
	session    *blockingSession
	submission clientapi.Submission
}

func (r *blockingRun) ID() clientapi.RunID              { return r.submission.ID }
func (r *blockingRun) Session() clientapi.SessionInfo   { return clientapi.SessionInfo{} }
func (r *blockingRun) Submission() clientapi.Submission { return r.submission }
func (r *blockingRun) Events() <-chan clientapi.Event {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch
}
func (r *blockingRun) Done() <-chan struct{} { return r.session.release }
func (r *blockingRun) Err() error            { return nil }
func (r *blockingRun) Wait(ctx context.Context) (clientapi.Result, error) {
	close(r.session.started)
	select {
	case <-ctx.Done():
		r.session.waitContextCanceled = true
		return clientapi.Result{Submission: r.submission, Input: &sessionruntime.InputResult{Status: sessionruntime.InputStatusFailed}}, ctx.Err()
	case <-r.session.release:
		return clientapi.Result{Submission: r.submission, Input: &sessionruntime.InputResult{Status: sessionruntime.InputStatusOK}}, nil
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
