// Package harness owns the channel-to-session use case boundary.
//
// A harness service receives normalized channel input, binds the channel
// conversation to a runtime session/thread, delegates execution to the session
// orchestrator, and publishes normalized channel output. Protocols, terminal
// UX, and wire formats belong in adapters.
package harness
