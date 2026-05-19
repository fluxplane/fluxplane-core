package codershell

import "testing"

func TestParseConnectEndpoint(t *testing.T) {
	for _, tc := range []struct {
		value    string
		wantMode ClientMode
		want     string
	}{
		{"", ClientModeDirect, "direct"},
		{"direct", ClientModeDirect, "direct"},
		{"fake", ClientModeFake, "fake"},
		{"unix:///tmp/coder.sock", ClientModeLocal, "unix:///tmp/coder.sock"},
		{"https://localhost:4321", ClientModeRemote, "https://localhost:4321"},
		{"a2a://my-agents.com/foo", ClientModeRemote, "a2a://my-agents.com/foo"},
	} {
		got, err := parseConnectEndpoint(tc.value)
		if err != nil {
			t.Fatalf("parseConnectEndpoint(%q) error = %v", tc.value, err)
		}
		if got.Mode != tc.wantMode || got.Endpoint != tc.want {
			t.Fatalf("parseConnectEndpoint(%q) = %+v, want mode=%q endpoint=%q", tc.value, got, tc.wantMode, tc.want)
		}
	}
	if _, err := parseConnectEndpoint("bogus"); err == nil {
		t.Fatal("parseConnectEndpoint(bogus) error is nil")
	}
}

func TestNewCommandExposesConnectFlag(t *testing.T) {
	cmd := NewCommand()
	if cmd.Use != "shell [path]" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	if cmd.Flags().Lookup("connect") == nil {
		t.Fatal("--connect flag missing")
	}
}
