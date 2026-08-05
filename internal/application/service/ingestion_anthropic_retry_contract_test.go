package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

type anthropicRetryContractServer struct {
	mu       sync.Mutex
	requests []map[string]any
}

func (s *anthropicRetryContractServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var requestBody map[string]any
	_ = json.NewDecoder(r.Body).Decode(&requestBody)
	s.mu.Lock()
	s.requests = append(s.requests, requestBody)
	attempt := len(s.requests)
	s.mu.Unlock()
	if attempt == 1 {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4096")
		w.Header().Set("Retry-After", "6")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"private provider response"}}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "msg_retry", "type": "message", "role": "assistant",
		"content": []any{map[string]any{
			"type": "text", "text": validMapEvidenceJSON("anthropic mapped"),
		}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 3, "output_tokens": 2},
	})
}

func (s *anthropicRetryContractServer) snapshots() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.requests...)
}

func TestAnthropicMapUsesRetryAfterWhenRateLimitBodyInterrupted(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	handler := &anthropicRetryContractServer{}
	server := httptest.NewServer(handler)
	defer server.Close()
	model, err := chat.NewAnthropicChat(&chat.ChatConfig{
		Source: types.ModelSourceRemote, BaseURL: server.URL,
		ModelName: "claude-sonnet-4-5", ModelID: "model-id", APIKey: "test-key",
		Provider: string(provider.ProviderAnthropic),
	})
	require.NoError(t, err)
	logs := &lockedBuffer{}
	logger.SetOutput(logs)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	var waits []time.Duration
	unit := newTestAnalysisUnit(0, 0, 4, "私密正文")

	_, attempts, err := analyzeIngestionDocumentUnit(context.Background(), ingestionDocumentMapUnitRequest{
		Model: model, Unit: unit, TotalUnits: 1, RetryPolicy: recordingRetryPolicy(&waits),
	})

	require.NoError(t, err)
	require.Equal(t, 2, attempts)
	require.Equal(t, []time.Duration{6 * time.Second}, waits)
	requests := handler.snapshots()
	require.Len(t, requests, 2)
	require.Equal(t, requests[0], requests[1])
	require.Equal(t, float64(0), requests[0]["temperature"])
	require.Equal(t, float64(ingestionDocumentAnalysisCompletionTokens), requests[0]["max_tokens"])
	require.NotContains(t, requests[0], "max_completion_tokens")
	outputConfig := requests[0]["output_config"].(map[string]any)
	format := outputConfig["format"].(map[string]any)
	require.Equal(t, "json_schema", format["type"])
	require.Equal(t, false, format["schema"].(map[string]any)["additionalProperties"])
	require.NotContains(t, logs.String(), "私密正文")
	require.NotContains(t, logs.String(), "private provider response")
}
