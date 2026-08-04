package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

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
