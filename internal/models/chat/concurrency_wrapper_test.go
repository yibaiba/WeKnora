package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeChat is a minimal Chat whose stream emits continuously until ctx is done,
// so we can exercise the concurrency wrapper's slot lifecycle.
type fakeChat struct{ id string }

func (f *fakeChat) GetModelName() string { return f.id }
func (f *fakeChat) GetModelID() string   { return f.id }

func (f *fakeChat) Chat(ctx context.Context, _ []Message, _ *ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{}, nil
}

func (f *fakeChat) ChatStream(ctx context.Context, _ []Message, _ *ChatOptions) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case ch <- types.StreamResponse{}:
			}
		}
	}()
	return ch, nil
}

type contextWindowFakeChat struct {
	*fakeChat
	contextWindow int
}

func (f *contextWindowFakeChat) ContextWindowTokens() int { return f.contextWindow }

func TestChatWrappersPreserveOptionalContextWindowCapability(t *testing.T) {
	inner := &contextWindowFakeChat{fakeChat: &fakeChat{id: "model-capability"}, contextWindow: 65536}
	debug := &debugChat{inner: inner}
	langfuseWrapped := &langfuseChat{inner: debug}
	wrapped := &concurrencyChat{inner: langfuseWrapped}

	if got := ResolveContextWindowTokens(wrapped); got != 65536 {
		t.Fatalf("ResolveContextWindowTokens(wrapped) = %d, want 65536", got)
	}
	if got := ResolveContextWindowTokens(&fakeChat{id: "legacy-stub"}); got != types.DefaultModelContextWindowTokens {
		t.Fatalf("legacy Chat default = %d, want %d", got, types.DefaultModelContextWindowTokens)
	}
}

type structuredPromptFakeChat struct{ *fakeChat }

func (f *structuredPromptFakeChat) StructuredOutputPrompt(schema json.RawMessage) (string, error) {
	return "prompt:" + string(schema), nil
}

func TestChatWrappersPreserveOptionalStructuredOutputPromptCapability(t *testing.T) {
	inner := &structuredPromptFakeChat{fakeChat: &fakeChat{id: "prompt-capability"}}
	wrapper := &concurrencyChat{inner: &langfuseChat{inner: &debugChat{inner: inner}}}
	schema := json.RawMessage(`{"type":"object"}`)

	prompt, err := ResolveStructuredOutputPrompt(wrapper, schema)
	if err != nil {
		t.Fatalf("ResolveStructuredOutputPrompt(wrapped): %v", err)
	}
	if prompt != `prompt:{"type":"object"}` {
		t.Fatalf("prompt = %q", prompt)
	}
	legacyPrompt, err := ResolveStructuredOutputPrompt(&fakeChat{id: "legacy"}, schema)
	if err != nil || legacyPrompt != "" {
		t.Fatalf("legacy prompt = %q, err = %v", legacyPrompt, err)
	}
}

// TestConcurrencyChatInteractiveNotGated verifies interactive calls bypass the
// governor entirely even at limit 1.
func TestConcurrencyChatInteractiveNotGated(t *testing.T) {
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })
	limiter.SetGovernor(limiter.NewLocalLimiter(), 1)

	w := &concurrencyChat{inner: &fakeChat{id: "model-x"}}
	// No background marker: neither call should be throttled.
	if _, err := w.Chat(context.Background(), nil, nil); err != nil {
		t.Fatalf("interactive chat: %v", err)
	}
	if _, err := w.Chat(context.Background(), nil, nil); err != nil {
		t.Fatalf("interactive chat 2: %v", err)
	}
}

// TestConcurrencyChatStreamReleasesOnAbandon verifies that when the consumer
// stops reading and cancels the context, the held slot is released rather than
// leaked (the #9 fix).
func TestConcurrencyChatStreamReleasesOnAbandon(t *testing.T) {
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })
	limiter.SetGovernor(limiter.NewLocalLimiter(), 1)

	const id = "model-y"
	w := &concurrencyChat{inner: &fakeChat{id: id}}

	streamCtx, cancel := context.WithCancel(types.WithBackgroundTask(context.Background()))
	out, err := w.ChatStream(streamCtx, nil, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	// Consume one item so the relay goroutine is running and holding the slot.
	<-out

	// The single slot is now held: a background acquire must block.
	blocked := make(chan struct{})
	go func() {
		rel := limiter.Gate(types.WithBackgroundTask(context.Background()), id)
		rel()
		close(blocked)
	}()
	select {
	case <-blocked:
		t.Fatal("slot should be held by the live stream")
	case <-time.After(50 * time.Millisecond):
	}

	// Abandon the stream: stop reading and cancel. The relay must release.
	cancel()
	select {
	case <-blocked:
		// released and reacquired successfully
	case <-time.After(2 * time.Second):
		t.Fatal("stream slot leaked: not released after consumer abandoned + cancelled")
	}
}
