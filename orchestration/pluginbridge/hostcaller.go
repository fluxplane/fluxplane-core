package pluginbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
	"github.com/fluxplane/fluxplane-plugin/protocol"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/hostinfo"
	"github.com/fluxplane/fluxplane-system/systemkit"
)

// ProviderCallHandler handles plugin host provider calls that are backed by
// Core or product runtime services.
type ProviderCallHandler func(context.Context, sdkhost.ProviderCallRequest) (sdkhost.ProviderCallResponse, error)

// HostCallerOption configures a Core host caller.
type HostCallerOption func(*systemHostCaller)

// WithHostCallerContext sets the context used for host capability calls.
func WithHostCallerContext(ctx context.Context) HostCallerOption {
	return func(c *systemHostCaller) {
		if ctx != nil {
			c.ctx = ctx
		}
	}
}

// WithProviderCallHandler wires product/Core service calls into plugin host
// provider-call capabilities.
func WithProviderCallHandler(handler ProviderCallHandler) HostCallerOption {
	return func(c *systemHostCaller) {
		c.providerCall = handler
	}
}

// WithSecretResolver lets the host caller resolve SDK HTTP auth purposes
// against Core/plugin-scoped secret material.
func WithSecretResolver(resolver sharedsecret.Resolver) HostCallerOption {
	return func(c *systemHostCaller) {
		c.secrets = resolver
	}
}

// WithPluginIdentity sets the plugin instance used for plugin-scoped secret
// refs such as plugin/slack/default/bot_token.
func WithPluginIdentity(plugin, instance string) HostCallerOption {
	return func(c *systemHostCaller) {
		c.plugin = strings.TrimSpace(plugin)
		c.instance = strings.TrimSpace(instance)
	}
}

// WithEndpointRegistry lets SDK HTTP calls resolve endpoint refs through the
// Core runtime endpoint registry.
func WithEndpointRegistry(registry *fpendpoint.Registry) HostCallerOption {
	return func(c *systemHostCaller) {
		c.endpoints = registry
	}
}

// WithSystemInfoProvider handles provider call system.info using
// fluxplane-system host metadata.
func WithSystemInfoProvider() HostCallerOption {
	return func(c *systemHostCaller) {
		c.providerCall = c.systemInfoProvider
	}
}

// NewSystemHostCaller adapts a fluxplane-system boundary into the SDK host
// capability protocol expected by fluxplane-plugin runtimes.
func NewSystemHostCaller(sys fpsystem.System, opts ...HostCallerOption) protocol.HostCaller {
	caller := &systemHostCaller{ctx: context.Background()}
	if sys != nil {
		caller.system = sys
		caller.fileSystem = sys.FileSystem()
		caller.network = sys.Network()
		caller.environment = sys.Environment()
		caller.process = sys.Process()
	}
	for _, opt := range opts {
		if opt != nil {
			opt(caller)
		}
	}
	return caller
}

// NewSystemHostCallerFactory returns a pluginbridge host-caller factory backed
// by the same Core system boundary for each plugin instance.
func NewSystemHostCallerFactory(sys fpsystem.System, opts ...HostCallerOption) HostCallerFactory {
	return func(ctx contributions.Context) protocol.HostCaller {
		factoryOpts := append([]HostCallerOption(nil), opts...)
		factoryOpts = append(factoryOpts, WithPluginIdentity(ctx.Ref.Name, ctx.Ref.InstanceName()), WithSecretResolver(ctx.Secrets))
		return NewSystemHostCaller(sys, factoryOpts...)
	}
}

type systemHostCaller struct {
	ctx          context.Context
	plugin       string
	instance     string
	system       fpsystem.System
	fileSystem   fpsystem.FileSystem
	network      fpsystem.Network
	environment  fpsystem.Environment
	process      fpsystem.ProcessManager
	endpoints    *fpendpoint.Registry
	secrets      sharedsecret.Resolver
	providerCall ProviderCallHandler
}

