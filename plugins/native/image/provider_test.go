package image

import (
	"context"
	"encoding/base64"
	"encoding/json"
	browser "github.com/fluxplane/fluxplane-browser"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/runtime/system"
)

func TestPollinationsGenerateUsesImageEndpoint(t *testing.T) {
	sys := newProviderTestSystem(t, map[string]string{}, system.HTTPResponse{
		Status:      "200 OK",
		StatusCode:  200,
		ContentType: "image/png",
		Body:        []byte{0x89, 0x50, 0x4e, 0x47},
	})

	result, err := (pollinationsProvider{}).Generate(context.Background(), sys, GenerateRequest{
		Prompt: "a red cube",
		Model:  "turbo",
		Width:  640,
		Height: 480,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req := sys.network.lastRequest()
	if req.Method != "GET" || !strings.HasPrefix(req.URL, "https://image.pollinations.ai/prompt/a%20red%20cube?") {
		t.Fatalf("request = %#v, want pollinations GET", req)
	}
	if !strings.Contains(req.URL, "model=turbo") || !strings.Contains(req.URL, "width=640") || !strings.Contains(req.URL, "height=480") {
		t.Fatalf("url = %q, want model and dimensions", req.URL)
	}
	if result.Provider != "pollinations" || result.Model != "turbo" || result.ContentType != "image/png" || result.SizeBytes != 4 {
		t.Fatalf("result = %#v, want pollinations png", result)
	}
}

func TestProviderInfoUsesAuthorizationContextForEnvSecrets(t *testing.T) {
	base := newProviderTestSystem(t, map[string]string{"OPENAI_API_KEY": "secret"}, system.HTTPResponse{})
	sys := system.WithAuthorization(base, system.AuthorizationConfig{})
	ctx := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:  []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources: []policy.ResourceRef{{Kind: policy.ResourceNetwork, Name: "*"}},
			Actions:   []policy.Action{policy.ActionNetworkFetch},
		}}},
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged, Scopes: []policy.Scope{"*"}},
	})

	info := openAIImageProvider{}.Info(ctx, sys)
	if info.Configured || len(info.Missing) != 1 || info.Missing[0] != "OPENAI_API_KEY" {
		t.Fatalf("Info = %#v, want OPENAI_API_KEY hidden without secret.read", info)
	}
}

func TestOpenAIImageGenerateUsesConfiguredAPI(t *testing.T) {
	image := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})
	sys := newProviderTestSystem(t, map[string]string{"OPENAI_API_KEY": "sk-test"}, system.HTTPResponse{
		Status:     "200 OK",
		StatusCode: 200,
		Body:       []byte(`{"data":[{"b64_json":"` + image + `"}]}`),
	})

	result, err := (openAIImageProvider{}).Generate(context.Background(), sys, GenerateRequest{
		Prompt:  "a diagram",
		Model:   "gpt-image-1",
		Size:    "1024x1024",
		Quality: "high",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req := sys.network.lastRequest()
	if req.URL != "https://api.openai.com/v1/images/generations" || req.Method != "POST" {
		t.Fatalf("request = %#v, want OpenAI image POST", req)
	}
	if req.Headers["authorization"] != "Bearer sk-test" || req.Headers["content-type"] != "application/json" {
		t.Fatalf("headers = %#v, want authorization and json content type", req.Headers)
	}
	body := decodeJSONBody(t, req.Body)
	if body["model"] != "gpt-image-1" || body["prompt"] != "a diagram" || body["response_format"] != nil || body["quality"] != "high" {
		t.Fatalf("body = %#v, want OpenAI generation payload without response_format for gpt-image-1", body)
	}
	if result.Provider != "openai" || result.ContentType != "image/png" {
		t.Fatalf("result = %#v, want openai png", result)
	}
}

func TestOpenAIImageGenerateRequestsB64ForDALLE(t *testing.T) {
	image := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})
	sys := newProviderTestSystem(t, map[string]string{"OPENAI_API_KEY": "sk-test"}, system.HTTPResponse{
		Status:     "200 OK",
		StatusCode: 200,
		Body:       []byte(`{"data":[{"b64_json":"` + image + `"}]}`),
	})

	_, err := (openAIImageProvider{}).Generate(context.Background(), sys, GenerateRequest{Prompt: "a diagram", Model: "dall-e-3"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body := decodeJSONBody(t, sys.network.lastRequest().Body)
	if body["response_format"] != "b64_json" {
		t.Fatalf("body = %#v, want response_format for DALL-E model", body)
	}
}

func TestOpenAIImageGenerateDownloadsURLResponse(t *testing.T) {
	sys := newProviderTestSystem(t, map[string]string{"OPENAI_API_KEY": "sk-test"}, system.HTTPResponse{})
	sys.network.responses = []system.HTTPResponse{
		{
			Status:     "200 OK",
			StatusCode: 200,
			Body:       []byte(`{"data":[{"url":"https://example.test/generated.png"}]}`),
		},
		{
			Status:      "200 OK",
			StatusCode:  200,
			ContentType: "image/png",
			Body:        []byte{0x89, 0x50, 0x4e, 0x47},
		},
	}

	result, err := (openAIImageProvider{}).Generate(context.Background(), sys, GenerateRequest{Prompt: "a diagram"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(sys.network.requests) != 2 || sys.network.requests[1].URL != "https://example.test/generated.png" || sys.network.requests[1].Method != "GET" {
		t.Fatalf("requests = %#v, want image download after generation", sys.network.requests)
	}
	if result.Provider != "openai" || result.ContentType != "image/png" || result.SizeBytes != 4 {
		t.Fatalf("result = %#v, want downloaded openai png", result)
	}
}

func TestOpenRouterImageGenerateUsesChatCompletions(t *testing.T) {
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})
	sys := newProviderTestSystem(t, map[string]string{"OPENROUTER_API_KEY": "or-test"}, system.HTTPResponse{
		Status:     "200 OK",
		StatusCode: 200,
		Body:       []byte(`{"choices":[{"message":{"images":[{"image_url":{"url":"` + dataURL + `"}}]}}]}`),
	})

	result, err := (openRouterImageProvider{}).Generate(context.Background(), sys, GenerateRequest{Prompt: "a chart"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req := sys.network.lastRequest()
	if req.URL != "https://openrouter.ai/api/v1/chat/completions" || req.Headers["authorization"] != "Bearer or-test" {
		t.Fatalf("request = %#v, want OpenRouter authorized chat request", req)
	}
	body := decodeJSONBody(t, req.Body)
	if body["model"] != "google/gemini-2.5-flash-image" {
		t.Fatalf("body model = %#v, want default image model", body["model"])
	}
	if result.Provider != "openrouter" || result.ContentType != "image/png" {
		t.Fatalf("result = %#v, want openrouter png", result)
	}
}

func TestAnthropicUnderstandUsesMessagesAPI(t *testing.T) {
	sys := newProviderTestSystem(t, map[string]string{"ANTHROPIC_API_KEY": "ant-test"}, system.HTTPResponse{
		Status:     "200 OK",
		StatusCode: 200,
		Body:       []byte(`{"content":[{"type":"text","text":"a small png"}]}`),
	})

	result, err := (anthropicUnderstandingProvider{}).Understand(context.Background(), sys, UnderstandRequest{
		Images: []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})},
		Prompt: "describe",
	})
	if err != nil {
		t.Fatalf("Understand: %v", err)
	}
	req := sys.network.lastRequest()
	if req.URL != "https://api.anthropic.com/v1/messages" || req.Method != "POST" {
		t.Fatalf("request = %#v, want Anthropic messages POST", req)
	}
	if req.Headers["x-api-key"] != "ant-test" || req.Headers["anthropic-version"] == "" {
		t.Fatalf("headers = %#v, want Anthropic auth/version", req.Headers)
	}
	body := decodeJSONBody(t, req.Body)
	if body["model"] != "claude-haiku-4-5-20251001" {
		t.Fatalf("body model = %#v, want default Anthropic model", body["model"])
	}
	if result.Provider != "anthropic" || result.Text != "a small png" {
		t.Fatalf("result = %#v, want Anthropic text", result)
	}
}

