package chat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

const streamDumpSecret = "private aggregated ingestion evidence"

func TestRemoteStreamRawDumpSkipsRedactedSDKAndRawCalls(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(writeTestOpenAIStream))
	defer server.Close()
	model, err := NewRemoteAPIChat(&ChatConfig{
		Source: types.ModelSourceRemote, BaseURL: server.URL + "/v1",
		ModelName: "gpt-4o", ModelID: "gpt-4o", APIKey: "test-key", Provider: "openai",
	})
	require.NoError(t, err)
	ctx := types.WithRedactedLLMTracePayloads(context.Background())

	for _, options := range []*ChatOptions{{Temperature: 0.7}, {Temperature: 0, TemperatureSet: true}} {
		directory := t.TempDir()
		t.Setenv("WEKNORA_LLM_STREAM_RAW_DUMP_DIR", directory)
		stream, err := model.ChatStream(ctx, []Message{{Role: "user", Content: streamDumpSecret}}, options)
		require.NoError(t, err)
		for range stream {
		}
		entries, err := os.ReadDir(directory)
		require.NoError(t, err)
		require.Empty(t, entries)
	}
}

func TestStreamRawDumpStillWritesOrdinaryDiagnostics(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("WEKNORA_LLM_STREAM_RAW_DUMP_DIR", directory)
	dumper := newStreamPacketDumper(
		context.Background(), "ordinary-model", map[string]any{"prompt": streamDumpSecret},
	)
	require.NotNil(t, dumper)
	dumper.WritePacketRaw([]byte(`{"content":"ordinary packet"}`))
	dumper.WriteHTTPError(http.StatusBadGateway, []byte(`{"message":"ordinary error"}`))
	dumper.Close()

	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	payload, err := os.ReadFile(filepath.Join(directory, entries[0].Name()))
	require.NoError(t, err)
	require.Contains(t, string(payload), streamDumpSecret)
	require.Contains(t, string(payload), "ordinary packet")
	require.Contains(t, string(payload), "ordinary error")
}

func writeTestOpenAIStream(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, strings.Join([]string{
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"private stream response"}}]}`,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"data: [DONE]", "",
	}, "\n\n"))
}
