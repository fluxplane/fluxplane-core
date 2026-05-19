package codershell

import (
	"fmt"
	"net/url"
	"strings"
)

// ConnectEndpoint is the parsed form of --connect.
type ConnectEndpoint struct {
	Mode     ClientMode
	Endpoint string
}

func parseConnectEndpoint(value string) (ConnectEndpoint, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "direct":
		return ConnectEndpoint{Mode: ClientModeDirect, Endpoint: "direct"}, nil
	case "fake":
		return ConnectEndpoint{Mode: ClientModeFake, Endpoint: "fake"}, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return ConnectEndpoint{}, fmt.Errorf("unsupported shell endpoint %q; expected fake, direct, unix://PATH, http(s)://URL, or target URL", value)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "unix":
		return ConnectEndpoint{Mode: ClientModeLocal, Endpoint: value}, nil
	case "http", "https", "a2a":
		return ConnectEndpoint{Mode: ClientModeRemote, Endpoint: value}, nil
	default:
		return ConnectEndpoint{Mode: ClientModeRemote, Endpoint: value}, nil
	}
}
