package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

type langfuseErrorChat struct {
	err error
}

func (c *langfuseErrorChat) Chat(
	context.Context, []Message, *ChatOptions,
) (*types.ChatResponse, error) {
	return nil, c.err
}

func (c *langfuseErrorChat) ChatStream(
	context.Context, []Message, *ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, c.err
}

func (c *langfuseErrorChat) GetModelName() string { return "error-model" }
func (c *langfuseErrorChat) GetModelID() string   { return "error-model-id" }

func TestBuildLangfuseGenerationOutput(t *testing.T) {
	toolCalls := []types.LLMToolCall{{ID: "call_1", Type: "function"}}

	got := buildLangfuseGenerationOutput("", "", "tool_calls", toolCalls)
	want := map[string]interface{}{
		"content":       "",
		"tool_calls":    toolCalls,
		"finish_reason": "tool_calls",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output without reasoning = %#v; want %#v", got, want)
	}

	got = buildLangfuseGenerationOutput("answer", "thinking", "stop", nil)
	want = map[string]interface{}{
		"content":           "answer",
		"tool_calls":        []types.LLMToolCall(nil),
		"finish_reason":     "stop",
		"reasoning_content": "thinking",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output with reasoning = %#v; want %#v", got, want)
	}
}

func TestSnapshotLangfuseToolCallsKeepsModelArguments(t *testing.T) {
	providerCalls := []types.LLMToolCall{{
		ID:       "call_1",
		Function: types.FunctionCall{Name: "wiki_read_page", Arguments: `{"slugs":["res://0001"]}`},
	}}
	snapshot := snapshotLangfuseToolCalls(providerCalls)
	providerCalls[0].Function.Arguments = `{"slugs":["summary/uuid"]}`

	if got := snapshot[0].Function.Arguments; got != `{"slugs":["res://0001"]}` {
		t.Fatalf("Langfuse snapshot was mutated to %s", got)
	}
}

func TestBuildLangfuseMessagesReasoningContent(t *testing.T) {
	msgs := buildLangfuseMessages([]Message{
		{Role: "assistant", ReasoningContent: "chain of thought", ToolCalls: []ToolCall{{ID: "tc1"}}},
	})
	if len(msgs) != 1 {
		t.Fatalf("len(messages) = %d; want 1", len(msgs))
	}
	if msgs[0]["reasoning_content"] != "chain of thought" {
		t.Fatalf("reasoning_content = %v; want chain of thought", msgs[0]["reasoning_content"])
	}
}

func TestBuildLangfuseGenerationInputRedactsSensitiveTaskMessages(t *testing.T) {
	ctx := types.WithRedactedLLMTracePayloads(context.Background())
	messages := []Message{
		{Role: "system", Content: "sensitive system prompt"},
		{
			Role:             "assistant",
			Content:          "private answer",
			ReasoningContent: "private chain of thought",
			ToolCalls: []ToolCall{{
				Function: FunctionCall{
					Name:      "inspect_ingestion_document",
					Arguments: `{"offset":0,"document":"private"}`,
				},
			}},
		},
		{Role: "tool", Name: "inspect_ingestion_document", Content: "private source excerpt"},
	}

	encoded, err := json.Marshal(buildLangfuseGenerationInput(ctx, messages))
	if err != nil {
		t.Fatalf("marshal redacted input: %v", err)
	}
	tracePayload := string(encoded)
	for _, sensitive := range []string{
		"sensitive system prompt", "private answer", "private chain of thought",
		`\"offset\"`, "private source excerpt",
	} {
		if strings.Contains(tracePayload, sensitive) {
			t.Fatalf("redacted input contains %q: %s", sensitive, tracePayload)
		}
	}
	if !strings.Contains(tracePayload, "inspect_ingestion_document") {
		t.Fatalf("redacted input omitted tool name: %s", tracePayload)
	}
}

func TestBuildLangfuseGenerationOutputRedactsSensitiveTaskResponse(t *testing.T) {
	ctx := types.WithRedactedLLMTracePayloads(context.Background())
	output := buildLangfuseGenerationOutputForContext(
		ctx,
		"private answer",
		"private chain of thought",
		"tool_calls",
		[]types.LLMToolCall{{Function: types.FunctionCall{
			Name:      "preview_ingestion_chunking",
			Arguments: `{"separators":["private source"]}`,
		}}},
	)
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal redacted output: %v", err)
	}
	tracePayload := string(encoded)
	for _, sensitive := range []string{"private answer", "private chain of thought", "private source"} {
		if strings.Contains(tracePayload, sensitive) {
			t.Fatalf("redacted output contains %q: %s", sensitive, tracePayload)
		}
	}
	if !strings.Contains(tracePayload, "preview_ingestion_chunking") {
		t.Fatalf("redacted output omitted tool name: %s", tracePayload)
	}
}

func TestLangfuseWrapperRedactsExportedErrorsWithoutChangingCallerError(t *testing.T) {
	const sensitive = "provider echoed private document body"
	providerErr := errors.New(sensitive)
	var exported bytes.Buffer
	var exportedMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		exportedMu.Lock()
		exported.Write(payload)
		exportedMu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	manager, err := langfuse.Init(langfuse.Config{
		Enabled: true, Host: server.URL, PublicKey: "pk", SecretKey: "sk",
		FlushAt: 1, FlushInterval: time.Second, QueueSize: 16,
		RequestTimeout: time.Second, SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("init Langfuse: %v", err)
	}
	t.Cleanup(func() {
		_ = manager.Shutdown(context.Background())
		_, _ = langfuse.Init(langfuse.Config{Enabled: false})
	})
	wrapped := &langfuseChat{inner: &langfuseErrorChat{err: providerErr}}
	ctx := types.WithRedactedLLMTracePayloads(context.Background())

	_, chatErr := wrapped.Chat(ctx, []Message{{Role: "user", Content: "private"}}, nil)
	_, streamErr := wrapped.ChatStream(ctx, []Message{{Role: "user", Content: "private"}}, nil)
	if !errors.Is(chatErr, providerErr) || !errors.Is(streamErr, providerErr) {
		t.Fatalf("caller errors changed: chat=%v stream=%v", chatErr, streamErr)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown Langfuse: %v", err)
	}
	_, _ = langfuse.Init(langfuse.Config{Enabled: false})
	exportedMu.Lock()
	defer exportedMu.Unlock()
	if bytes.Contains(exported.Bytes(), []byte(sensitive)) {
		t.Fatalf("OTLP export leaked provider error: %q", exported.String())
	}
}

func TestConvertUsageIncludesPromptCacheCounters(t *testing.T) {
	got := convertUsage(&types.TokenUsage{
		PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050,
		CacheReadTokens: 800, CacheWriteTokens: 100, CacheMissTokens: 200,
	})
	if got == nil {
		t.Fatal("convertUsage returned nil")
	}
	if got.CacheRead != 800 || got.CacheWrite != 100 || got.CacheMiss != 200 {
		t.Fatalf("cache usage = read:%d write:%d miss:%d", got.CacheRead, got.CacheWrite, got.CacheMiss)
	}
}
