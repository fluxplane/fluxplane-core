// Package authstatus evaluates plugin auth readiness without exposing secrets.
package authstatus

import (
	"context"
	"strings"
	"time"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
)

const (
	ObservationKind          = "auth.status"
	AssertionAuthenticated   = "integration.authenticated"
	ObserverName             = "auth.status"
	AssertionDeriverName     = "auth.assertions"
	StatusConnected          = "connected"
	StatusNotConnected       = "not_connected"
	defaultObservationSource = "auth"
)

// Target is one plugin instance whose declared auth methods should be checked.
type Target struct {
	Ref     resource.PluginRef
	Methods []coresecret.AuthMethodSpec
}

// Status is a non-secret summary of one plugin instance's auth readiness.
type Status struct {
	Plugin    string        `json:"plugin"`
	Instance  string        `json:"instance,omitempty"`
	Status    string        `json:"status"`
	MethodID  string        `json:"method_id,omitempty"`
	Method    string        `json:"method,omitempty"`
	Connected bool          `json:"connected"`
	Message   string        `json:"message,omitempty"`
	Fields    []FieldStatus `json:"fields,omitempty"`
}

// FieldStatus is non-secret presence information for a setup field.
type FieldStatus struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

// Evaluate returns the first locally resolvable auth method for target.
func Evaluate(ctx context.Context, resolver runtimesecret.Resolver, target Target) Status {
	ref := target.Ref
	status := Status{
		Plugin:   strings.TrimSpace(ref.Name),
		Instance: ref.InstanceName(),
		Status:   StatusNotConnected,
	}
	for _, method := range target.Methods {
		configured, fields := methodConfigured(ctx, resolver, ref, method)
		if configured {
			status.Status = StatusConnected
			status.Connected = true
			status.MethodID = strings.TrimSpace(method.Name)
			status.Method = FriendlyMethodName(method)
			status.Fields = fields
			return status
		}
		if status.Method == "" && anyFieldSet(fields) {
			status.MethodID = strings.TrimSpace(method.Name)
			status.Method = FriendlyMethodName(method)
			status.Fields = fields
		}
	}
	if len(target.Methods) == 0 {
		status.Message = "no auth methods declared"
	}
	return status
}

// FriendlyMethodName returns the compact method label used in status summaries.
func FriendlyMethodName(method coresecret.AuthMethodSpec) string {
	name := strings.ToLower(strings.TrimSpace(method.Name))
	switch name {
	case "personal_access_token", "personal-access-token", "api_token", "api-token", "bearer":
		return "token"
	default:
		return name
	}
}

func methodConfigured(ctx context.Context, resolver runtimesecret.Resolver, ref resource.PluginRef, method coresecret.AuthMethodSpec) (bool, []FieldStatus) {
	if resolver == nil {
		return false, nil
	}
	if method.Method == coresecret.AuthMethodStored && len(method.SetupFields) > 0 {
		return setupFieldsConfigured(ctx, resolver, ref, method.SetupFields)
	}
	if len(method.SetupFields) > 0 {
		fieldsConfigured, fields := setupFieldsConfigured(ctx, resolver, ref, method.SetupFields)
		for _, candidate := range refsForMethod(method) {
			if secretConfigured(ctx, resolver, candidate) {
				return fieldsConfigured, fields
			}
		}
		return false, fields
	}
	for _, candidate := range refsForMethod(method) {
		if secretConfigured(ctx, resolver, candidate) {
			return true, nil
		}
	}
	return false, nil
}

func setupFieldsConfigured(ctx context.Context, resolver runtimesecret.Resolver, ref resource.PluginRef, fields []coresecret.SetupFieldSpec) (bool, []FieldStatus) {
	configured := map[string]bool{}
	statuses := make([]FieldStatus, 0, len(fields))
	anySet := false
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			continue
		}
		set := secretConfigured(ctx, resolver, coresecret.Plugin(ref.Name, ref.InstanceName(), name)) || envConfigured(ctx, resolver, field.Env)
		configured[name] = set
		anySet = anySet || set
		statuses = append(statuses, FieldStatus{Name: name, Set: set})
	}
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		if field.Required && !configured[name] {
			return false, statuses
		}
	}
	groups := requiredGroups(fields)
	for _, names := range groups {
		ok := false
		for _, name := range names {
			if configured[name] {
				ok = true
				break
			}
		}
		if !ok {
			return false, statuses
		}
	}
	return anySet, statuses
}

func refsForMethod(method coresecret.AuthMethodSpec) []coresecret.Ref {
	switch method.Method {
	case coresecret.AuthMethodEnv:
		return envRefs(method.Env)
	case coresecret.AuthMethodOAuth2, coresecret.AuthMethodStored:
		ref := method.Secret.Normalize()
		if ref.ResourceName() == "" {
			return nil
		}
		return []coresecret.Ref{ref}
	default:
		return nil
	}
}

