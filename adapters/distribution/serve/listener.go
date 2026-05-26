package serve

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/adapters/resources/appconfig"
)

func ListenerHandler(listener appconfig.ListenerDoc, next http.Handler) (http.Handler, error) {
	mode := strings.ToLower(strings.TrimSpace(AuthString(listener.Auth, "mode")))
	if mode == "" {
		if AddrIsTCP(listener.Addr) {
			return nil, fmt.Errorf("serve: listener %q uses TCP addr %q and requires auth", listener.Name, listener.Addr)
		}
		return next, nil
	}
	switch mode {
	case "local_socket":
		if AddrIsTCP(listener.Addr) {
			return nil, fmt.Errorf("serve: listener %q auth mode local_socket requires a unix socket addr", listener.Name)
		}
		return next, nil
	case "bearer", "token":
		token := AuthString(listener.Auth, "token")
		if token == "" {
			if env := AuthString(listener.Auth, "env"); env != "" {
				token = os.Getenv(env)
			}
		}
		if token == "" {
			return nil, fmt.Errorf("serve: listener %q bearer auth token is empty", listener.Name)
		}
		return BearerAuthHandler(token, next), nil
	default:
		return nil, fmt.Errorf("serve: listener %q unsupported auth mode %q", listener.Name, mode)
	}
}

func BearerAuthHandler(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fluxplane"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func AuthString(auth map[string]any, key string) string {
	if len(auth) == 0 {
		return ""
	}
	value, ok := auth[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func AddrIsTCP(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return true
	}
	return !strings.HasSuffix(addr, ".sock")
}

func Listen(addr string) (net.Listener, string, func(), error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if strings.HasSuffix(addr, ".sock") {
		path := ResolveSocketPath(addr)
		if err := PrepareSocketPath(path); err != nil {
			return nil, "", func() {}, err
		}
		ln, err := net.Listen("unix", path)
		if err != nil {
			return nil, "", func() {}, err
		}
		cleanup := func() { _ = os.Remove(path) }
		return ln, "unix:" + path, cleanup, nil
	}
	ln, err := net.Listen("tcp", addr)
	return ln, "http://" + addr, func() {}, err
}

func PrepareSocketPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("serve: inspect unix socket %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("serve: unix socket path %s already exists and is not a socket", path)
	}
	conn, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("serve: unix socket %s is already in use", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("serve: remove stale unix socket %s: %w", path, err)
	}
	return nil
}

func ResolveSocketPath(addr string) string {
	addr = strings.TrimSpace(addr)
	if !strings.ContainsRune(addr, filepath.Separator) {
		base := os.Getenv("XDG_RUNTIME_DIR")
		if base == "" {
			base = os.TempDir()
		}
		return filepath.Join(base, addr)
	}
	return addr
}
