package launch

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	sessionruntime "github.com/fluxplane/agentruntime/orchestration/session"
)

func TestRunPathUsesLoadedDistributionAndSubmitsInput(t *testing.T) {
	runtime := &fakeRunRuntime{}
	loader := func(context.Context, string) (distribution.Loaded, error) {
		return distribution.Loaded{
			Distribution: distribution.Distribution{
				Spec: coredistribution.Spec{
					Name:           "sample",
					DefaultSession: coresession.Ref{Name: "main"},
				},
				Runtime: runtime,
			},
		}, nil
	}
	out := bytes.Buffer{}
	errOut := bytes.Buffer{}
	err := RunPathWithLoader(context.Background(), loader, "ignored", RunPathOptions{
		Session:      "custom",
		Conversation: "conv",
		Thinking:     "on",
		ThinkingSet:  true,
		Effort:       "high",
		EffortSet:    true,
		Input:        "hello",
		In:           strings.NewReader(""),
		Out:          &out,
		Err:          &errOut,
	})
	if err != nil {
		t.Fatalf("RunPathWithLoader: %v", err)
	}
	if runtime.request.Session.Name != "custom" {
		t.Fatalf("session = %q, want custom", runtime.request.Session.Name)
	}
	if runtime.request.Conversation.ID != "conv" {
		t.Fatalf("conversation = %q, want conv", runtime.request.Conversation.ID)
	}
	if runtime.request.Thinking != "on" || !runtime.request.ThinkingSet || runtime.request.Effort != "high" || !runtime.request.EffortSet {
		t.Fatalf("reasoning request = %#v, want thinking on effort high", runtime.request)
	}
	if runtime.session.submission.Input == nil || runtime.session.submission.Input.Text != "hello" {
		t.Fatalf("submission = %#v, want input hello", runtime.session.submission)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("output = %q, want ok", out.String())
	}
}

func TestRunPathRequiresDefaultOrExplicitSession(t *testing.T) {
	loader := func(context.Context, string) (distribution.Loaded, error) {
		return distribution.Loaded{
			Manifest: "agentsdk.app.yaml",
			Distribution: distribution.Distribution{
				Spec:    coredistribution.Spec{Name: "sample"},
				Runtime: &fakeRunRuntime{},
			},
		}, nil
	}
	err := RunPathWithLoader(context.Background(), loader, "ignored", RunPathOptions{
		Input: "hello",
		In:    strings.NewReader(""),
		Out:   io.Discard,
		Err:   io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "no default session") {
		t.Fatalf("RunPathWithLoader error = %v, want no default session", err)
	}
}

func TestRunPathSuggestsInitWhenUninitialized(t *testing.T) {
	loader := func(context.Context, string) (distribution.Loaded, error) {
		return distribution.Loaded{
			Root: "/repo/sample",
			Distribution: distribution.Distribution{
				Spec:    coredistribution.Spec{Name: "sample"},
				Runtime: &fakeRunRuntime{},
			},
		}, nil
	}
	err := RunPathWithLoader(context.Background(), loader, "sample", RunPathOptions{
		Input: "hello",
		In:    strings.NewReader(""),
		Out:   io.Discard,
		Err:   io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "agentsdk init sample") {
		t.Fatalf("RunPathWithLoader error = %v, want init guidance", err)
	}
}

func TestRunCommandHelpIncludesConnectorPath(t *testing.T) {
	cmd := NewRunCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "--connectors-path") {
		t.Fatalf("help = %q, want --connectors-path", out.String())
	}
}

func TestRunCommandDefaultsPathToCurrentDirectory(t *testing.T) {
	var gotPath string
	runtime := &fakeRunRuntime{}
	loader := func(_ context.Context, path string) (distribution.Loaded, error) {
		gotPath = path
		return distribution.Loaded{
			Distribution: distribution.Distribution{
				Spec: coredistribution.Spec{
					Name:           "sample",
					DefaultSession: coresession.Ref{Name: "main"},
				},
				Runtime: runtime,
			},
		}, nil
	}
	cmd := NewRunCommandWithLoader(loader)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--input", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "." {
		t.Fatalf("path = %q, want .", gotPath)
	}
	if runtime.request.Effort != "" || runtime.request.EffortSet {
		t.Fatalf("reasoning request = %#v, want no default effort", runtime.request)
	}
}

func TestRunCommandRejectsInvalidThinking(t *testing.T) {
	cmd := NewRunCommandWithLoader(func(context.Context, string) (distribution.Loaded, error) {
		t.Fatal("loader should not be called")
		return distribution.Loaded{}, nil
	})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--thinking", "auth", "--input", "hello"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `invalid --thinking "auth"`) {
		t.Fatalf("Execute error = %v, want invalid thinking", err)
	}
}

type fakeRunRuntime struct {
	request distribution.OpenRequest
	session *fakeRunSession
}

func (r *fakeRunRuntime) OpenSession(_ context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	r.request = req
	r.session = &fakeRunSession{
		info: clientapi.SessionInfo{
			Session:      req.Session,
			Thread:       corethread.Ref{ID: "thread-1", BranchID: corethread.MainBranch},
			Conversation: req.Conversation,
		},
	}
	return r.session, nil
}

type fakeRunSession struct {
	info       clientapi.SessionInfo
	submission clientapi.Submission
}

func (s *fakeRunSession) Info() clientapi.SessionInfo { return s.info }

func (s *fakeRunSession) Submit(_ context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	s.submission = submission
	return fakeRunHandle{info: s.info, submission: submission}, nil
}

func (s *fakeRunSession) Events(context.Context, clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch, func() {}, nil
}

func (s *fakeRunSession) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	return func() {}, nil
}

func (s *fakeRunSession) Close(context.Context) error { return nil }

type fakeRunHandle struct {
	info       clientapi.SessionInfo
	submission clientapi.Submission
}

func (r fakeRunHandle) ID() clientapi.RunID { return "run-1" }

func (r fakeRunHandle) Session() clientapi.SessionInfo { return r.info }

func (r fakeRunHandle) Submission() clientapi.Submission { return r.submission }

func (r fakeRunHandle) Events() <-chan clientapi.Event {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch
}

func (r fakeRunHandle) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (r fakeRunHandle) Err() error { return nil }

func (r fakeRunHandle) Wait(context.Context) (clientapi.Result, error) {
	return clientapi.Result{
		RunID:      r.ID(),
		Session:    r.info,
		Submission: r.submission,
		Input:      &sessionruntime.InputResult{Status: sessionruntime.InputStatusOK},
		Outbound: &channel.Outbound{
			Kind:    channel.OutboundMessage,
			Message: &channel.Message{Content: "ok"},
		},
	}, nil
}