func envConfigured(ctx context.Context, resolver runtimesecret.Resolver, spec coresecret.EnvSpec) bool {
	for _, ref := range envRefs(spec) {
		if secretConfigured(ctx, resolver, ref) {
			return true
		}
	}
	return false
}

func envRefs(spec coresecret.EnvSpec) []coresecret.Ref {
	names := append([]string{spec.Name}, spec.Aliases...)
	refs := make([]coresecret.Ref, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		refs = append(refs, coresecret.Env(name))
	}
	return refs
}

func secretConfigured(ctx context.Context, resolver runtimesecret.Resolver, ref coresecret.Ref) bool {
	material, ok, err := resolver.ResolveSecret(ctx, ref)
	return err == nil && ok && strings.TrimSpace(material.Value) != ""
}

func anyFieldSet(fields []FieldStatus) bool {
	for _, field := range fields {
		if field.Set {
			return true
		}
	}
	return false
}

func requiredGroups(fields []coresecret.SetupFieldSpec) map[string][]string {
	groups := map[string][]string{}
	for _, field := range fields {
		group := strings.TrimSpace(field.RequiredGroup)
		name := strings.TrimSpace(field.Name)
		if group == "" || name == "" {
			continue
		}
		groups[group] = append(groups[group], name)
	}
	return groups
}

// NewObserver returns a startup observer for auth readiness.
func NewObserver(targets []Target, resolver runtimesecret.Resolver) runtimeevidence.Observer {
	return observer{targets: append([]Target(nil), targets...), resolver: resolver}
}

// NewAssertionDeriver returns a deriver for auth readiness observations.
func NewAssertionDeriver() runtimeevidence.AssertionDeriver {
	return assertionDeriver{}
}

type observer struct {
	targets  []Target
	resolver runtimesecret.Resolver
}

func (observer) Spec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:            ObserverName,
		Description:     "Reports non-secret plugin auth readiness.",
		Phase:           coreevidence.PhaseStartup,
		ObservableKinds: []string{ObservationKind},
		Annotations:     map[string]string{"source": defaultObservationSource},
	}
}

func (o observer) Observe(ctx context.Context, _ runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	out := make([]coreevidence.Observation, 0, len(o.targets))
	now := time.Now().UTC()
	for _, target := range o.targets {
		status := Evaluate(ctx, o.resolver, target)
		ref := target.Ref
		out = append(out, coreevidence.Observation{
			ID:      "auth:" + ref.Name + ":" + ref.InstanceName(),
			Source:  defaultObservationSource,
			Kind:    ObservationKind,
			Scope:   ref.InstanceName(),
			Content: status,
			Metadata: map[string]any{
				"plugin":   status.Plugin,
				"instance": status.Instance,
				"method":   status.Method,
				"status":   status.Status,
				"fields":   fieldMetadata(status.Fields),
			},
			At: now,
		})
	}
	return out, nil
}

type assertionDeriver struct{}

func (assertionDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             AssertionDeriverName,
		Description:      "Derives integration authenticated assertions from auth status observations.",
		ObservationKinds: []string{ObservationKind},
		Assertions: []coreevidence.AssertionTemplate{{
			Kind:    AssertionAuthenticated,
			Subject: coreevidence.Subject{Kind: coreevidence.SubjectIntegration},
		}},
	}
}

func (assertionDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if observation.Kind != ObservationKind {
			continue
		}
		status, ok := statusFromObservation(observation.Content)
		if !ok || !status.Connected {
			continue
		}
		target := status.Plugin
		subjectID := status.Plugin + "/" + status.Instance
		out = append(out, coreevidence.Assertion{
			Kind:           AssertionAuthenticated,
			Target:         target,
			Subject:        coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: status.Plugin, ID: subjectID},
			Scope:          status.Instance,
			Source:         defaultObservationSource,
			Confidence:     1,
			ObservationIDs: observationIDs(observation.ID),
			Metadata: map[string]string{
				"plugin":   status.Plugin,
				"instance": status.Instance,
				"method":   status.Method,
				"status":   status.Status,
				"fields":   strings.Join(setFieldNames(status.Fields), ","),
			},
		})
	}
	return out, nil
}

func fieldMetadata(fields []FieldStatus) string {
	return strings.Join(setFieldNames(fields), ",")
}

func setFieldNames(fields []FieldStatus) []string {
	var out []string
	for _, field := range fields {
		if field.Set && strings.TrimSpace(field.Name) != "" {
			out = append(out, strings.TrimSpace(field.Name))
		}
	}
	return out
}

func statusFromObservation(content any) (Status, bool) {
	switch typed := content.(type) {
	case Status:
		return typed, true
	case *Status:
		if typed == nil {
			return Status{}, false
		}
		return *typed, true
	default:
		return Status{}, false
	}
}

func observationIDs(id string) []string {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return []string{id}
}
