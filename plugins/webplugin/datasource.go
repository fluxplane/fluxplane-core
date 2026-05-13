package webplugin

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const SearchResultEntity coredatasource.EntityType = "web.search_result"

var _ pluginhost.DatasourceProviderContributor = Plugin{}

type SearchResult struct {
	URL     string `json:"url" datasource:"id,url,searchable" jsonschema:"description=Search result URL.,required"`
	Title   string `json:"title,omitempty" datasource:"searchable" jsonschema:"description=Search result title."`
	Snippet string `json:"snippet,omitempty" datasource:"searchable" jsonschema:"description=Search result snippet."`
	Source  string `json:"source,omitempty" datasource:"filterable" jsonschema:"description=Search provider source."`
}

// DatasourceProviders returns web-backed datasource providers.
func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
	return []coredatasource.Provider{webSearchProvider(p)}, nil
}

type webSearchProvider struct {
	system system.System
}

func (p webSearchProvider) Entities() []coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[SearchResult](SearchResultEntity, "Web search result.")
	entity.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch}
	return []coredatasource.EntitySpec{entity}
}

func (p webSearchProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if !specHasEntity(spec, SearchResultEntity) {
		return nil, fmt.Errorf("unsupported entities %q", spec.Entities)
	}
	if spec.Kind != "web" && spec.Kind != "websearch" && spec.Kind != "web_search" {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	if p.system == nil {
		return nil, fmt.Errorf("web datasource system is nil")
	}
	return &webSearchAccessor{system: p.system, spec: spec, entity: p.Entities()[0]}, nil
}

type webSearchAccessor struct {
	system system.System
	spec   coredatasource.Spec
	entity coredatasource.EntitySpec
}

func (a *webSearchAccessor) Spec() coredatasource.Spec { return a.spec }

func (a *webSearchAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}

func (a *webSearchAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	if req.Entity != SearchResultEntity {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return coredatasource.SearchResult{}, fmt.Errorf("web search query is empty")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	url := searchURL(firstNonEmpty(a.spec.Config["search_url"], "https://html.duckduckgo.com/html/?q={query}"), query)
	resp, err := a.system.Network().DoHTTP(ctx, system.HTTPRequest{
		URL:       url,
		Method:    "GET",
		Timeout:   30 * time.Second,
		MaxBytes:  512 * 1024,
		UserAgent: "agentruntime/0.1",
	})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	records := parseSearchResults(string(resp.Body), limit)
	for i := range records {
		records[i].Datasource = a.spec.Name
		records[i].Entity = SearchResultEntity
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: req.Entity, Records: records, Total: len(records)}, nil
}

func searchURL(template, query string) string {
	escaped := queryEscape(query)
	if strings.Contains(template, "{query}") {
		return strings.ReplaceAll(template, "{query}", escaped)
	}
	separator := "?"
	if strings.Contains(template, "?") {
		separator = "&"
	}
	return template + separator + "q=" + escaped
}

func queryEscape(value string) string {
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
	resultLinkRE = regexp.MustCompile(`(?is)<a[^>]+class=["'][^"']*result__a[^"']*["'][^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	snippetRE    = regexp.MustCompile(`(?is)<(?:a|div)[^>]+class=["'][^"']*result__snippet[^"']*["'][^>]*>(.*?)</(?:a|div)>`)
	tagRE        = regexp.MustCompile(`(?is)<[^>]+>`)
)

func parseSearchResults(body string, limit int) []coredatasource.Record {
	matches := resultLinkRE.FindAllStringSubmatchIndex(body, -1)
	records := make([]coredatasource.Record, 0, minInt(len(matches), limit))
	for i, match := range matches {
		if limit > 0 && len(records) >= limit {
			break
		}
		url := normalizeSearchURL(body[match[2]:match[3]])
		title := cleanHTML(body[match[4]:match[5]])
		if url == "" || title == "" {
			continue
		}
		nextStart := len(body)
		if i+1 < len(matches) {
			nextStart = matches[i+1][0]
		}
		window := body[match[1]:nextStart]
		snippet := ""
		if snippetMatch := snippetRE.FindStringSubmatch(window); len(snippetMatch) > 1 {
			snippet = cleanHTML(snippetMatch[1])
		}
		records = append(records, coredatasource.Record{
			ID:       url,
			URL:      url,
			Title:    title,
			Content:  snippet,
			Metadata: map[string]string{"source": "web"},
			Raw: SearchResult{
				URL:     url,
				Title:   title,
				Snippet: snippet,
				Source:  "web",
			},
		})
	}
	return records
}

func normalizeSearchURL(raw string) string {
	value := html.UnescapeString(strings.TrimSpace(raw))
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	if idx := strings.Index(value, "uddg="); idx >= 0 {
		encoded := value[idx+len("uddg="):]
		if end := strings.IndexByte(encoded, '&'); end >= 0 {
			encoded = encoded[:end]
		}
		if decoded := percentDecode(encoded); decoded != "" {
			value = decoded
		}
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return ""
}

func percentDecode(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '%' && i+2 < len(value) {
			hi, okHi := hexValue(value[i+1])
			lo, okLo := hexValue(value[i+2])
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

func hexValue(c byte) (byte, bool) {
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

func cleanHTML(value string) string {
	text := tagRE.ReplaceAllString(value, " ")
	text = html.UnescapeString(text)
	return strings.Join(strings.Fields(text), " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func specHasEntity(spec coredatasource.Spec, entity coredatasource.EntityType) bool {
	for _, candidate := range spec.Entities {
		if candidate == entity {
			return true
		}
	}
	return false
}
