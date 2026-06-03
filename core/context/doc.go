// Package context defines Core's agent-runtime context materialization layer.
//
// Portable provider specs, block metadata, freshness, placement, and request
// fields live in github.com/fluxplane/fluxplane-context and are re-exported
// here where Core APIs already use them. This package adds only Core-specific
// runtime concerns: provider interfaces, evidence-aware requests, render
// records/diffs, committed state, and context render events.
package context
