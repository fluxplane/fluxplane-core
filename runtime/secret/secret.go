package secret

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"github.com/fluxplane/fluxplane-policy/policyauth"
	"strings"
	"sync"
	"time"

	coresecret "github.com/fluxplane/fluxplane-auth/authsecret"
	"github.com/fluxplane/fluxplane-policy"
)

// Environment is the system environment boundary shape used by EnvResolver.
type Environment interface {
	Lookup(context.Context, string) (string, bool, error)
}

// Resolver resolves secret refs to raw material for trusted runtime code.
type Resolver interface {
	ResolveSecret(context.Context, coresecret.Ref) (coresecret.Material, bool, error)
}

// ResolverFunc adapts a function into a Resolver.
type ResolverFunc func(context.Context, coresecret.Ref) (coresecret.Material, bool, error)

// ResolveSecret implements Resolver.
func (f ResolverFunc) ResolveSecret(ctx context.Context, ref coresecret.Ref) (coresecret.Material, bool, error) {
	if f == nil {
		return coresecret.Material{}, false, nil
	}
	return f(ctx, ref)
}

// EnvResolver resolves env-backed secrets. Authorization is enforced by Broker;
// this resolver intentionally avoids relying on system Environment's
// secret.read gate so secret.use can be a non-disclosing capability.
type EnvResolver struct {
	Environment Environment
	Kind        coresecret.Kind
}

// ResolveSecret resolves env/<KEY> refs.
func (r EnvResolver) ResolveSecret(ctx context.Context, ref coresecret.Ref) (coresecret.Material, bool, error) {
	ref = ref.Normalize()
	if ref.Scheme != coresecret.SchemeEnv {
		return coresecret.Material{}, false, nil
	}
	if ref.Slot == "" {
		return coresecret.Material{}, false, fmt.Errorf("secret env ref name is empty")
	}
	if r.Environment == nil {
		return coresecret.Material{}, false, fmt.Errorf("secret env resolver environment is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	value, ok, err := r.Environment.Lookup(ctx, string(ref.Slot))
	if err != nil {
		return coresecret.Material{}, false, err
	}
	if !ok || strings.TrimSpace(value) == "" {
		return coresecret.Material{}, false, nil
	}
	kind := r.Kind
	if kind == "" {
		kind = coresecret.KindAPIKey
	}
	return coresecret.Material{Ref: ref, Kind: kind, Value: []byte(value)}, true, nil
}

// ChainResolver tries resolvers in order.
type ChainResolver []Resolver

// ResolveSecret implements Resolver.
func (c ChainResolver) ResolveSecret(ctx context.Context, ref coresecret.Ref) (coresecret.Material, bool, error) {
	for _, resolver := range c {
		if resolver == nil {
			continue
		}
		material, ok, err := resolver.ResolveSecret(ctx, ref)
		if err != nil || ok {
			return material, ok, err
		}
	}
	return coresecret.Material{}, false, nil
}

// Scope ties opaque handles to one session/turn.
type Scope struct {
	Session string
	Turn    string
}

type scopeContextKey struct{}

// ContextWithScope stores secret handle scope on ctx.
func ContextWithScope(ctx context.Context, scope Scope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, scopeContextKey{}, scope)
}

// ScopeFromContext returns the handle scope carried by ctx.
func ScopeFromContext(ctx context.Context) Scope {
	if ctx == nil {
		return Scope{}
	}
	scope, _ := ctx.Value(scopeContextKey{}).(Scope)
	return scope
}

// Broker authorizes secret use and mints scoped opaque placeholders.
type Broker struct {
	resolver Resolver
	now      func() time.Time
	ttl      time.Duration
	mu       sync.Mutex
	handles  map[string]handleRecord
}

type handleRecord struct {
	Scope    Scope
	Ref      coresecret.Ref
	Material coresecret.Material
	Expires  time.Time
}

// Resolution is resolved credential material plus the method that supplied it.
type Resolution struct {
	Ref      coresecret.Ref
	Method   coresecret.AuthMethodSpec
	Material coresecret.Material
}

// NewBroker returns a broker backed by resolver.
func NewBroker(resolver Resolver) *Broker {
	return &Broker{
		resolver: resolver,
		now:      time.Now,
		ttl:      time.Hour,
		handles:  map[string]handleRecord{},
	}
}

// WithTTL sets handle lifetime.
func (b *Broker) WithTTL(ttl time.Duration) *Broker {
	if ttl > 0 {
		b.ttl = ttl
	}
	return b
}

// Use resolves a secret after checking secret.use authorization.
func (b *Broker) Use(ctx context.Context, ref coresecret.Ref) (coresecret.Material, bool, error) {
	if b == nil || b.resolver == nil {
		return coresecret.Material{}, false, fmt.Errorf("secret broker resolver is nil")
	}
	ref = ref.Normalize()
	if err := authorize(ctx, ref); err != nil {
		return coresecret.Material{}, false, err
	}
	return b.resolver.ResolveSecret(ctx, ref)
}

// UseFirst resolves the first available ref after checking secret.use for that
// concrete ref. Missing candidates do not require authorization.
func (b *Broker) UseFirst(ctx context.Context, refs ...coresecret.Ref) (coresecret.Ref, coresecret.Material, bool, error) {
	if b == nil || b.resolver == nil {
		return coresecret.Ref{}, coresecret.Material{}, false, fmt.Errorf("secret broker resolver is nil")
	}
	for _, ref := range refs {
		ref = ref.Normalize()
		probe, ok, err := b.resolver.ResolveSecret(ctx, ref)
		if err != nil {
			return coresecret.Ref{}, coresecret.Material{}, false, err
		}
		if !ok {
			continue
		}
		if err := authorize(ctx, ref); err != nil {
			return ref, coresecret.Material{}, false, err
		}
		return ref, probe, true, nil
	}
	return coresecret.Ref{}, coresecret.Material{}, false, nil
}

// UseAvailable resolves the first configured auth method that has material
// after checking secret.use on the logical plugin secret.
func (b *Broker) UseAvailable(ctx context.Context, req coresecret.AuthRequest) (Resolution, bool, error) {
	if b == nil || b.resolver == nil {
		return Resolution{}, false, fmt.Errorf("secret broker resolver is nil")
	}
	req = req.Normalize()
	logical := req.SecretRef()
	if logical.ResourceName() == "" {
		return Resolution{}, false, fmt.Errorf("secret auth request is incomplete")
	}
	if err := authorize(ctx, logical); err != nil {
		return Resolution{}, false, err
	}
	for _, method := range req.Methods {
		if err := coresecret.ValidateAuthMethod(method); err != nil {
			return Resolution{}, false, err
		}
		for _, ref := range refsForMethod(method) {
			material, found, err := b.resolver.ResolveSecret(ctx, ref)
			if found && method.Kind != "" {
				material.Kind = method.Kind
			}
			if err != nil || found {
				return Resolution{Ref: ref, Method: method, Material: material}, found, err
			}
		}
	}
	return Resolution{}, false, nil
}

// Mint returns a model-visible placeholder for a resolved secret.
func (b *Broker) Mint(ctx context.Context, ref coresecret.Ref) (coresecret.Placeholder, bool, error) {
	material, ok, err := b.Use(ctx, ref)
	if err != nil || !ok {
		return "", ok, err
	}
	handle, err := randomHandle()
	if err != nil {
		return "", false, err
	}
	record := handleRecord{
		Scope:    ScopeFromContext(ctx),
		Ref:      ref.Normalize(),
		Material: material,
		Expires:  b.now().Add(b.ttl),
	}
	b.mu.Lock()
	b.handles[handle] = record
	b.mu.Unlock()
	return coresecret.PlaceholderFor(handle), true, nil
}

// ResolveHandle resolves a previously minted handle in the same scope.
func (b *Broker) ResolveHandle(ctx context.Context, handle string) (coresecret.Material, bool, error) {
	if b == nil {
		return coresecret.Material{}, false, fmt.Errorf("secret broker is nil")
	}
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return coresecret.Material{}, false, fmt.Errorf("secret handle is empty")
	}
	b.mu.Lock()
	record, ok := b.handles[handle]
	if ok && !record.Expires.IsZero() && !b.now().Before(record.Expires) {
		delete(b.handles, handle)
		ok = false
	}
	b.mu.Unlock()
	if !ok {
		return coresecret.Material{}, false, nil
	}
	if !sameScope(record.Scope, ScopeFromContext(ctx)) {
		return coresecret.Material{}, false, fmt.Errorf("secret handle scope mismatch")
	}
	if err := authorize(ctx, record.Ref); err != nil {
		return coresecret.Material{}, false, err
	}
	return record.Material, true, nil
}

