// Package llm contains provider-adapter helpers for implementing
// runtime/agent/llmagent model ports.
//
// The package is intentionally provider-neutral: it defines message, tool,
// stream, redaction, and tool-call assembly shapes that real OpenAI,
// Anthropic, or other provider adapters can map to their wire formats later.
package llm
