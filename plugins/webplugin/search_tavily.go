package webplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/runtime/system"
)

const tavilySearchURL = "https://api.tavily.com/search"

type tavilySearchProvider struct {
	system system.System
	apiKey string
}

func newTavilySearchProvider(sys system.System) tavilySearchProvider {
	return tavilySearchProvider{system: sys, apiKey: env(sys, "TAVILY_API_KEY")}
}

func (p tavilySearchProvider) Name() string { return SearchProviderTavily }

func (p tavilySearchProvider) Available(context.Context) bool {
	return strings.TrimSpace(p.apiKey) != "" && p.system != nil && p.system.Network() != nil
}

func (p tavilySearchProvider) Search(ctx context.Context, req SearchProviderRequest) (SearchProviderResult, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return SearchProviderResult{}, fmt.Errorf("query is required")
	}
	if !p.Available(ctx) {
		return SearchProviderResult{}, fmt.Errorf("web search provider %q is not available; TAVILY_API_KEY is not set", SearchProviderTavily)
	}
	max := normalizeSearchMax(req.Max)
	body, err := json.Marshal(tavilySearchRequest{
		Query:             query,
		SearchDepth:       "basic",
		Topic:             "general",
		MaxResults:        max,
		IncludeAnswer:     false,
		IncludeRawContent: false,
		IncludeImages:     false,
	})
	if err != nil {
		return SearchProviderResult{}, err
	}
	resp, err := p.system.Network().DoHTTP(ctx, system.HTTPRequest{
		URL:    tavilySearchURL,
		Method: "POST",
		Headers: map[string]string{
			"Authorization": "Bearer " + p.apiKey,
			"Content-Type":  "application/json",
		},
		Body:      string(body),
		Timeout:   30 * time.Second,
		MaxBytes:  1024 * 1024,
		UserAgent: "agentruntime/0.1",
	})
	if err != nil {
		return SearchProviderResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SearchProviderResult{}, fmt.Errorf("tavily search failed: %s: %s", resp.Status, tavilyErrorMessage(resp.Body))
	}
	var decoded tavilySearchResponse
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		return SearchProviderResult{}, fmt.Errorf("decode tavily response: %w", err)
	}
	results := make([]SearchResult, 0, len(decoded.Results))
	for _, result := range decoded.Results {
		url := strings.TrimSpace(result.URL)
		if url == "" {
			continue
		}
		results = append(results, SearchResult{
			URL:     url,
			Title:   strings.TrimSpace(result.Title),
			Snippet: strings.TrimSpace(result.Content),
			Source:  SearchProviderTavily,
		})
	}
	return SearchProviderResult{
		Provider: SearchProviderTavily,
		Query:    firstNonEmpty(decoded.Query, query),
		Answer:   strings.TrimSpace(decoded.Answer),
		Results:  results,
	}, nil
}

type tavilySearchRequest struct {
	Query             string `json:"query"`
	SearchDepth       string `json:"search_depth"`
	Topic             string `json:"topic"`
	MaxResults        int    `json:"max_results"`
	IncludeAnswer     bool   `json:"include_answer"`
	IncludeRawContent bool   `json:"include_raw_content"`
	IncludeImages     bool   `json:"include_images"`
}

type tavilySearchResponse struct {
	Query        string               `json:"query"`
	Answer       string               `json:"answer"`
	Results      []tavilySearchResult `json:"results"`
	ResponseTime float64              `json:"response_time"`
}

type tavilySearchResult struct {
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	RawContent string  `json:"raw_content"`
	Favicon    string  `json:"favicon"`
}

func tavilyErrorMessage(body []byte) string {
	var decoded struct {
		Detail any `json:"detail"`
	}
	if err := json.Unmarshal(body, &decoded); err == nil && decoded.Detail != nil {
		switch detail := decoded.Detail.(type) {
		case string:
			return detail
		case map[string]any:
			if msg, ok := detail["error"].(string); ok && strings.TrimSpace(msg) != "" {
				return msg
			}
		}
	}
	return strings.TrimSpace(string(body))
}
