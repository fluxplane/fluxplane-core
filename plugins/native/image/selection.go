package image

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/agentruntime/runtime/system"
)

func selectGenerationProvider(ctx context.Context, sys system.System, providers []GenerationProvider, requested string) (GenerationProvider, error) {
	requested = strings.TrimSpace(requested)
	var configured []string
	var available []string
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		info := provider.Info(ctx, sys)
		available = append(available, info.Name)
		if requested != "" && info.Name != requested {
			continue
		}
		if info.Configured {
			return provider, nil
		}
		if requested != "" {
			return nil, fmt.Errorf("image generation provider %q is not configured; missing: %s", requested, strings.Join(info.Missing, ", "))
		}
		configured = append(configured, info.Name)
	}
	if requested != "" {
		return nil, fmt.Errorf("unknown image generation provider %q; available: %s", requested, strings.Join(available, ", "))
	}
	return nil, fmt.Errorf("no configured image generation provider; available: %s", strings.Join(configured, ", "))
}

func selectUnderstandingProvider(ctx context.Context, sys system.System, providers []UnderstandingProvider, requested string) (UnderstandingProvider, error) {
	requested = strings.TrimSpace(requested)
	var available []string
	var missing []string
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		info := provider.Info(ctx, sys)
		available = append(available, info.Name)
		if requested != "" && info.Name != requested {
			continue
		}
		if info.Configured {
			return provider, nil
		}
		missing = append(missing, info.Missing...)
		if requested != "" {
			return nil, fmt.Errorf("image understanding provider %q is not configured; missing: %s", requested, strings.Join(info.Missing, ", "))
		}
	}
	if requested != "" {
		return nil, fmt.Errorf("unknown image understanding provider %q; available: %s", requested, strings.Join(available, ", "))
	}
	sort.Strings(missing)
	return nil, fmt.Errorf("no configured image understanding provider; set one of: %s", strings.Join(uniqueStrings(missing), ", "))
}

func sortProviderInfos(infos []ProviderInfo) {
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func intDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