func TestOpenRouterUnderstandUsesVisionChatCompletions(t *testing.T) {
	sys := newProviderTestSystem(t, map[string]string{"OPENROUTER_API_KEY": "or-test"}, system.HTTPResponse{
		Status:     "200 OK",
		StatusCode: 200,
		Body:       []byte(`{"choices":[{"message":{"content":"a screenshot"}}]}`),
	})

	result, err := (openRouterUnderstandingProvider{}).Understand(context.Background(), sys, UnderstandRequest{
		Images: []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})},
		Prompt: "describe",
	})
	if err != nil {
		t.Fatalf("Understand: %v", err)
	}
	req := sys.network.lastRequest()
	if req.URL != "https://openrouter.ai/api/v1/chat/completions" || req.Headers["authorization"] != "Bearer or-test" {
		t.Fatalf("request = %#v, want OpenRouter authorized chat request", req)
	}
	body := decodeJSONBody(t, req.Body)
	if body["model"] != "anthropic/claude-haiku-4.5" {
		t.Fatalf("body model = %#v, want default vision model", body["model"])
	}
	if result.Provider != "openrouter" || result.Text != "a screenshot" {
		t.Fatalf("result = %#v, want OpenRouter text", result)
	}
}

func decodeJSONBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func newProviderTestSystem(t *testing.T, env map[string]string, response system.HTTPResponse) providerTestSystem {
	t.Helper()
	host, err := system.NewHost(system.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return providerTestSystem{
		workspace: host.Workspace(),
		network:   &recordingNetwork{response: response},
		env:       fakeEnvironment(env),
	}
}

type providerTestSystem struct {
	workspace system.Workspace
	network   *recordingNetwork
	env       system.Environment
}

func (s providerTestSystem) Workspace() system.Workspace     { return s.workspace }
func (s providerTestSystem) Network() system.Network         { return s.network }
func (s providerTestSystem) Process() system.ProcessManager  { return nil }
func (s providerTestSystem) Browser() browser.Manager        { return nil }
func (s providerTestSystem) Clarifier() system.Clarifier     { return nil }
func (s providerTestSystem) Environment() system.Environment { return s.env }

type recordingNetwork struct {
	requests  []system.HTTPRequest
	response  system.HTTPResponse
	responses []system.HTTPResponse
	err       error
}

func (n *recordingNetwork) DoHTTP(_ context.Context, req system.HTTPRequest) (system.HTTPResponse, error) {
	n.requests = append(n.requests, req)
	if len(n.responses) > 0 {
		resp := n.responses[0]
		n.responses = n.responses[1:]
		return resp, n.err
	}
	return n.response, n.err
}

func (n *recordingNetwork) lastRequest() system.HTTPRequest {
	if len(n.requests) == 0 {
		return system.HTTPRequest{}
	}
	return n.requests[len(n.requests)-1]
}

type fakeEnvironment map[string]string

func (e fakeEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e[key]
	return value, ok, nil
}
