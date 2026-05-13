# Datasource Embeddings

Datasource semantic search uses a runtime `Embedder` interface plus a separate
semantic index store. Datasource providers expose corpus text; the semantic
index service chunks that corpus, embeds changed chunks, and writes vectors plus
incremental metadata.

## Default Provider

The default embedding provider is the local Axon/Hugot adapter:

```yaml
semantic_search:
  embeddings:
    provider: axon
```

It wraps `github.com/codewandler/axon/indexer/embeddings.NewHugot` from the
local `../../axon` module. The default model is:

```text
KnightsAnalytics/all-MiniLM-L6-v2
```

The provider runs in-process on CPU through Hugot/GoMLX. No embedding text is
sent to an external service. The model is lazy-loaded on first use. If model
files are not already present, the first real embedding call downloads the ONNX
model to Axon's local cache under `~/.axon/models/...`.

The model identifier stored in index metadata is prefixed with the adapter:

```text
axon/hugot/KnightsAnalytics/all-MiniLM-L6-v2
```

Changing the embedding provider or model causes incremental indexing to
re-index affected documents.

## Hash Provider

A deterministic hash embedder remains available for tests and fast smoke
checks:

```bash
agentsdk datasource index build examples/slack-bot \
  --datasource local-docs \
  --entity file.document \
  --provider hash
```

The hash provider does not perform real semantic embedding. Use it only when
testing the indexing pipeline without loading or downloading the local model.

## Store

The current semantic index store is a JSON file store, not SQLite. By default it
is written under the app root:

```text
.agents/index/datasources.json
```

The CLI can override this path:

```bash
agentsdk datasource index build examples/slack-bot \
  --store /tmp/agentruntime-semantic-smoke.json
```

The JSON store is suitable for local development and pipeline validation. Large
or long-lived corpora should move to a dedicated SQLite/vector backend.

## Chunking

Default chunking is conservative for the Axon/Hugot MiniLM model sequence
limit:

```yaml
semantic_search:
  defaults:
    chunking:
      target_tokens: 350
      overlap_tokens: 50
```

The current chunker uses a simple character approximation rather than a
provider tokenizer. Provider-token-aware chunking is the next refinement before
large production corpora.

## CLI

Build or update an index:

```bash
agentsdk datasource index build <app-dir> \
  --datasource local-docs \
  --entity file.document \
  --full
```

Show index status:

```bash
agentsdk datasource index status <app-dir> \
  --datasource local-docs \
  --entity file.document
```

Clear indexed records:

```bash
agentsdk datasource index clear <app-dir> \
  --datasource local-docs \
  --entity file.document
```

Provider selection:

```bash
--provider axon   # default local CPU Hugot provider
--provider hash   # deterministic test/smoke provider
```

`--provider openai` is reserved but not implemented yet.
