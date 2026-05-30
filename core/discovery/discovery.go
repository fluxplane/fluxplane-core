package discovery

import shared "github.com/fluxplane/fluxplane-endpoint"

// Request describes one endpoint discovery request.
type Request = shared.DiscoveryRequest

// Candidate is a possible endpoint discovered by a source.
type Candidate = shared.DiscoveryCandidate

// DetectorSpec declares product-neutral candidate matching hints.
type DetectorSpec = shared.DetectorSpec

// ProbeSpec declares a safe probe. Core does not execute it.
type ProbeSpec = shared.ProbeSpec

// ProbeResult records the outcome of an executed probe.
type ProbeResult = shared.ProbeResult

// Result is one endpoint discovery response.
type Result = shared.DiscoveryResult
