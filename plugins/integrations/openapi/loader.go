package openapi

import (
	"context"
	"fmt"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	"github.com/getkin/kin-openapi/openapi3"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

const maxSpecBytes = 20 * 1024 * 1024

type loadedSpec struct {
	Config SpecConfig
	Source string
	Doc    *openapi3.T
}

func loadSpecs(ctx context.Context, sys fpsystem.System, workspace runtimeworkspace.Workspace, cfg Config) ([]loadedSpec, []error) {
	var out []loadedSpec
	var errs []error
	for i, spec := range cfg.Specs {
		loaded, err := loadSpec(ctx, sys, workspace, spec)
		if err != nil {
			errs = append(errs, fmt.Errorf("specs[%d]: %w", i, err))
			continue
		}
		out = append(out, loaded)
	}
	return out, errs
}

func loadSpec(ctx context.Context, sys fpsystem.System, workspace runtimeworkspace.Workspace, cfg SpecConfig) (loadedSpec, error) {
	if sys == nil {
		return loadedSpec{}, fmt.Errorf("system is nil")
	}
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = readFromURI(ctx, sys, workspace)
	var (
		data     []byte
		location *url.URL
		source   string
		err      error
	)
	if cfg.URL != "" {
		location, err = url.Parse(cfg.URL)
		if err != nil {
			return loadedSpec{}, fmt.Errorf("parse url: %w", err)
		}
		data, err = readRemote(ctx, sys, location)
		source = cfg.URL
	} else {
		data, location, source, err = readWorkspaceFile(ctx, workspace, cfg.File)
	}
	if err != nil {
		return loadedSpec{}, err
	}
	doc, err := loader.LoadFromDataWithPath(data, location)
	if err != nil {
		return loadedSpec{}, fmt.Errorf("parse openapi: %w", err)
	}
	if strings.TrimSpace(doc.OpenAPI) == "" {
		return loadedSpec{}, fmt.Errorf("openapi version is empty")
	}
	if doc.Paths == nil {
		return loadedSpec{}, fmt.Errorf("openapi paths are empty")
	}
	return loadedSpec{Config: cfg, Source: source, Doc: doc}, nil
}

func readFromURI(ctx context.Context, sys fpsystem.System, workspace runtimeworkspace.Workspace) openapi3.ReadFromURIFunc {
	return func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		switch strings.ToLower(location.Scheme) {
		case "http", "https":
			return readRemote(ctx, sys, location)
		case "", "file":
			data, _, _, err := readWorkspaceFile(ctx, workspace, location.Path)
			return data, err
		default:
			return nil, fmt.Errorf("unsupported openapi ref scheme %q", location.Scheme)
		}
	}
}

func readRemote(ctx context.Context, sys fpsystem.System, location *url.URL) ([]byte, error) {
	if sys == nil || sys.Network() == nil {
		return nil, fmt.Errorf("network system is nil")
	}
	client := systemkit.NewHTTPClient(sys.Network(), systemkit.WithHTTPClientMaxBytes(maxSpecBytes))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/yaml, application/json, text/yaml, text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", location.String(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: http %d", location.String(), resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSpecBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSpecBytes {
		return nil, fmt.Errorf("fetch %s: response exceeds %d bytes", location.String(), maxSpecBytes)
	}
	return data, nil
}

func readWorkspaceFile(ctx context.Context, workspace runtimeworkspace.Workspace, raw string) ([]byte, *url.URL, string, error) {
	if workspace == nil {
		return nil, nil, "", fmt.Errorf("workspace system is nil")
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "file://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, nil, "", err
		}
		raw = u.Path
	}
	resolved, err := workspace.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read %s: %w", raw, err)
	}
	fsys, err := runtimeworkspace.FileSystem(workspace)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read %s: %w", raw, err)
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, fsys, runtimeworkspace.PathName(resolved), maxSpecBytes)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read %s: %w", raw, err)
	}
	if truncated {
		return nil, nil, "", fmt.Errorf("read %s: file exceeds %d bytes", raw, maxSpecBytes)
	}
	location := &url.URL{Scheme: "file", Path: filepath.ToSlash(resolved.Abs)}
	return data, location, resolved.Rel, nil
}
