package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	modelollama "github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/stretchr/testify/require"
)

type ollamaRetryContractServer struct {
	mu       sync.Mutex
	requests []map[string]any
}

func (s *ollamaRetryContractServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		w.WriteHeader(http.StatusOK)
	case "/api/tags":
		_, _ = w.Write([]byte(`{"models":[{"name":"test-model:latest"}]}`))
	case "/api/chat":
		s.handleChat(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *ollamaRetryContractServer) handleChat(w http.ResponseWriter, r *http.Request) {
	var requestBody map[string]any
	_ = json.NewDecoder(r.Body).Decode(&requestBody)
	s.mu.Lock()
	s.requests = append(s.requests, requestBody)
	attempt := len(s.requests)
	s.mu.Unlock()
	if attempt == 1 {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"partial`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model": "test-model", "done": true,
		"message": map[string]any{
			"role": "assistant", "content": validMapEvidenceJSON("ollama mapped"),
		},
	})
}

func (s *ollamaRetryContractServer) snapshots() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.requests...)
}

func TestOllamaMapRetriesInterruptedResponseBody(t *testing.T) {
	handler := &ollamaRetryContractServer{}
	server := httptest.NewServer(handler)
	defer server.Close()
	t.Setenv("OLLAMA_BASE_URL", server.URL)
	t.Setenv("OLLAMA_OPTIONAL", "false")
	service, err := modelollama.GetOllamaService()
	require.NoError(t, err)
	model, err := chat.NewOllamaChat(&chat.ChatConfig{
		ModelName: "test-model", ModelID: "test-model",
	}, service)
	require.NoError(t, err)
	var waits []time.Duration
	unit := newTestAnalysisUnit(0, 0, 4, "正文内容")

	_, attempts, err := analyzeIngestionDocumentUnit(context.Background(), ingestionDocumentMapUnitRequest{
		Model: model, Unit: unit, TotalUnits: 1, RetryPolicy: recordingRetryPolicy(&waits),
	})

	require.NoError(t, err)
	require.Equal(t, 2, attempts)
	require.Equal(t, []time.Duration{time.Second}, waits)
	requests := handler.snapshots()
	require.Len(t, requests, 2)
	require.Equal(t, requests[0], requests[1])
}
