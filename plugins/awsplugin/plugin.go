package awsplugin

import (
	"context"
	"strings"
	"time"

	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name = "aws"

	ObservationAWSEnvironment = "aws.environment"

	observerName = "aws.environment"
	deriverName  = "aws.signals"
)

type Config struct {
	Profile    string `json:"profile,omitempty" yaml:"profile,omitempty"`
	Region     string `json:"region,omitempty" yaml:"region,omitempty"`
	ProfileEnv string `json:"profile_env,omitempty" yaml:"profile_env,omitempty"`
	RegionEnv  string `json:"region_env,omitempty" yaml:"region_env,omitempty"`
}

// Plugin observes local AWS configuration without exposing credential values.
type Plugin struct {
	pluginhost.Configurable[Config]
	system system.System
	ref    resource.PluginRef
	cfg    Config
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.SignalDeriverContributor = Plugin{}

func New(sys system.System) Plugin {
	return Plugin{system: sys}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "AWS environment observation."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[Config](ctx)
	if err != nil {
		return nil, err
	}
	p.ref = ctx.Ref
	p.cfg = NormalizeConfig(cfg)
	return p, nil
}

func (p Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	p.ref = ctx.Ref
	return resource.ContributionBundle{
		Observers:      []coreenvironment.ObserverSpec{observerSpec(p.ref)},
		SignalDerivers: []coreenvironment.SignalDeriverSpec{signalDeriverSpec()},
	}, nil
}

func (p Plugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeenvironment.Observer, error) {
	return []runtimeenvironment.Observer{observer{plugin: p}}, nil
}

func (Plugin) SignalDerivers(context.Context, pluginhost.Context) ([]runtimeenvironment.SignalDeriver, error) {
	return []runtimeenvironment.SignalDeriver{signalDeriver{}}, nil
}

func NormalizeConfig(cfg Config) Config {
	cfg.Profile = strings.TrimSpace(cfg.Profile)
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.ProfileEnv = strings.TrimSpace(cfg.ProfileEnv)
	cfg.RegionEnv = strings.TrimSpace(cfg.RegionEnv)
	return cfg
}

type observer struct {
	plugin Plugin
}

func (o observer) Spec() coreenvironment.ObserverSpec {
	return observerSpec(o.plugin.ref)
}

func (o observer) Observe(ctx context.Context, _ runtimeenvironment.ObservationRequest) ([]coreenvironment.Observation, error) {
	content := map[string]any{
		"configured":               false,
		"available":                false,
		"profile":                  o.plugin.cfg.Profile,
		"region":                   o.plugin.cfg.Region,
		"access_key_configured":    false,
		"secret_key_configured":    false,
		"session_token_configured": false,
		"web_identity_configured":  false,
		"role_arn_configured":      false,
		"source":                   "env",
	}
	if o.plugin.cfg.Profile != "" || o.plugin.cfg.Region != "" {
		content["source"] = "config"
	}

	var env system.Environment
	if o.plugin.system != nil {
		env = o.plugin.system.Environment()
	}
	if env != nil {
		if content["profile"] == "" {
			profile, _, err := lookupFirst(ctx, env, profileKeys(o.plugin.cfg)...)
			if err != nil {
				return nil, err
			}
			content["profile"] = profile
		}
		if content["region"] == "" {
			region, _, err := lookupFirst(ctx, env, regionKeys(o.plugin.cfg)...)
			if err != nil {
				return nil, err
			}
			content["region"] = region
		}
		accessKey, err := lookupPresent(ctx, env, "AWS_ACCESS_KEY_ID")
		if err != nil {
			return nil, err
		}
		secretKey, err := lookupPresent(ctx, env, "AWS_SECRET_ACCESS_KEY")
		if err != nil {
			return nil, err
		}
		sessionToken, err := lookupPresent(ctx, env, "AWS_SESSION_TOKEN")
		if err != nil {
			return nil, err
		}
		webIdentityTokenFile, err := lookupPresent(ctx, env, "AWS_WEB_IDENTITY_TOKEN_FILE")
		if err != nil {
			return nil, err
		}
		roleARN, err := lookupPresent(ctx, env, "AWS_ROLE_ARN")
		if err != nil {
			return nil, err
		}
		content["access_key_configured"] = accessKey
		content["secret_key_configured"] = secretKey
		content["session_token_configured"] = sessionToken
		content["web_identity_configured"] = webIdentityTokenFile
		content["role_arn_configured"] = roleARN
	}

	profile, _ := content["profile"].(string)
	region, _ := content["region"].(string)
	staticCredentials := boolContent(content, "access_key_configured") && boolContent(content, "secret_key_configured")
	webIdentity := boolContent(content, "web_identity_configured") && boolContent(content, "role_arn_configured")
	configured := profile != "" || region != "" || staticCredentials || webIdentity || boolContent(content, "session_token_configured")
	available := profile != "" || staticCredentials || webIdentity
	content["configured"] = configured
	content["available"] = available

	now := time.Now().UTC()
	return []coreenvironment.Observation{{
		ID:          "integration:aws:" + observerInstance(o.plugin.ref),
		Environment: coreenvironment.Ref{Name: coreenvironment.Name(Name)},
		Kind:        ObservationAWSEnvironment,
		Scope:       awsScope(profile, region, o.plugin.ref),
		Content:     content,
		At:          now,
	}}, nil
}

type signalDeriver struct{}

func (signalDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return signalDeriverSpec()
}

func (signalDeriver) Derive(_ context.Context, req runtimeenvironment.SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	var out []coreenvironment.Signal
	for _, observation := range req.Observations {
		if observation.Kind != ObservationAWSEnvironment {
			continue
		}
		content, _ := observation.Content.(map[string]any)
		metadata := signalMetadata(content)
		if boolContent(content, "configured") {
			out = append(out, coreenvironment.Signal{
				Kind:           "integration.configured",
				Target:         Name,
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: []string{observation.ID},
				Metadata:       metadata,
			})
		}
		if boolContent(content, "available") {
			out = append(out, coreenvironment.Signal{
				Kind:           "integration.available",
				Target:         Name,
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: []string{observation.ID},
				Metadata:       metadata,
			})
		}
	}
	return out, nil
}

func observerSpec(ref resource.PluginRef) coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{
		Name:        observerName,
		Description: "Reports non-secret AWS environment configuration and credential presence.",
		Environment: coreenvironment.Ref{
			Name: coreenvironment.Name(Name),
		},
		Phase:           coreenvironment.PhaseTurn,
		ObservableKinds: []string{ObservationAWSEnvironment},
		Dynamic:         true,
		Annotations: map[string]string{
			"plugin":   Name,
			"instance": observerInstance(ref),
		},
	}
}

