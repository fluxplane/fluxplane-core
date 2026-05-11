// Package operation defines the pure core model for executable units.
//
// An Operation is the smallest executable domain primitive: it receives one
// input value and returns one result. The core package describes operation
// identity, input/output contracts, semantic effect claims, result/error shapes,
// and inert event payloads.
//
// Core operation intentionally does not define middleware, validators,
// persistence, logging, approval prompts, retries, timeouts, rendering, or live
// event emission. Those are runtime or orchestration concerns.
package operation
