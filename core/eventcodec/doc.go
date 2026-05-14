// Package eventcodec contains pure helpers for event record normalization and
// typed payload JSON encoding.
//
// Core defines event payloads, records, registries, and store contracts. This
// package owns deterministic record validation, sensitivity defaults, cloning,
// and JSON payload codec behavior. Stores pass in the current time so IO-backed
// timestamp decisions stay outside core.
package eventcodec
