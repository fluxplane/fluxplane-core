// Package resource defines pure contribution identity and metadata.
//
// Resource adapters read external formats and produce contribution bundles.
// Orchestration turns those bundles into executable app/session composition.
// Resource IDs and resolvers let multiple contributions share local names while
// remaining distinguishable by origin and namespace.
// Core resource types must not read files, inspect directories, instantiate
// plugins, or execute commands.
package resource
