// Package agentfactory turns composed agent specs into runnable agent
// implementations.
//
// It is orchestration-layer code because it resolves resources, projects
// model-facing tools, and selects runtime agent implementations without owning
// provider transport.
package agentfactory
