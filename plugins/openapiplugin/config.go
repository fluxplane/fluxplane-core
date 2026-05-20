package openapiplugin

import (
	"fmt"
	"strings"

	coresecret "github.com/fluxplane/agentruntime/core/secret"
)

const Name = "openapi"

// Config is the per-instance OpenAPI plugin configuration.
type Config struct {
	Specs []SpecConfig `json:"specs,omitempty" yaml:"specs,omitempty"`
}

// SpecConfig configures one OpenAPI document.
type SpecConfig struct {
	URL        string           `json:"url,omitempty" yaml:"url,omitempty"`
	File       string           `json:"file,omitempty" yaml:"file,omitempty"`
	Operations OperationsConfig `json:"operations,omitempty" yaml:"operations,omitempty"`
	Datasource DatasourceConfig `json:"datasource,omitempty" yaml:"datasource,omitempty"`
	Auth       AuthConfig       `json:"auth,omitempty" yaml:"auth,omitempty"`
}

// OperationsConfig selects and customizes generated operations.
type OperationsConfig struct {
	Prefix    string                     `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Include   []string                   `json:"include,omitempty" yaml:"include,omitempty"`
	Exclude   []string                   `json:"exclude,omitempty" yaml:"exclude,omitempty"`
	Overrides map[string]OperationConfig `json:"overrides,omitempty" yaml:"overrides,omitempty"`
}

// OperationConfig overrides one generated operation.
type OperationConfig struct {
	Name        string `json:"name,omitempty" yaml:"name,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// DatasourceConfig configures generated documentation datasource resources.
type DatasourceConfig struct {
	Name  string                `json:"name,omitempty" yaml:"name,omitempty"`
	Index DatasourceIndexConfig `json:"index,omitempty" yaml:"index,omitempty"`
}

// DatasourceIndexConfig configures local indexing for generated docs.
type DatasourceIndexConfig struct {
	Enabled   bool   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Freshness string `json:"freshness,omitempty" yaml:"freshness,omitempty"`
}

// AuthConfig maps OpenAPI security scheme names to runtime auth methods.
type AuthConfig struct {
	Schemes map[string]AuthSchemeConfig `json:"schemes,omitempty" yaml:"schemes,omitempty"`
}

// AuthSchemeConfig configures one OpenAPI security scheme credential source.
type AuthSchemeConfig struct {
	Method      coresecret.AuthMethodKind `json:"method,omitempty" yaml:"method,omitempty"`
	Kind        coresecret.Kind           `json:"kind,omitempty" yaml:"kind,omitempty"`
	DisplayName string                    `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Description string                    `json:"description,omitempty" yaml:"description,omitempty"`
	Secret      coresecret.Ref            `json:"secret,omitempty" yaml:"secret,omitempty"`
	Env         coresecret.EnvSpec        `json:"env,omitempty" yaml:"env,omitempty"`
	Header      coresecret.HeaderSpec     `json:"header,omitempty" yaml:"header,omitempty"`
}

func normalizeConfig(cfg Config) Config {
	for i := range cfg.Specs {
		spec := &cfg.Specs[i]
		spec.URL = strings.TrimSpace(spec.URL)
		spec.File = strings.TrimSpace(spec.File)
		if strings.HasPrefix(spec.URL, "file://") && spec.File == "" {
			spec.File = strings.TrimPrefix(spec.URL, "file://")
			spec.URL = ""
		}
		spec.Operations.Prefix = strings.TrimSpace(spec.Operations.Prefix)
		spec.Operations.Include = normalizeStrings(spec.Operations.Include)
		spec.Operations.Exclude = normalizeStrings(spec.Operations.Exclude)
		spec.Datasource.Name = strings.TrimSpace(spec.Datasource.Name)
		spec.Datasource.Index.Freshness = strings.TrimSpace(spec.Datasource.Index.Freshness)
	}
	return cfg
}

func (cfg Config) validate() error {
	for i, spec := range cfg.Specs {
		if (strings.TrimSpace(spec.URL) == "") == (strings.TrimSpace(spec.File) == "") {
			return fmt.Errorf("specs[%d]: exactly one of url or file is required", i)
		}
	}
	return nil
}

func normalizeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}
