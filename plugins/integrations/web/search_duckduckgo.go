package web

import (
	"context"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-system/systemkit"
)

var duckDuckGoSearchURLTemplate = "https://html.duckduckgo.com/html/?q={query}"

type duckDuckGoSearchProvider struct {
	network  fpsystem.Network
	template string
}

func newDuckDuckGoSearchProvider(network fpsystem.Network) duckDuckGoSearchProvider {
	return duckDuckGoSearchProvider{network: network, template: duckDuckGoSearchURLTemplate}
}

func (p duckDuckGoSearchProvider) Name() string { return SearchProviderDuckDuckGo }

func (p duckDuckGoSearchProvider) Available(context.Context) bool {
	return p.network != nil
}

func (p duckDuckGoSearchProvider) Search(ctx context.Context, req SearchProviderRequest) (SearchProviderResult, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return SearchProviderResult{}, fmt.Errorf("query is required")
	}
	if !p.Available(ctx) {
		return SearchProviderResult{}, fmt.Errorf("web search provider %q is not available; network is not configured", SearchProviderDuckDuckGo)
	}
	resp, err := systemkit.DoHTTP(ctx, p.network, systemkit.HTTPRequest{
		URL:       duckDuckGoSearchURL(p.template, query),
		Method:    "GET",
		Timeout:   30 * time.Second,
		MaxBytes:  512 * 1024,
		UserAgent: "fluxplane/0.1",
	})
	if err != nil {
		return SearchProviderResult{}, err
	}
	results := parseDuckDuckGoSearchResults(string(resp.Body), normalizeSearchMax(req.Max))
	return SearchProviderResult{Provider: SearchProviderDuckDuckGo, Query: query, Results: results}, nil
}

func duckDuckGoSearchURL(template, query string) string {
	escaped := duckDuckGoQueryEscape(query)
	if strings.Contains(template, "{query}") {
		return strings.ReplaceAll(template, "{query}", escaped)
	}
	separator := "?"
	if strings.Contains(template, "?") {
		separator = "&"
	}
	return template + separator + "q=" + escaped
}

func duckDuckGoQueryEscape(value string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			out.WriteByte(c)
		case c == ' ':
			out.WriteByte('+')
		default:
			out.WriteByte('%')
			out.WriteByte(hex[c>>4])
			out.WriteByte(hex[c&0x0f])
		}
	}
	return out.String()
}

var (
	duckDuckGoResultLinkRE = regexp.MustCompile(`(?is)<a[^>]+class=["'][^"']*result__a[^"']*["'][^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	duckDuckGoSnippetRE    = regexp.MustCompile(`(?is)<(?:a|div)[^>]+class=["'][^"']*result__snippet[^"']*["'][^>]*>(.*?)</(?:a|div)>`)
	duckDuckGoTagRE        = regexp.MustCompile(`(?is)<[^>]+>`)
)

func parseDuckDuckGoSearchResults(body string, limit int) []SearchResult {
	matches := duckDuckGoResultLinkRE.FindAllStringSubmatchIndex(body, -1)
	results := make([]SearchResult, 0, minInt(len(matches), limit))
	for i, match := range matches {
		if limit > 0 && len(results) >= limit {
			break
		}
		url := normalizeDuckDuckGoSearchURL(body[match[2]:match[3]])
		title := cleanDuckDuckGoHTML(body[match[4]:match[5]])
		if url == "" || title == "" {
			continue
		}
		nextStart := len(body)
		if i+1 < len(matches) {
			nextStart = matches[i+1][0]
		}
		window := body[match[1]:nextStart]
		snippet := ""
		if snippetMatch := duckDuckGoSnippetRE.FindStringSubmatch(window); len(snippetMatch) > 1 {
			snippet = cleanDuckDuckGoHTML(snippetMatch[1])
		}
		results = append(results, SearchResult{
			URL:     url,
			Title:   title,
			Snippet: snippet,
			Source:  SearchProviderDuckDuckGo,
		})
	}
	return results
}

func normalizeDuckDuckGoSearchURL(raw string) string {
	value := html.UnescapeString(strings.TrimSpace(raw))
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	if idx := strings.Index(value, "uddg="); idx >= 0 {
		encoded := value[idx+len("uddg="):]
		if end := strings.IndexByte(encoded, '&'); end >= 0 {
			encoded = encoded[:end]
		}
		if decoded := duckDuckGoPercentDecode(encoded); decoded != "" {
			value = decoded
		}
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return ""
}

func duckDuckGoPercentDecode(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '%' && i+2 < len(value) {
			hi, okHi := duckDuckGoHexValue(value[i+1])
			lo, okLo := duckDuckGoHexValue(value[i+2])
			if okHi && okLo {
				out.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		if value[i] == '+' {
			out.WriteByte(' ')
			continue
		}
		out.WriteByte(value[i])
	}
	return out.String()
}

func duckDuckGoHexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func cleanDuckDuckGoHTML(value string) string {
	text := duckDuckGoTagRE.ReplaceAllString(value, " ")
	text = html.UnescapeString(text)
	return strings.Join(strings.Fields(text), " ")
}
