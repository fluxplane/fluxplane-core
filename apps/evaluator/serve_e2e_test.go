package evaluator

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	adapterllm "github.com/fluxplane/fluxplane-core/adapters/llm"
	"github.com/fluxplane/fluxplane-core/apps/launch"
	"github.com/fluxplane/fluxplane-core/core/agent"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/orchestration/agentfactory"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	coreevent "github.com/fluxplane/fluxplane-event"
)

func TestTargetSubmitOperationUsesLaunchServeUnixSocket(t *testing.T) {
	appDir := t.TempDir()
	socketPath := appDir + "/target.sock"
	writeLaunchServeFixture(t, appDir, socketPath)

	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- launch.Serve(serveCtx, launch.Options{
			AppDir:        appDir,
			Debug:         true,
			ModelResolver: launchServeTestResolver{},
		})
	}()

	waitForLaunchServeReady(t, socketPath, errCh)

	result := targetSubmit(operation.NewContext(context.Background(), coreevent.Discard()), TargetSubmitInput{
		BaseURL:      "http://unix",
		UnixSocket:   socketPath,
		Session:      "target",
		Conversation: "launch-serve-e2e",
		Prompt:       "hello over socket",
		Timeout:      "5s",
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("targetSubmit status = %s error=%#v", result.Status, result.Error)
	}
	out, ok := result.Output.(TargetSubmitOutput)
	if !ok {
		t.Fatalf("output = %T", result.Output)
	}
	if out.ThreadID == "" || out.RunID == "" {
		t.Fatalf("target ids missing: %#v", out)
	}
	if out.OutboundText != "launch target response" {
		t.Fatalf("outbound text = %q, want launch target response", out.OutboundText)
	}
	if len(out.Events) == 0 {
		t.Fatalf("events empty")
	}

	stopServe()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Serve did not stop after context cancellation")
	}
}

func writeLaunchServeFixture(t *testing.T, appDir, socketPath string) {
	t.Helper()
	manifest := fmt.Sprintf(`kind: app
name: evaluator-target-fixture
description: Deterministic target fixture for evaluator serve tests.
default_agent:
  name: target_agent
distribution:
  name: evaluator-target-fixture
  default_session: target
  surfaces:
    serve: true
daemon:
  listeners:
    - name: local
      type: http
      addr: %q
      auth:
        mode: local_socket
  channels:
    - name: local
      type: direct
      listener: local
      session: target
      access:
        mode: open
---
kind: session
name: target
agent: target_agent
---
kind: agent
name: target_agent
model: openai/gpt-5.5
turns:
  max_steps: 1
system: |
  You are a deterministic target fixture.
`, socketPath)
	if err := os.WriteFile(filepath.Join(appDir, "fluxplane.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write fixture manifest: %v", err)
	}
}

func waitForLaunchServeReady(t *testing.T, socketPath string, errCh <-chan error) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	var lastErr error
	for {
		select {
		case err := <-errCh:
			t.Fatalf("Serve exited before readiness: %v", err)
		case <-deadline:
			t.Fatalf("Serve did not become ready; last error: %v", lastErr)
		case <-tick.C:
			client := launchServeHTTPClient(socketPath)
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/control/status", nil)
			if err != nil {
				cancel()
				lastErr = err
				continue
			}
			resp, err := client.Do(req)
			cancel()
			client.CloseIdleConnections()
			if err != nil {
				lastErr = err
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = fmt.Errorf("status %s", resp.Status)
		}
	}
}

func launchServeHTTPClient(socketPath string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return new(net.Dialer).DialContext(ctx, "unix", socketPath)
		},
	}}
}

type launchServeTestResolver struct{}

func (launchServeTestResolver) ResolveModel(context.Context, agent.Spec) (llmagent.Model, error) {
	return adapterllm.ScriptedModel{Response: adapterllm.Response{Message: "launch target response"}}, nil
}

func (launchServeTestResolver) ResolveModelWithSpec(context.Context, agent.Spec) (agentfactory.ModelResolution, error) {
	return agentfactory.ModelResolution{
		Model: adapterllm.ScriptedModel{Response: adapterllm.Response{Message: "launch target response"}},
		Spec:  corellm.ModelSpec{Ref: corellm.ModelRef{Provider: "test", Name: "scripted"}},
	}, nil
}