func refsForMethod(method coresecret.AuthMethodSpec) []coresecret.Ref {
	switch method.Method {
	case coresecret.AuthMethodEnv:
		if strings.TrimSpace(method.Env.Name) == "" {
			return envRefs(method.Env.Aliases)
		}
		return []coresecret.Ref{coresecret.Env(method.Env.Name)}
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

func envRefs(names []string) []coresecret.Ref {
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

func authorize(ctx context.Context, ref coresecret.Ref) error {
	auth, ok := policyauth.AuthorizationFromContext(ctx)
	if !ok || auth.Policy.IsZero() {
		return nil
	}
	req := policy.AuthorizationRequest{
		Subjects: auth.Subjects,
		Trust:    auth.Trust,
		Resource: policy.ResourceRef{Kind: policy.ResourceSecret, Name: ref.ResourceName()},
		Action:   policy.ActionSecretUse,
	}
	evaluation := policy.EvaluateAuthorization(auth.Policy, req)
	policyauth.EmitAuthorizationDecision(ctx, auth, req, evaluation)
	if evaluation.Decision == policy.DecisionAllow {
		return nil
	}
	return fmt.Errorf("authorization_%s: %s secret:%s: %s", evaluation.Decision, policy.ActionSecretUse, ref.ResourceName(), evaluation.Reason)
}

func randomHandle() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func sameScope(a, b Scope) bool {
	return a.Session == b.Session && a.Turn == b.Turn
}