func (c *systemHostCaller) CallHost(command string, payload any) (json.RawMessage, error) {
	switch strings.TrimSpace(command) {
	case sdkhost.SecretGetCommand:
		return c.secretGet(payload)
	case protocol.HostCapabilityEnvLookup:
		return c.envLookup(payload)
	case protocol.HostCapabilityProcessRun:
		return c.processRun(payload)
	case protocol.HostCapabilityHTTPDo:
		return c.httpDo(payload)
	case sdkhost.EndpointResolve:
		return c.endpointResolve(payload)
	case protocol.HostCapabilityBlobRead:
		return c.blobRead(payload)
	case protocol.HostCapabilityBlobWrite:
		return c.blobWrite(payload)
	case protocol.HostCapabilityBlobInfo:
		return c.blobInfo(payload)
	case protocol.HostCapabilityProviderCall:
		return c.callProvider(payload)
	default:
		return nil, protocol.HostError{Code: "unsupported_host_capability", Message: fmt.Sprintf("unsupported host capability %q", command)}
	}
}

func (*systemHostCaller) EmitHostEvent(string, any) error {
	return nil
}

func (c *systemHostCaller) secretGet(payload any) (json.RawMessage, error) {
	var req struct {
		Purpose string `json:"purpose"`
	}
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	purpose := strings.TrimSpace(req.Purpose)
	value, _, err := c.resolveSecretPurpose(purpose)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sdkhost.SecretMaterial{Purpose: purpose, Value: value})
}