func signalDeriverSpec() coreenvironment.SignalDeriverSpec {
	return coreenvironment.SignalDeriverSpec{
		Name:             deriverName,
		Description:      "Derives AWS integration configured/available signals from AWS environment observations.",
		ObservationKinds: []string{ObservationAWSEnvironment},
		Signals: []coreenvironment.SignalTemplate{
			{Kind: "integration.configured", Target: Name},
			{Kind: "integration.available", Target: Name},
		},
	}
}

func profileKeys(cfg Config) []string {
	if cfg.ProfileEnv != "" {
		return []string{cfg.ProfileEnv}
	}
	return []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE"}
}

func regionKeys(cfg Config) []string {
	if cfg.RegionEnv != "" {
		return []string{cfg.RegionEnv}
	}
	return []string{"AWS_REGION", "AWS_DEFAULT_REGION"}
}

func lookupFirst(ctx context.Context, env system.Environment, keys ...string) (string, bool, error) {
	for _, key := range keys {
		value, ok, err := env.Lookup(ctx, key)
		if err != nil {
			return "", false, err
		}
		value = strings.TrimSpace(value)
		if ok && value != "" {
			return value, true, nil
		}
	}
	return "", false, nil
}

func lookupPresent(ctx context.Context, env system.Environment, key string) (bool, error) {
	value, ok, err := env.Lookup(ctx, key)
	if err != nil {
		return false, err
	}
	return ok && strings.TrimSpace(value) != "", nil
}

func awsScope(profile, region string, ref resource.PluginRef) string {
	parts := []string{"integration", Name}
	if instance := observerInstance(ref); instance != Name {
		parts = append(parts, "instance", sanitizeScopePart(instance))
	}
	if profile != "" {
		parts = append(parts, "profile", sanitizeScopePart(profile))
	}
	if region != "" {
		parts = append(parts, "region", sanitizeScopePart(region))
	}
	return strings.Join(parts, ":")
}

func observerInstance(ref resource.PluginRef) string {
	if instance := strings.TrimSpace(ref.InstanceName()); instance != "" {
		return instance
	}
	return Name
}

func sanitizeScopePart(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer(":", "_", "/", "_", "\\", "_", " ", "_")
	return replacer.Replace(value)
}

func boolContent(content map[string]any, key string) bool {
	value, _ := content[key].(bool)
	return value
}

func signalMetadata(content map[string]any) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"profile", "region"} {
		value, _ := content[key].(string)
		if value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
