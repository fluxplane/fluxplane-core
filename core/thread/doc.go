// Package thread models durable conversation threads and branches.
//
// Threads are a semantic projection over events, not the generic event store
// itself. The thread model preserves conversation-specific concepts such as
// branch ancestry, fork points, node IDs, metadata, and archive state.
package thread
