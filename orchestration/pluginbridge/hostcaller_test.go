package pluginbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
	"github.com/fluxplane/fluxplane-plugin/protocol"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/memsystem"
	"github.com/fluxplane/fluxplane-system/systemkit"
)

func TestSystemHostCallerEnvLookup(t *testing.T) {
	sys := memsystem.New()
	sys.Environment().(*memsystem.Environment).Set("TOKEN", "secret")
	caller := NewSystemHostCaller(sys)

	raw, err := caller.CallHost(protocol.HostCapabilityEnvLookup, sdkhost.EnvLookupRequest{Key: "TOKEN"})
	if err != nil {
		t.Fatalf("CallHost: %v", err)
	}
	var resp sdkhost.EnvLookupResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !resp.Found || resp.Key != "TOKEN" || resp.Value != "secret" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestSystemHostCallerBlobReadWriteInfo(t *testing.T) {
	sys := memsystem.New()
	caller := NewSystemHostCaller(sys)

	raw, err := caller.CallHost(protocol.HostCapabilityBlobWrite, sdkhost.BlobWriteRequest{
		Path:      "notes/todo.txt",
		Content:   []byte("abcdef"),
		MediaType: "text/plain",
		Overwrite: true,
	})
	if err != nil {
		t.Fatalf("BlobWrite: %v", err)
	}
	var writeResp sdkhost.BlobRef
	if err := json.Unmarshal(raw, &writeResp); err != nil {
		t.Fatalf("Unmarshal write: %v", err)
	}
	if writeResp.Path != "notes/todo.txt" || writeResp.Ref != "notes/todo.txt" || writeResp.Size != 6 || writeResp.MediaType != "text/plain" {
		t.Fatalf("write response = %#v", writeResp)
	}

	raw, err = caller.CallHost(protocol.HostCapabilityBlobRead, sdkhost.BlobReadRequest{Path: "notes/todo.txt", MaxBytes: 3})
	if err != nil {
		t.Fatalf("BlobRead: %v", err)
	}
	var readResp sdkhost.BlobReadResponse
	if err := json.Unmarshal(raw, &readResp); err != nil {
		t.Fatalf("Unmarshal read: %v", err)
	}
	if string(readResp.Content) != "abc" || !readResp.Truncated || readResp.Blob.Size != 6 {
		t.Fatalf("read response = %#v", readResp)
	}

	raw, err = caller.CallHost(protocol.HostCapabilityBlobInfo, sdkhost.BlobInfoRequest{Path: "notes/todo.txt"})
	if err != nil {
		t.Fatalf("BlobInfo: %v", err)
	}
	var infoResp sdkhost.BlobRef
	if err := json.Unmarshal(raw, &infoResp); err != nil {
		t.Fatalf("Unmarshal info: %v", err)
	}
	if infoResp.Path != "notes/todo.txt" || infoResp.Size != 6 {
		t.Fatalf("info response = %#v", infoResp)
	}
}

func TestSystemHostCallerProcessRun(t *testing.T) {
	process := &recordingProcessManager{result: fpsystem.ProcessResult{
		Command:         "git",
		Args:            []string{"status"},
		Workdir:         "repo",
		ExitCode:        0,
		Duration:        1500 * time.Millisecond,
		Stdout:          "clean",
		Stderr:          "warn",
		StdoutTruncated: true,
	}}
	facade, err := systemkit.NewSystem().
		WithFileSystem(memsystem.NewFileSystem()).
		WithNetwork(&recordingHTTPNetwork{}).
		WithEnvironment(memsystem.NewEnvironment(nil)).
		WithProcess(process).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	caller := NewSystemHostCaller(facade)

	raw, err := caller.CallHost(protocol.HostCapabilityProcessRun, sdkhost.ProcessRunRequest{
		Command:   "git",
		Args:      []string{"status"},
		Workdir:   "repo",
		Env:       []string{"GIT_CONFIG_NOSYSTEM=1"},
		TimeoutMS: 2000,
		MaxStdout: 100,
		MaxStderr: 50,
		Label:     "git-status",
		Group:     "git",
		Tags:      []string{"repo"},
		Metadata:  map[string]string{"purpose": "test"},
	})
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if process.request.Command != "git" || process.request.Workdir != "repo" || process.request.Timeout != 2*time.Second || process.request.MaxStdout != 100 || process.request.MaxStderr != 50 {
		t.Fatalf("request = %#v", process.request)
	}
	if len(process.request.Args) != 1 || process.request.Args[0] != "status" || len(process.request.Env) != 1 || process.request.Env[0] != "GIT_CONFIG_NOSYSTEM=1" {
		t.Fatalf("request args/env = %#v", process.request)
	}
	if process.request.Label != "git-status" || process.request.Group != "git" || len(process.request.Tags) != 1 || process.request.Tags[0] != "repo" || process.request.Metadata["purpose"] != "test" {
		t.Fatalf("request metadata = %#v", process.request)
	}
	var resp sdkhost.ProcessRunResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.Command != "git" || resp.ExitCode != 0 || resp.DurationMS != 1500 || resp.Stdout != "clean" || resp.Stderr != "warn" || !resp.StdoutTruncated {
		t.Fatalf("response = %#v", resp)
	}
}

func TestSystemHostCallerHTTP(t *testing.T) {
	network := &recordingHTTPNetwork{}
	facade, err := systemkit.NewSystem().
		WithFileSystem(memsystem.NewFileSystem()).
		WithNetwork(network).
		WithEnvironment(memsystem.NewEnvironment(nil)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	caller := NewSystemHostCaller(facade)

	raw, err := caller.CallHost(protocol.HostCapabilityHTTPDo, sdkhost.HTTPRequest{
		URL:       "https://example.test/api",
		Path:      "items",
		Query:     map[string][]string{"q": {"hello"}},
		Method:    "POST",
		Headers:   map[string]string{"X-Test": "yes"},
		Body:      []byte("body"),
		MaxBytes:  10,
		UserAgent: "core-test",
	})
	if err != nil {
		t.Fatalf("HTTPDo: %v", err)
	}
	var resp sdkhost.HTTPResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if network.request.URL != "https://example.test/api/items?q=hello" || network.request.Method != "POST" || network.request.UserAgent != "core-test" {
		t.Fatalf("request = %#v", network.request)
	}
	if string(network.request.Body) != "body" || network.request.Headers["X-Test"] != "yes" || network.request.MaxBytes != 10 {
		t.Fatalf("request details = %#v", network.request)
	}
	if resp.StatusCode != 201 || string(resp.Body) != "ok" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestSystemHostCallerHTTPResolvesBearerAuth(t *testing.T) {
	network := &recordingHTTPNetwork{}
	caller := NewSystemHostCaller(
		testHTTPSystem(t, network),
		WithPluginIdentity("slack", "work"),
		WithSecretResolver(secretResolver(map[sharedsecret.Ref]string{
			sharedsecret.Plugin("slack", "work", "bot_token"): "xoxb-test",
			sharedsecret.Plugin("slack", "work", "trace_id"):  "trace-1",
		})),
	)

	_, err := caller.CallHost(protocol.HostCapabilityHTTPDo, sdkhost.HTTPRequest{
		URL:    "https://example.test/api",
		Method: "GET",
		Auth: &sdkhost.HTTPAuthRequest{
			BearerTokenPurpose: "bot_token",
			HeaderPurposes:     map[string]string{"X-Trace-ID": "trace_id"},
		},
	})
	if err != nil {
		t.Fatalf("HTTPDo: %v", err)
	}
	if network.request.Headers["Authorization"] != "Bearer xoxb-test" || network.request.Headers["X-Trace-ID"] != "trace-1" {
		t.Fatalf("headers = %#v", network.request.Headers)
	}
}

func TestSystemHostCallerSecretGet(t *testing.T) {
	caller := NewSystemHostCaller(
		memsystem.New(),
		WithPluginIdentity("openapi", "helpdesk"),
		WithSecretResolver(secretResolver(map[sharedsecret.Ref]string{
			sharedsecret.Plugin("openapi", "helpdesk", "api_key"): "secret-key",
		})),
	)

	raw, err := caller.CallHost(sdkhost.SecretGetCommand, map[string]any{"purpose": "api_key"})
	if err != nil {
		t.Fatalf("SecretGet: %v", err)
	}
	var material sdkhost.SecretMaterial
	if err := json.Unmarshal(raw, &material); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if material.Purpose != "api_key" || material.Value != "secret-key" {
		t.Fatalf("material = %#v", material)
	}
}

func TestSystemHostCallerHTTPResolvesBasicAuth(t *testing.T) {
	network := &recordingHTTPNetwork{}
	caller := NewSystemHostCaller(
		testHTTPSystem(t, network),
		WithPluginIdentity("sql", "default"),
		WithSecretResolver(secretResolver(map[sharedsecret.Ref]string{
			sharedsecret.Plugin("sql", "default", "username"): "user",
			sharedsecret.Plugin("sql", "default", "password"): "pass",
		})),
	)

	_, err := caller.CallHost(protocol.HostCapabilityHTTPDo, sdkhost.HTTPRequest{
		URL: "https://example.test/api",
		Auth: &sdkhost.HTTPAuthRequest{
			UsernamePurpose: "username",
			PasswordPurpose: "password",
		},
	})
	if err != nil {
		t.Fatalf("HTTPDo: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if network.request.Headers["Authorization"] != want {
		t.Fatalf("Authorization = %q, want %q", network.request.Headers["Authorization"], want)
	}
}

func TestSystemHostCallerProviderCall(t *testing.T) {
	caller := NewSystemHostCaller(memsystem.New(), WithProviderCallHandler(func(_ context.Context, req sdkhost.ProviderCallRequest) (sdkhost.ProviderCallResponse, error) {
		if req.Provider != "runtime" || req.Action != "ping" {
			t.Fatalf("request = %#v", req)
		}
		return sdkhost.ProviderCallResponse{Result: json.RawMessage(`{"pong":true}`)}, nil
	}))

	raw, err := caller.CallHost(protocol.HostCapabilityProviderCall, sdkhost.ProviderCallRequest{Provider: "runtime", Action: "ping"})
	if err != nil {
		t.Fatalf("ProviderCall: %v", err)
	}
	var resp sdkhost.ProviderCallResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(resp.Result) != `{"pong":true}` {
		t.Fatalf("response = %#v", resp)
	}
}

func TestSystemHostCallerSystemInfoProvider(t *testing.T) {
	sys := memsystem.New()
	caller := NewSystemHostCaller(sys, WithSystemInfoProvider())

	raw, err := caller.CallHost(protocol.HostCapabilityProviderCall, sdkhost.ProviderCallRequest{
		Provider: "system",
		Action:   "info",
		Payload:  json.RawMessage(`{"categories":["time","env"]}`),
	})
	if err != nil {
		t.Fatalf("ProviderCall: %v", err)
	}
	var resp sdkhost.ProviderCallResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("Unmarshal response: %v", err)
	}
	var result struct {
		Categories []string       `json:"categories"`
		Generated  string         `json:"generated_at"`
		System     map[string]any `json:"system"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if len(result.Categories) != 2 || result.Categories[0] != "time" || result.Categories[1] != "env" || result.Generated == "" {
		t.Fatalf("result = %#v", result)
	}
	if _, ok := result.System["time"]; !ok {
		t.Fatalf("system info missing time: %#v", result.System)
	}
	if _, ok := result.System["env"]; !ok {
		t.Fatalf("system info missing env: %#v", result.System)
	}
}

func testHTTPSystem(t *testing.T, network fpsystem.Network) fpsystem.System {
	t.Helper()
	facade, err := systemkit.NewSystem().
		WithFileSystem(memsystem.NewFileSystem()).
		WithNetwork(network).
		WithEnvironment(memsystem.NewEnvironment(nil)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return facade
}

func secretResolver(values map[sharedsecret.Ref]string) sharedsecret.Resolver {
	return sharedsecret.ResolverFunc(func(_ context.Context, ref sharedsecret.Ref) (sharedsecret.Material, bool, error) {
		for want, value := range values {
			if want.Normalize() == ref.Normalize() {
				return sharedsecret.Material{Ref: ref.Normalize(), Value: []byte(value)}, true, nil
			}
		}
		return sharedsecret.Material{}, false, nil
	})
}

type recordingHTTPNetwork struct {
	request systemkit.HTTPRequest
}

type recordingProcessManager struct {
	request fpsystem.ProcessRequest
	result  fpsystem.ProcessResult
}

func (p *recordingProcessManager) Run(_ context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessResult, error) {
	p.request = req
	return p.result, nil
}

func (*recordingProcessManager) Start(context.Context, fpsystem.ProcessRequest) (fpsystem.ProcessHandle, error) {
	return nil, errors.ErrUnsupported
}

func (*recordingProcessManager) Ensure(context.Context, fpsystem.ProcessRequest) (fpsystem.ProcessHandle, bool, error) {
	return nil, false, errors.ErrUnsupported
}

func (*recordingProcessManager) Group(string) fpsystem.ProcessGroup {
	return unsupportedProcessGroup{}
}

func (*recordingProcessManager) List(context.Context) ([]fpsystem.ProcessInfo, error) {
	return nil, errors.ErrUnsupported
}

type unsupportedProcessGroup struct{}

func (unsupportedProcessGroup) Name() string { return "" }
func (unsupportedProcessGroup) List(context.Context) ([]fpsystem.ProcessInfo, error) {
	return nil, errors.ErrUnsupported
}
func (unsupportedProcessGroup) Subscribe(context.Context) <-chan fpsystem.ProcessEvent { return nil }
func (unsupportedProcessGroup) Wait(context.Context) (fpsystem.ProcessResult, error) {
	return fpsystem.ProcessResult{}, errors.ErrUnsupported
}
func (unsupportedProcessGroup) Stop(context.Context) error { return errors.ErrUnsupported }
func (unsupportedProcessGroup) Kill(context.Context) error { return errors.ErrUnsupported }
func (unsupportedProcessGroup) Signal(context.Context, fpsystem.ProcessSignal) error {
	return errors.ErrUnsupported
}
func (unsupportedProcessGroup) Interrupt(context.Context) error { return errors.ErrUnsupported }
func (unsupportedProcessGroup) Reload(context.Context) error    { return errors.ErrUnsupported }
func (unsupportedProcessGroup) Pause(context.Context) error     { return errors.ErrUnsupported }
func (unsupportedProcessGroup) Resume(context.Context) error    { return errors.ErrUnsupported }
func (unsupportedProcessGroup) Write(context.Context, []byte) (int, error) {
	return 0, errors.ErrUnsupported
}
func (unsupportedProcessGroup) CloseInput(context.Context) error { return errors.ErrUnsupported }
func (unsupportedProcessGroup) Restart(context.Context) (fpsystem.ProcessHandle, error) {
	return nil, errors.ErrUnsupported
}
func (unsupportedProcessGroup) Detach(context.Context) error { return errors.ErrUnsupported }

func (n *recordingHTTPNetwork) DoHTTP(_ context.Context, req systemkit.HTTPRequest) (systemkit.HTTPResponse, error) {
	n.request = req
	return systemkit.HTTPResponse{
		URL:        req.URL,
		FinalURL:   req.URL,
		Method:     req.Method,
		Status:     "201 Created",
		StatusCode: 201,
		Headers:    map[string][]string{"Content-Type": {"text/plain"}},
		Body:       []byte("ok"),
	}, nil
}

func (*recordingHTTPNetwork) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.ErrUnsupported
}

func (*recordingHTTPNetwork) Resolver() fpsystem.Resolver {
	return unsupportedResolver{}
}

type unsupportedResolver struct{}

func (unsupportedResolver) LookupHost(context.Context, string) ([]string, error) {
	return nil, errors.ErrUnsupported
}

func (unsupportedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return nil, errors.ErrUnsupported
}

func (unsupportedResolver) LookupCNAME(context.Context, string) (string, error) {
	return "", errors.ErrUnsupported
}

func (unsupportedResolver) LookupMX(context.Context, string) ([]*net.MX, error) {
	return nil, errors.ErrUnsupported
}

func (unsupportedResolver) LookupSRV(context.Context, string, string, string) (string, []*net.SRV, error) {
	return "", nil, errors.ErrUnsupported
}

func (unsupportedResolver) LookupTXT(context.Context, string) ([]string, error) {
	return nil, errors.ErrUnsupported
}
