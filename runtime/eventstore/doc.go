// Package eventstore contains surface-neutral event store implementations.
//
// IO-free mutable stores, such as the in-memory store, live here rather than in
// core because they assign record IDs, timestamps, schema defaults, sequence
// numbers, and concurrency behavior. Durable filesystem/database-backed stores
// live in adapters.
package eventstore
