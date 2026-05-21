package discovery

import "github.com/fluxplane/engine/core/endpoint"

// Request describes one endpoint discovery request.
type Request struct {
	Op        string            `json:"op,omitempty"`
	Providers []string          `json:"providers,omitempty"`
	Product   string            `json:"product,omitempty"`
	Products  []string          `json:"products,omitempty"`
	Query     map[string]string `json:"query,omitempty"`
	Limit     int               `json:"limit,omitempty"`
}

// Candidate is a possible endpoint discovered by a source.
type Candidate struct {
	ID          string             `json:"id"`
	URL         string             `json:"url,omitempty"`
	Scheme      string             `json:"scheme,omitempty"`
	Host        string             `json:"host,omitempty"`
	Port        int                `json:"port,omitempty"`
	PortName    string             `json:"port_name,omitempty"`
	ProductHint string             `json:"product_hint,omitempty"`
	Protocol    string             `json:"protocol,omitempty"`
	AuthRef     string             `json:"auth_ref,omitempty"`
	Labels      map[string]string  `json:"labels,omitempty"`
	Annotations map[string]string  `json:"annotations,omitempty"`
	Source      endpoint.SourceRef `json:"source"`
	Reasons     []string           `json:"reasons,omitempty"`
	Score       float64            `json:"score,omitempty"`
}

// DetectorSpec declares product-neutral candidate matching hints.
type DetectorSpec struct {
	Product      string              `json:"product"`
	Names        []string            `json:"names,omitempty"`
	Labels       map[string][]string `json:"labels,omitempty"`
	Ports        []int               `json:"ports,omitempty"`
	PortNames    []string            `json:"port_names,omitempty"`
	Schemes      []string            `json:"schemes,omitempty"`
	Protocols    []string            `json:"protocols,omitempty"`
	Sources      []string            `json:"sources,omitempty"`
	ExcludeNames []string            `json:"exclude_names,omitempty"`
}

// ProbeSpec declares a safe probe. Core does not execute it.
type ProbeSpec struct {
	Product       string            `json:"product"`
	Method        string            `json:"method,omitempty"`
	Path          string            `json:"path"`
	ExpectedCodes []int             `json:"expected_codes,omitempty"`
	Timeout       string            `json:"timeout,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
}

// ProbeResult records the outcome of an executed probe.
type ProbeResult struct {
	CandidateID string            `json:"candidate_id"`
	Probe       ProbeSpec         `json:"probe"`
	Status      string            `json:"status"`
	LatencyMS   int64             `json:"latency_ms,omitempty"`
	Product     string            `json:"product,omitempty"`
	Version     string            `json:"version,omitempty"`
	Error       string            `json:"error,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Result is one endpoint discovery response.
type Result struct {
	EndpointRefs []endpoint.Ref `json:"endpoint_refs,omitempty"`
	Candidates   []Candidate    `json:"candidates,omitempty"`
	Probes       []ProbeResult  `json:"probes,omitempty"`
}
