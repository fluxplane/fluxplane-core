// Package command defines channel-facing invocation specs.
//
// Commands are pure descriptors for how an external actor may invoke a target
// through a channel. Slash parsing produces inert invocations; protocol
// handling, dispatch, rendering, and execution live outside core.
package command
