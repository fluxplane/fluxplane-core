// Package directchannel provides an in-process channel client over the harness.
//
// It is a small proof adapter for local apps and tests. Network protocols such
// as HTTP/SSE should live in their own adapters and translate to the same
// harness calls.
package directchannel
