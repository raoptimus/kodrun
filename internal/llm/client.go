/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package llm

import "context"

// Client is the interface for communicating with an LLM provider.
type Client interface {
	Ping(ctx context.Context) error
	Models(ctx context.Context) ([]Model, error)
	Chat(ctx context.Context, req *ChatRequest) (<-chan ChatChunk, error)
	ChatSync(ctx context.Context, req *ChatRequest) (ChatChunk, error)
	ChatSyncWithCallback(ctx context.Context, req *ChatRequest, cb ChunkCallback) (ChatChunk, error)
	Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error)
}
