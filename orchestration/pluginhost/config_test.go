package pluginhost

import (
	"encoding/json"
	"testing"
)

func TestConfigurableExposesConfigSchema(t *testing.T) {
	type nestedConfig struct {
		TokenEnv string `json:"token_env,omitempty"`
	}
	type testConfig struct {
		BaseURL string       `json:"base_url,omitempty"`
		Auth    nestedConfig `json:"auth,omitempty"`
	}
	var configurable Configurable[testConfig]
	data, err := configurable.ConfigSchema()
	if err != nil {
		t.Fatalf("ConfigSchema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["base_url"]; !ok {
		t.Fatalf("properties = %#v, want base_url", properties)
	}
	auth := properties["auth"].(map[string]any)
	authProperties := auth["properties"].(map[string]any)
	if _, ok := authProperties["token_env"]; !ok {
		t.Fatalf("auth properties = %#v, want token_env", authProperties)
	}
	if schema["$defs"] != nil {
		t.Fatalf("$defs = %#v, want inlined schema", schema["$defs"])
	}
}
