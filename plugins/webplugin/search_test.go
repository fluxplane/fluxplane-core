package webplugin

import (
	"context"

	"github.com/fluxplane/agentruntime/runtime/system"
)

type testSystem struct {
	workspace system.Workspace
	network   *testNetwork
	env       system.Environment
}

func (s testSystem) Workspace() system.Workspace     { return s.workspace }
func (s testSystem) Network() system.Network         { return s.network }
func (s testSystem) Process() system.ProcessManager  { return nil }
func (s testSystem) Browser() system.BrowserManager  { return nil }
func (s testSystem) Clarifier() system.Clarifier     { return nil }
func (s testSystem) Environment() system.Environment { return s.env }

type testNetwork struct {
	requests []system.HTTPRequest
	response system.HTTPResponse
	err      error
}

func (n *testNetwork) DoHTTP(_ context.Context, req system.HTTPRequest) (system.HTTPResponse, error) {
	n.requests = append(n.requests, req)
	return n.response, n.err
}

func (n *testNetwork) lastRequest() system.HTTPRequest {
	if len(n.requests) == 0 {
		return system.HTTPRequest{}
	}
	return n.requests[len(n.requests)-1]
}

type testEnvironment map[string]string

func (e testEnvironment) Getenv(key string) string { return e[key] }