func (c *systemHostCaller) processRun(payload any) (json.RawMessage, error) {
	if c.process == nil {
		return nil, protocol.HostError{Code: "host_process_unavailable", Message: "host process manager is unavailable"}
	}
	var req sdkhost.ProcessRunRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	result, err := c.process.Run(c.callContext(), fpsystem.ProcessRequest{
		Command:   req.Command,
		Args:      append([]string(nil), req.Args...),
		Workdir:   req.Workdir,
		Env:       append([]string(nil), req.Env...),
		Timeout:   time.Duration(req.TimeoutMS) * time.Millisecond,
		MaxStdout: req.MaxStdout,
		MaxStderr: req.MaxStderr,
		Label:     req.Label,
		Group:     req.Group,
		Tags:      append([]string(nil), req.Tags...),
		Metadata:  cloneStringMap(req.Metadata),
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(sdkhost.ProcessRunResponse{
		Command:         result.Command,
		Args:            append([]string(nil), result.Args...),
		Workdir:         result.Workdir,
		ExitCode:        result.ExitCode,
		TimedOut:        result.TimedOut,
		DurationMS:      result.Duration.Milliseconds(),
		Stdout:          result.Stdout,
		Stderr:          result.Stderr,
		StdoutTruncated: result.StdoutTruncated,
		StderrTruncated: result.StderrTruncated,
	})
}

func (c *systemHostCaller) envLookup(payload any) (json.RawMessage, error) {
	if c.environment == nil {
		return nil, protocol.HostError{Code: "host_environment_unavailable", Message: "host environment is unavailable"}
	}
	var req sdkhost.EnvLookupRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(req.Key)
	value, found, err := c.environment.Lookup(c.callContext(), key)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sdkhost.EnvLookupResponse{Key: key, Value: value, Found: found})
}

func (c *systemHostCaller) httpDo(payload any) (json.RawMessage, error) {
	if c.network == nil {
		return nil, protocol.HostError{Code: "host_network_unavailable", Message: "host network is unavailable"}
	}
	var req sdkhost.HTTPRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	urlString, err := c.hostHTTPURL(req)
	if err != nil {
		return nil, err
	}
	headers, err := c.resolveHTTPAuth(req.Headers, req.Auth)
	if err != nil {
		return nil, err
	}
	resp, err := systemkit.DoHTTP(c.callContext(), c.network, systemkit.HTTPRequest{
		URL:       urlString,
		Method:    req.Method,
		Headers:   headers,
		Body:      req.Body,
		Timeout:   time.Duration(req.TimeoutMS) * time.Millisecond,
		MaxBytes:  req.MaxBytes,
		UserAgent: req.UserAgent,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(sdkhost.HTTPResponse{
		URL:         resp.URL,
		FinalURL:    resp.FinalURL,
		Method:      resp.Method,
		Status:      resp.Status,
		StatusCode:  resp.StatusCode,
		Headers:     resp.Headers,
		ContentType: resp.ContentType,
		Body:        resp.Body,
		Truncated:   resp.Truncated,
		DurationMS:  resp.Duration.Milliseconds(),
	})
}

func (c *systemHostCaller) endpointResolve(payload any) (json.RawMessage, error) {
	var req struct {
		EndpointRef string `json:"endpoint_ref"`
	}
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	resolved, ok := c.resolveEndpoint(req.EndpointRef)
	if !ok {
		return nil, protocol.HostError{Code: "host_endpoint_not_found", Message: fmt.Sprintf("endpoint %q is not registered", strings.TrimSpace(req.EndpointRef))}
	}
	return json.Marshal(fpendpoint.EndpointRef{
		ID:       resolved.Ref.ID(),
		URL:      resolved.URL,
		Product:  resolved.Metadata["product"],
		Protocol: endpointProtocol(resolved.URL),
		Source:   resolved.Source.Kind,
	})
}

func (c *systemHostCaller) blobRead(payload any) (json.RawMessage, error) {
	if c.fileSystem == nil {
		return nil, protocol.HostError{Code: "host_filesystem_unavailable", Message: "host filesystem is unavailable"}
	}
	var req sdkhost.BlobReadRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	name, err := blobPath(req.Ref, req.Path)
	if err != nil {
		return nil, err
	}
	content, truncated, err := fpsystem.ReadFileLimit(c.callContext(), c.fileSystem, name, req.MaxBytes)
	if err != nil {
		return nil, err
	}
	ref := c.blobRef(req.Ref, name, nil)
	if ref.Size == 0 {
		ref.Size = int64(len(content))
	}
	return json.Marshal(sdkhost.BlobReadResponse{Blob: ref, Content: content, Truncated: truncated})
}

func (c *systemHostCaller) blobWrite(payload any) (json.RawMessage, error) {
	if c.fileSystem == nil {
		return nil, protocol.HostError{Code: "host_filesystem_unavailable", Message: "host filesystem is unavailable"}
	}
	var req sdkhost.BlobWriteRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	name, err := blobPath(req.Ref, req.Path)
	if err != nil {
		return nil, err
	}
	if err := c.fileSystem.WriteFile(c.callContext(), name, req.Content, fpsystem.WriteFileOptions{Overwrite: req.Overwrite}); err != nil {
		return nil, err
	}
	return json.Marshal(c.blobRef(req.Ref, name, &req))
}

func (c *systemHostCaller) blobInfo(payload any) (json.RawMessage, error) {
	if c.fileSystem == nil {
		return nil, protocol.HostError{Code: "host_filesystem_unavailable", Message: "host filesystem is unavailable"}
	}
	var req sdkhost.BlobInfoRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	name, err := blobPath(req.Ref, req.Path)
	if err != nil {
		return nil, err
	}
	return json.Marshal(c.blobRef(req.Ref, name, nil))
}

func (c *systemHostCaller) callProvider(payload any) (json.RawMessage, error) {
	if c.providerCall == nil {
		return nil, protocol.HostError{Code: "host_provider_unavailable", Message: "host provider calls are unavailable"}
	}
	var req sdkhost.ProviderCallRequest
	if err := decodeHostPayload(payload, &req); err != nil {
		return nil, err
	}
	resp, err := c.providerCall(c.callContext(), req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resp)
}

func (c *systemHostCaller) resolveHTTPAuth(headers map[string]string, auth *sdkhost.HTTPAuthRequest) (map[string]string, error) {
	headers = cloneHTTPHeaders(headers)
	if auth == nil {
		return headers, nil
	}
	if strings.TrimSpace(headers["Authorization"]) != "" {
		return c.resolveHTTPHeaderPurposes(headers, auth.HeaderPurposes), nil
	}
	if tokenPurpose := strings.TrimSpace(auth.BearerTokenPurpose); tokenPurpose != "" {
		if value, ok, err := c.resolveSecretPurpose(tokenPurpose); err != nil {
			return nil, err
		} else if ok {
			headers = withHTTPHeader(headers, "Authorization", "Bearer "+value)
			return c.resolveHTTPHeaderPurposes(headers, auth.HeaderPurposes), nil
		}
	}
	usernamePurpose := strings.TrimSpace(auth.UsernamePurpose)
	passwordPurpose := strings.TrimSpace(auth.PasswordPurpose)
	if usernamePurpose == "" && passwordPurpose == "" {
		return c.resolveHTTPHeaderPurposes(headers, auth.HeaderPurposes), nil
	}
	var username, password string
	if usernamePurpose != "" {
		if value, ok, err := c.resolveSecretPurpose(usernamePurpose); err != nil {
			return nil, err
		} else if ok {
			username = value
		}
	}
	if passwordPurpose != "" {
		if value, ok, err := c.resolveSecretPurpose(passwordPurpose); err != nil {
			return nil, err
		} else if ok {
			password = value
		}
	}
	if username == "" && password == "" {
		return c.resolveHTTPHeaderPurposes(headers, auth.HeaderPurposes), nil
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return c.resolveHTTPHeaderPurposes(withHTTPHeader(headers, "Authorization", "Basic "+encoded), auth.HeaderPurposes), nil
}

func (c *systemHostCaller) resolveHTTPHeaderPurposes(headers map[string]string, purposes map[string]string) map[string]string {
	for header, purpose := range purposes {
		header = strings.TrimSpace(header)
		purpose = strings.TrimSpace(purpose)
		if header == "" || purpose == "" || strings.TrimSpace(headers[header]) != "" {
			continue
		}
		if value, ok, err := c.resolveSecretPurpose(purpose); err == nil && ok {
			headers = withHTTPHeader(headers, header, value)
		}
	}
	return headers
}

func (c *systemHostCaller) resolveSecretPurpose(purpose string) (string, bool, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" || c.secrets == nil || strings.TrimSpace(c.plugin) == "" {
		return "", false, nil
	}
	material, ok, err := c.secrets.ResolveSecret(c.callContext(), sharedsecret.Plugin(c.plugin, c.instance, sharedsecret.Slot(purpose)))
	if err != nil || !ok {
		return "", false, err
	}
	value := strings.TrimSpace(material.String())
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func cloneHTTPHeaders(headers map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range headers {
		out[key] = value
	}
	return out
}

func withHTTPHeader(headers map[string]string, key, value string) map[string]string {
	if headers == nil {
		headers = map[string]string{}
	}
	headers[key] = value
	return headers
}

func (c *systemHostCaller) systemInfoProvider(ctx context.Context, req sdkhost.ProviderCallRequest) (sdkhost.ProviderCallResponse, error) {
	if strings.TrimSpace(req.Provider) != "system" || strings.TrimSpace(req.Action) != "info" {
		return sdkhost.ProviderCallResponse{}, protocol.HostError{Code: "unsupported_provider_call", Message: fmt.Sprintf("unsupported provider call %s.%s", req.Provider, req.Action)}
	}
	var input struct {
		Categories []string `json:"categories,omitempty"`
	}
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &input); err != nil {
			return sdkhost.ProviderCallResponse{}, err
		}
	}
	categories := make([]hostinfo.Category, 0, len(input.Categories))
	for _, category := range input.Categories {
		if strings.TrimSpace(category) != "" {
			categories = append(categories, hostinfo.Category(strings.TrimSpace(category)))
		}
	}
	info, err := hostinfo.Collect(ctx, c.system, hostinfo.Request{Categories: categories})
	if err != nil {
		return sdkhost.ProviderCallResponse{}, err
	}
	system := map[string]any{}
	addHostInfoCategory(system, "os", info.OS)
	addHostInfoCategory(system, "runtime", info.Runtime)
	addHostInfoCategory(system, "user", info.User)
	addHostInfoCategory(system, "paths", info.Paths)
	addHostInfoCategory(system, "cpu", info.CPU)
	addHostInfoCategory(system, "time", info.Time)
	addHostInfoCategory(system, "env", info.Env)
	addHostInfoCategory(system, "network", info.Network)
	result, err := json.Marshal(map[string]any{
		"categories":   hostInfoCategoryNames(categories),
		"generated_at": info.GeneratedAt.Format(time.RFC3339Nano),
		"system":       system,
	})
	if err != nil {
		return sdkhost.ProviderCallResponse{}, err
	}
	return sdkhost.ProviderCallResponse{Result: result}, nil
}

func (c *systemHostCaller) blobRef(ref, name string, write *sdkhost.BlobWriteRequest) sdkhost.BlobRef {
	out := sdkhost.BlobRef{Ref: strings.TrimSpace(ref), Path: name}
	if out.Ref == "" {
		out.Ref = name
	}
	if c.fileSystem != nil {
		if info, err := c.fileSystem.Stat(name); err == nil {
			out.Size = info.Size()
			if strings.TrimSpace(out.Path) == "" {
				out.Path = info.Name()
			}
		}
	}
	if write != nil {
		out.MediaType = strings.TrimSpace(write.MediaType)
		out.Filename = strings.TrimSpace(write.Filename)
		out.Metadata = write.Metadata
	}
	return out
}

func (c *systemHostCaller) callContext() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

func decodeHostPayload(payload any, out any) error {
	if out == nil {
		return nil
	}
	if raw, ok := payload.(json.RawMessage); ok {
		return json.Unmarshal(raw, out)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (c *systemHostCaller) hostHTTPURL(req sdkhost.HTTPRequest) (string, error) {
	raw := strings.TrimSpace(req.URL)
	if raw == "" {
		resolved, ok := c.resolveEndpoint(req.EndpointRef)
		if !ok {
			return "", protocol.HostError{Code: "host_endpoint_not_found", Message: fmt.Sprintf("endpoint %q is not registered", strings.TrimSpace(req.EndpointRef))}
		}
		raw = resolved.URL
	}
	if raw == "" {
		return "", protocol.HostError{Code: "host_http_url_required", Message: "host HTTP URL is required"}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Path) != "" {
		joined, err := url.JoinPath(parsed.String(), req.Path)
		if err != nil {
			return "", err
		}
		parsed, err = url.Parse(joined)
		if err != nil {
			return "", err
		}
	}
	query := parsed.Query()
	for key, values := range req.Query {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (c *systemHostCaller) resolveEndpoint(ref string) (fpendpoint.Resolved, bool) {
	if c.endpoints == nil {
		return fpendpoint.Resolved{}, false
	}
	return c.endpoints.Resolve(fpendpoint.ParseRef(ref))
}

func endpointProtocol(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Scheme
}

func blobPath(ref, path string) (string, error) {
	name := strings.TrimSpace(path)
	if name == "" {
		name = strings.TrimSpace(ref)
	}
	if name == "" {
		return "", protocol.HostError{Code: "host_blob_path_required", Message: "host blob path or ref is required"}
	}
	return name, nil
}

func addHostInfoCategory(out map[string]any, name string, value map[string]any) {
	if len(value) > 0 {
		out[name] = value
	}
}

func hostInfoCategoryNames(categories []hostinfo.Category) []string {
	if len(categories) == 0 {
		categories = []hostinfo.Category{
			hostinfo.CategoryOS,
			hostinfo.CategoryRuntime,
			hostinfo.CategoryUser,
			hostinfo.CategoryPaths,
			hostinfo.CategoryCPU,
			hostinfo.CategoryTime,
			hostinfo.CategoryEnv,
			hostinfo.CategoryNetwork,
		}
	}
	out := make([]string, 0, len(categories))
	for _, category := range categories {
		if strings.TrimSpace(string(category)) != "" {
			out = append(out, string(category))
		}
	}
	return out
}
