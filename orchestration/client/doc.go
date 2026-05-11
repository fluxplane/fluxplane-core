// Package client defines the user-facing channel/session/run handles.
//
// Implementations may be in-process adapters, such as directchannel, or remote
// protocol adapters, such as a future HTTP/SSE channel client. Callers should
// see the same handle-oriented API regardless of transport.
package client
