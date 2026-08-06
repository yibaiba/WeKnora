package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestOllamaCloudModelConfigRunsFullMapReduceWithSchemaPrompt(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var mu sync.Mutex
	requests := make([]map[string]any, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode analysis request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		writeAnalysisChatResponse(w, validMapEvidenceJSON("Ollama Cloud aggregate"))
	}))
	defer server.Close()
	model := newOllamaCloudAnalysisModel(t, server.URL)
	content := strings.Repeat("章节内容。", 3000)

	evidence, err := analyzeFullIngestionDocument(context.Background(), model, types.IngestionAdvisorRequest{
		Content: content,
	})

	require.NoError(t, err)
	require.Equal(t, "Ollama Cloud aggregate", evidence.Summary)
	mu.Lock()
	captured := append([]map[string]any(nil), requests...)
	mu.Unlock()
	require.Greater(t, len(captured), 2)
	assertOllamaCloudAnalysisRequests(t, captured)
}

func TestOllamaCloudInvalidMapOutputIsNotRetriedOrLogged(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	const privateDocument = "private Ollama Cloud document"
	const privateProviderOutput = "private malformed provider output"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		writeAnalysisChatResponse(w, "```json\n"+validMapEvidenceJSON(privateProviderOutput)+"\n```")
	}))
	defer server.Close()
	model := newOllamaCloudAnalysisModel(t, server.URL)
	logs := &lockedBuffer{}
	logger.SetOutput(logs)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	unit := newTestAnalysisUnit(0, 0, utf8.RuneCountInString(privateDocument), privateDocument)

	_, attempts, err := analyzeIngestionDocumentUnit(context.Background(), ingestionDocumentMapUnitRequest{
		Model: model, Unit: unit, TotalUnits: 1,
	})

	require.Error(t, err)
	require.Equal(t, 1, attempts)
	require.Equal(t, 1, requests)
	require.NotContains(t, logs.String(), privateDocument)
	require.NotContains(t, logs.String(), privateProviderOutput)
}

func newOllamaCloudAnalysisModel(t *testing.T, serverURL string) chat.Chat {
	t.Helper()
	entity := &types.Model{
		ID: "ollama-cloud-model", Name: "deepseek-v3.1:671b-cloud",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL: serverURL + "/v1", APIKey: "test-key",
			Provider: string(provider.ProviderOllamaCloud), ContextWindowTokens: 4096,
		},
	}
	config := chat.ConfigFromModel(entity, "", "")
	require.Equal(t, string(provider.ProviderOllamaCloud), config.Provider)
	model, err := chat.NewChat(config, nil)
	require.NoError(t, err)
	return model
}

func writeAnalysisChatResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{},
	})
}

func assertOllamaCloudAnalysisRequests(t *testing.T, requests []map[string]any) {
	t.Helper()
	mapRequest := false
	reduceRequest := false
	for _, request := range requests {
		require.Equal(t, float64(ingestionDocumentAnalysisCompletionTokens), request["max_tokens"])
		require.NotContains(t, request, "max_completion_tokens")
		require.NotContains(t, request, "response_format")
		require.NotContains(t, request, "format")
		messages := request["messages"].([]any)
		system := messages[0].(map[string]any)["content"].(string)
		require.Contains(t, system, "<json_schema>")
		require.Contains(t, system, `"additionalProperties":false`)
		mapRequest = mapRequest || strings.Contains(system, "Map 阶段")
		reduceRequest = reduceRequest || strings.Contains(system, "Reduce 阶段")
	}
	require.True(t, mapRequest)
	require.True(t, reduceRequest)
}
