package codershell

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestNewClientSelectsConnectEndpoints(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	if _, ok := newClient(sys, Options{}).(*DirectChannelClient); !ok {
		t.Fatalf("default client is %T, want *DirectChannelClient", newClient(sys, Options{}))
	}
	if _, ok := newClient(sys, Options{Connect: "direct"}).(*DirectChannelClient); !ok {
		t.Fatalf("direct client is %T, want *DirectChannelClient", newClient(sys, Options{Connect: "direct"}))
	}
	if _, ok := newClient(sys, Options{Connect: "fake"}).(*FakeClient); !ok {
		t.Fatalf("fake client is %T, want *FakeClient", newClient(sys, Options{Connect: "fake"}))
	}
	local, ok := newClient(sys, Options{Connect: "unix:///tmp/coder.sock"}).(*DirectChannelClient)
	if !ok {
		t.Fatalf("unix client is %T, want *DirectChannelClient", newClient(sys, Options{Connect: "unix:///tmp/coder.sock"}))
	}
	if local.ConnectionDescription() != "direct-channel" {
		t.Fatalf("local connection = %q", local.ConnectionDescription())
	}
	remote, ok := newClient(sys, Options{Connect: "https://localhost:4321"}).(*DirectChannelClient)
	if !ok {
		t.Fatalf("remote client is %T, want *DirectChannelClient", newClient(sys, Options{Connect: "https://localhost:4321"}))
	}
	if remote.ConnectionDescription() != "direct-channel" {
		t.Fatalf("remote connection = %q", remote.ConnectionDescription())
	}
}

func TestRemoteClientUnavailableErrorsIncludeEndpoint(t *testing.T) {
	client := NewRemoteClient(RemoteClientOptions{Endpoint: "http://127.0.0.1:1234"})
	_, err := client.CreateSession(context.Background(), CreateSessionRequest{CWD: "."})
	if err == nil {
		t.Fatal("CreateSession() error is nil")
	}
	if !strings.Contains(err.Error(), "http://127.0.0.1:1234") || !strings.Contains(err.Error(), "no transport yet") {
		t.Fatalf("CreateSession() error = %v", err)
	}
}

func TestProtocolJSONRoundTrip(t *testing.T) {
	payload := AskSubmitPayload{
		Text: "summarize",
		CWD:  "/workspace",
		Context: []ContextItem{{
			Kind:    EventCommandOutput,
			Summary: "go version go1.2.3",
			Data:    map[string]string{"stream": "stdout"},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got AskSubmitPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Text != payload.Text || got.CWD != payload.CWD || len(got.Context) != 1 || got.Context[0].Kind != EventCommandOutput {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}
