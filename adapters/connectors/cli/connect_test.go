package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codewandler/connectors/connector"
	"github.com/codewandler/connectors/credential"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/orchestration/pluginhost"
)

func TestCommandHelpIsNativeCommand(t *testing.T) {
	cmd := NewCommand(testRegistry("slack"))
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "connect [provider]") {
		t.Fatalf("help = %q, want native provider argument", help)
	}
	for _, forbidden := range []string{"List available and connected connectors", "exec", "docs"} {
		if strings.Contains(help, forbidden) {
			t.Fatalf("help = %q, contains upstream connector CLI text %q", help, forbidden)
		}
	}
}

func TestRunStatusListsStoredInstances(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	instances := credential.NewInstanceStore(dir + "/instances")
	credentials := credential.NewFileStore(dir + "/credentials")
	if err := instances.Save(ctx, credential.Instance{
		ID:         "slack-prod",
		Connector:  "slack",
		AuthMethod: "token",
		Source:     "manual",
	}); err != nil {
		t.Fatalf("Save instance: %v", err)
	}
	if err := credentials.Save(ctx, "slack-prod", connector.Credentials{
		Auth: connector.AuthState{Kind: connector.AuthToken, Token: "xoxb-test"},
	}); err != nil {
		t.Fatalf("Save credentials: %v", err)
	}

	out := bytes.Buffer{}
	err := RunStatus(ctx, Options{ConnectorsPath: dir, Out: &out}, nil)
	if err != nil {
		t.Fatalf("RunStatus: %v", err)
	}
	got := out.String()
	for _, want := range []string{"PROVIDER", "slack", "slack-prod", "ok"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status = %q, want %q", got, want)
		}
	}
}

func TestRunProviderInfoUsesRegistry(t *testing.T) {
	out := bytes.Buffer{}
	err := RunProvider(context.Background(), "slack", Options{
		ConnectorsPath: t.TempDir(),
		Info:           true,
		Out:            &out,
	}, testRegistry("slack"))
	if err != nil {
		t.Fatalf("RunProvider: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Slack (slack)") || !strings.Contains(got, "Auth methods:") {
		t.Fatalf("info = %q, want slack connect info", got)
	}
}

func TestRunProviderRejectsInvalidField(t *testing.T) {
	err := RunProvider(context.Background(), "slack", Options{
		ConnectorsPath: t.TempDir(),
		Fields:         []string{"bad"},
		In:             strings.NewReader(""),
		Out:            &bytes.Buffer{},
	}, testRegistry("slack"))
	if err == nil || !strings.Contains(err.Error(), "invalid field") {
		t.Fatalf("RunProvider error = %v, want invalid field", err)
	}
}

func TestCredentialHealthReportsExpired(t *testing.T) {
	ctx := context.Background()
	store := credential.NewFileStore(t.TempDir())
	if err := store.Save(ctx, "slack-prod", connector.Credentials{
		Auth: connector.AuthState{
			Kind:      connector.AuthToken,
			Token:     "xoxb-test",
			ExpiresAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		},
	}); err != nil {
		t.Fatalf("Save credentials: %v", err)
	}
	if got := credentialHealth(ctx, store, "slack-prod"); got != "expired" {
		t.Fatalf("credentialHealth = %q, want expired", got)
	}
}

func TestRegisteredConnectorProviderNamesUsesRegistry(t *testing.T) {
	providers, err := registeredConnectorProviderNames(context.Background(), testRegistry("jira", "slack", "gitlab"))
	if err != nil {
		t.Fatalf("registeredConnectorProviderNames: %v", err)
	}
	got := strings.Join(providers, ",")
	if got != "gitlab,jira,slack" {
		t.Fatalf("providers = %q, want sorted unique list", got)
	}
}

func testRegistry(names ...string) PluginRegistry {
	return func(context.Context) ([]pluginhost.Plugin, error) {
		return []pluginhost.Plugin{fakeConnectorProviderPlugin{names: names}}, nil
	}
}

type fakeConnectorProviderPlugin struct {
	names []string
}

func (p fakeConnectorProviderPlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: "fake"}
}

func (fakeConnectorProviderPlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p fakeConnectorProviderPlugin) ConnectorProviders(context.Context, pluginhost.Context) ([]pluginhost.ConnectorProvider, error) {
	out := make([]pluginhost.ConnectorProvider, 0, len(p.names))
	for _, name := range p.names {
		out = append(out, pluginhost.ConnectorProvider{Name: name})
	}
	return out, nil
}
