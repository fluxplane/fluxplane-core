// Package resource defines pure contribution metadata.
//
// Resource adapters read external formats and produce contribution bundles.
// Orchestration turns those bundles into executable app/session composition.
// Core resource types must not read files, inspect directories, instantiate
// plugins, or execute commands.
package resource
