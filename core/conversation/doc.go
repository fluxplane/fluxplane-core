// Package conversation defines provider transcript contracts.
//
// Conversation transcripts are not generic summaries. They preserve the
// provider-visible request/response item sequence and continuation handles so
// adapters can replay or continue a model conversation without changing cache
// keys through lossy reconstruction.
package conversation
