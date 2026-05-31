package openapi

import (
	"fmt"
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
)

const Name = "openapi"

// Config is the per-instance OpenAPI plugin configuration.
type Config struct {
	Specs []SpecConfig `json:"specs,omitempty" yaml:"specs,omitempty" jsonschema:"description=OpenAPI documents to load and expose as generated operations or datasource docs."`
}

// SpecConfig configures one OpenAPI document.
type SpecConfig struct {
	URL        string           `json:"url,omitempty" yaml:"url,omitempty" jsonschema:"description=HTTP URL of the OpenAPI document."`
	File       string           `json:"file,omitempty" yaml:"file,omitempty" jsonschema:"description=Local file path to the OpenAPI document."`
	Operations OperationsConfig `json:"operations,omitempty" yaml:"operations,omitempty" jsonschema:"description=Generated operation selection and naming options."`
	Datasource DatasourceConfig `json:"datasource,omitempty" yaml:"datasource,omitempty" jsonschema:"description=Generated datasource documentation options."`
	Auth       AuthConfig       `json:"auth,omitempty" yaml:"auth,omitempty" jsonschema:"description=Security scheme credential mappings for generated operations."`
}

// OperationsConfig selects and customizes generated operations.
type OperationsConfig struct {
	Prefix    string                     `json:"prefix,omitempty" yaml:"prefix,omitempty" jsonschema:"description=Prefix added to generated operation names."`
	Include   []string                   `json:"include,omitempty" yaml:"include,omitempty" jsonschema:"description=Operation IDs or patterns to include."`
	Exclude   []string                   `json:"exclude,omitempty" yaml:"exclude,omitempty" jsonschema:"description=Operation IDs or patterns to exclude."`
	Overrides map[string]OperationConfig `json:"overrides,omitempty" yaml:"overrides,omitempty" jsonschema:"description=Per-operation naming and description overrides keyed by OpenAPI operation id."`
}

// OperationConfig overrides one generated operation.
type OperationConfig struct {
	Name        string `json:"name,omitempty" yaml:"name,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// DatasourceConfig configures generated documentation datasource resources.
type DatasourceConfig struct {
	Name  string                `json:"name,omitempty" yaml:"name,omitempty" jsonschema:"description=Name for the generated OpenAPI documentation datasource."`
	Index DatasourceIndexConfig `json:"index,omitempty" yaml:"index,omitempty" jsonschema:"description=Indexing options for the generated OpenAPI documentation datasource."`
}

// DatasourceIndexConfig configures local indexing for generated docs.
type DatasourceIndexConfig struct {
	Enabled   bool   `json:"enabled,omitempty" yaml:"enabled,omitempty" jsonschema:"description=Whether generated OpenAPI documentation should be indexed."`
	Freshness string `json:"freshness,omitempty" yaml:"freshness,omitempty" jsonschema:"description=How often generated OpenAPI documentation may be refreshed. Use Go duration syntax such as 24h."`
}

// AuthConfig maps OpenAPI security scheme names to runtime auth methods.
type AuthConfig struct {
	Schemes map[string]AuthSchemeConfig `json:"schemes,omitempty" yaml:"schemes,omitempty" jsonschema:"description=Credential mappings keyed by OpenAPI security scheme name."`
}

// AuthSchemeConfig configures one OpenAPI security scheme credential source.
type AuthSchemeConfig struct {
	Method      auth.Method       `json:"method,omitempty" yaml:"method,omitempty"`
	Kind        sharedsecret.Kind `json:"kind,omitempty" yaml:"kind,omitempty"`
	DisplayName string            `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Secret      sharedsecret.Ref  `json:"secret,omitempty" yaml:"secret,omitempty"`
	Env         auth.EnvSpec      `json:"env,omitempty" yaml:"env,omitempty"`
	Header      auth.HeaderSpec   `json:"header,omitempty" yaml:"header,omitempty"`
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
