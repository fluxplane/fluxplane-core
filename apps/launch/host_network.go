package launch

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

// HostNetwork implements primitive network access with target guards.
type HostNetwork struct {
	allowPrivate bool
}

func (n *HostNetwork) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip, err := resolvePublicIP(ctx, host, n.allowPrivate)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
}

func (n *HostNetwork) Resolver() fpsystem.Resolver {
	return hostResolver{}
}

type hostResolver struct{}

func (hostResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

func (hostResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func (hostResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return net.DefaultResolver.LookupCNAME(ctx, host)
}

func (hostResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, name)
}

func (hostResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return net.DefaultResolver.LookupSRV(ctx, service, proto, name)
}

func (hostResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
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
	if ip := net.ParseIP(host); ip != nil && !allowPrivate && blockedIP(ip) {
		return fmt.Errorf("private, local, multicast, and metadata network targets are blocked")
	}
	return nil
}

// PublicNetworkTransport returns a guarded HTTP transport.
func PublicNetworkTransport(allowPrivate bool) http.RoundTripper {
	return PublicNetworkTransportWithTLS(allowPrivate, nil)
}

// PublicNetworkTransportWithTLS returns a guarded HTTP transport with optional
// caller-provided TLS settings.
func PublicNetworkTransportWithTLS(allowPrivate bool, cfg *tls.Config) http.RoundTripper {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ip, err := resolvePublicIP(ctx, host, allowPrivate)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
		TLSClientConfig: secureTLSConfig(cfg),
	}
}

func secureTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	out := cfg.Clone()
	if out.MinVersion == 0 || out.MinVersion < tls.VersionTLS12 {
		out.MinVersion = tls.VersionTLS12
	}
	return out
}

func resolvePublicIP(ctx context.Context, host string, allowPrivate bool) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !allowPrivate && blockedIP(ip) {
			return nil, fmt.Errorf("private, local, multicast, and metadata network targets are blocked")
		}
		return ip, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		if allowPrivate || !blockedIP(addr.IP) {
			return addr.IP, nil
		}
	}
	return nil, fmt.Errorf("host resolves only to private, local, multicast, or metadata addresses")
}

func blockedIP(ip net.IP) bool {
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
