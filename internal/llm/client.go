/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package llm

import "context"

// Chatter is the narrow interface a consumer needs to drive a chat
// conversation. Streaming, blocking, and callback-streaming variants are
// declared together because every chat-driven consumer in this codebase
// (agent, classifier, runner) uses at least two of the three.
type Chatter interface {
	Chat(ctx context.Context, req *ChatRequest) (<-chan ChatChunk, error)
	ChatSync(ctx context.Context, req *ChatRequest) (ChatChunk, error)
	ChatSyncWithCallback(ctx context.Context, req *ChatRequest, cb ChunkCallback) (ChatChunk, error)
}

// Embedder is the narrow interface for callers that only need to compute
// embeddings (RAG indexing, similarity search).
type Embedder interface {
	Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error)
}

// Inspector is the narrow interface for connectivity / capability probes,
// used at startup to fail fast before the agent issues any real call.
type Inspector interface {
	Ping(ctx context.Context) error
	Models(ctx context.Context) ([]Model, error)
}

// Client is the full provider interface, composed of the segregated
// sub-interfaces. New code should depend on the narrowest sub-interface
// it actually uses; Client remains for callers that need every facet.
type Client interface {
	Chatter
	Embedder
	Inspector
}
