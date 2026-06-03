// Package operation re-exports the portable operation contract from
// github.com/fluxplane/fluxplane-operation for existing Core APIs.
//
// Runtime and orchestration packages may add middleware, validators,
// persistence, logging, approval prompts, retries, timeouts, rendering, or live
// event emission around this contract. The contract itself is owned by the leaf
// module.
package operation
