// Package usage defines generic, provider-neutral resource metering events.
//
// LLM token usage is normalized at provider boundaries before events are
// emitted. llm.input_tokens is the standard uncached input bucket;
// llm.cached_input_tokens is cache-read input; llm.cache_write_input_tokens is
// cache-creation input; and llm.total_tokens is the inclusive request total.
// Reasoning tokens are a provider-reported output detail and are not added to
// totals unless the provider's total already includes them.
package usage
