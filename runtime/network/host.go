package network

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	fpsystem "github.com/fluxplane/fluxplane-system"
)

// Host implements primitive host network access with target guards.
type Host struct {
	allowPrivate bool
}

// NewHost returns a host-backed network boundary.
func NewHost(allowPrivate bool) *Host {
	return &Host{allowPrivate: allowPrivate}
}

func (n *Host) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip, err := ResolvePublicIP(ctx, host, n.allowPrivate)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
}

func (n *Host) Resolver() fpsystem.Resolver {
	return Resolver{}
}

// Resolver delegates DNS lookups to net.DefaultResolver.
type Resolver struct{}

func (Resolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

func (Resolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func (Resolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return net.DefaultResolver.LookupCNAME(ctx, host)
}

func (Resolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, name)
}

func (Resolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return net.DefaultResolver.LookupSRV(ctx, service, proto, name)
}

func (Resolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

// ValidatePublicURL rejects non-HTTP and private/local targets.
func ValidatePublicURL(parsed *url.URL, allowPrivate bool) error {
	if parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("url must be absolute http or https")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("url host is empty")
	}
	if ip := net.ParseIP(host); ip != nil && !allowPrivate && BlockedIP(ip) {
		return fmt.Errorf("private, local, multicast, and metadata network targets are blocked")
	}
	return nil
}

// PublicTransport returns a guarded HTTP transport.
func PublicTransport(allowPrivate bool) http.RoundTripper {
	return PublicTransportWithTLS(allowPrivate, nil)
}

// PublicTransportWithTLS returns a guarded HTTP transport with optional
// caller-provided TLS settings.
func PublicTransportWithTLS(allowPrivate bool, cfg *tls.Config) http.RoundTripper {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ip, err := ResolvePublicIP(ctx, host, allowPrivate)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
		TLSClientConfig: SecureTLSConfig(cfg),
	}
}

// SecureTLSConfig returns cfg with at least TLS 1.2 enforced.
func SecureTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	out := cfg.Clone()
	if out.MinVersion == 0 || out.MinVersion < tls.VersionTLS12 {
		out.MinVersion = tls.VersionTLS12
	}
	return out
}

// ResolvePublicIP resolves host to an IP accepted by the allowPrivate policy.
func ResolvePublicIP(ctx context.Context, host string, allowPrivate bool) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !allowPrivate && BlockedIP(ip) {
			return nil, fmt.Errorf("private, local, multicast, and metadata network targets are blocked")
		}
		return ip, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		if allowPrivate || !BlockedIP(addr.IP) {
			return addr.IP, nil
		}
	}
	return nil, fmt.Errorf("host resolves only to private, local, multicast, or metadata addresses")
}

// BlockedIP reports whether ip is unsafe for public-only network access.
func BlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.Equal(net.ParseIP("169.254.169.254"))
}

var _ fpsystem.Network = (*Host)(nil)
var _ fpsystem.Resolver = Resolver{}
