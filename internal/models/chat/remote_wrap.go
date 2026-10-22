package chat

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
)

// wrapRemoteChat restores configured context windows on protocol clients and
// injects Ollama Cloud's schema-prompt structured output path.
func wrapRemoteChat(inner Chat, config *ChatConfig, resolved *modelruntime.Resolved) Chat {
	if inner == nil {
		return nil
	}
	tokens := 0
	if config != nil {
		tokens = config.ContextWindowTokens
	}
	wrapped := Chat(&contextWindowChat{inner: inner, tokens: tokens})
	if resolved != nil && resolved.Vendor != nil && resolved.Vendor.ID == providers.OllamaCloudID {
		wrapped = &structuredOutputChat{inner: wrapped}
	}
	return wrapped
}

type contextWindowChat struct {
	inner  Chat
	tokens int
}

func (c *contextWindowChat) GetModelName() string { return c.inner.GetModelName() }
func (c *contextWindowChat) GetModelID() string   { return c.inner.GetModelID() }
func (c *contextWindowChat) ContextWindowTokens() int {
	if c.tokens > 0 {
		return c.tokens
	}
	return ResolveContextWindowTokens(c.inner)
}
func (c *contextWindowChat) StructuredOutputPrompt(schema json.RawMessage) (string, error) {
	return ResolveStructuredOutputPrompt(c.inner, schema)
}
func (c *contextWindowChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	return c.inner.Chat(ctx, messages, opts)
}
func (c *contextWindowChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	return c.inner.ChatStream(ctx, messages, opts)
}

type structuredOutputChat struct {
	inner Chat
}

func (c *structuredOutputChat) GetModelName() string { return c.inner.GetModelName() }
func (c *structuredOutputChat) GetModelID() string   { return c.inner.GetModelID() }
func (c *structuredOutputChat) ContextWindowTokens() int {
	return ResolveContextWindowTokens(c.inner)
}
func (c *structuredOutputChat) StructuredOutputPrompt(schema json.RawMessage) (string, error) {
	return buildStructuredOutputPrompt(schema)
}

func (c *structuredOutputChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	messages, opts, err := prepareStructuredOutput(messages, opts)
	if err != nil {
		return nil, err
	}
	resp, err := c.inner.Chat(ctx, messages, opts)
	return resp, providerCallError(ctx, err)
}

func (c *structuredOutputChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	messages, opts, err := prepareStructuredOutput(messages, opts)
	if err != nil {
		return nil, err
	}
	ch, err := c.inner.ChatStream(ctx, messages, opts)
	return ch, providerCallError(ctx, err)
}

func prepareStructuredOutput(messages []Message, opts *ChatOptions) ([]Message, *ChatOptions, error) {
	if opts == nil || len(opts.Format) == 0 {
		return messages, opts, nil
	}
	injected, err := injectStructuredOutputPrompt(messages, opts.Format)
	if err != nil {
		return nil, nil, NewProviderError(ProviderFailureRequestInvalid, 0, "response_format")
	}
	cloned := *opts
	cloned.Format = nil
	return injected, &cloned, nil
}
