package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestFullDocumentAnalysisUsesModelContextWindowForMapUnits(t *testing.T) {
	content := strings.Repeat("章节内容。", 3000) + strings.Repeat("终", 32)
	mapCounts := make([]int, 0, 2)
	for _, contextWindow := range []int{4096, 32768} {
		model := &ingestionAdvisorFullTextModel{
			contextWindowTokens: contextWindow,
			agent:               &ingestionAdvisorScriptedModel{},
		}
		var progress []types.IngestionDocumentAnalysisProgress
		_, err := analyzeFullIngestionDocument(context.Background(), model, types.IngestionAdvisorRequest{
			Content: content,
			AnalysisProgressFn: func(event types.IngestionDocumentAnalysisProgress) {
				progress = append(progress, event)
			},
		})

		require.NoError(t, err)
		mapCounts = append(mapCounts, countAnalysisCalls(model.mapCalls, "Map 阶段"))
		require.Equal(t, contextWindow, progress[0].ContextWindowTokens)
		require.Equal(t, utf8.RuneCountInString(content), progress[0].CoveredCharacters)
		require.Positive(t, progress[0].ContentTokenBudget)
	}
	require.Greater(t, mapCounts[0], mapCounts[1])
}

func TestRemoteMapRetryPreservesStrictRequestBody(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	const privateBody = "private document body"
	const privateProviderBody = "private provider response"
	var captured []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requestBody map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
		captured = append(captured, requestBody)
		w.Header().Set("Content-Type", "application/json")
		if len(captured) < ingestionDocumentAnalysisMaximumAttempts {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"` + privateProviderBody + `"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant", "content": validMapEvidenceJSON("mapped"),
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{},
		})
	}))
	defer server.Close()
	model, err := chat.NewRemoteAPIChat(&chat.ChatConfig{
		Source: types.ModelSourceRemote, BaseURL: server.URL + "/v1",
		ModelName: "gpt-4o", ModelID: "model-id", APIKey: "test-key", Provider: "openai",
	})
	require.NoError(t, err)
	logs := &lockedBuffer{}
	logger.SetOutput(logs)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	unit := newTestAnalysisUnit(0, 0, utf8.RuneCountInString(privateBody), privateBody)

	_, attempts, err := analyzeIngestionDocumentUnit(context.Background(), ingestionDocumentMapUnitRequest{
		Model: model, Unit: unit, TotalUnits: 1,
	})

	require.NoError(t, err)
	require.Equal(t, ingestionDocumentAnalysisMaximumAttempts, attempts)
	require.Len(t, captured, ingestionDocumentAnalysisMaximumAttempts)
	for index := 1; index < len(captured); index++ {
		require.Equal(t, captured[0], captured[index])
	}
	require.Equal(t, float64(0), captured[0]["temperature"])
	require.Equal(t, float64(ingestionDocumentAnalysisCompletionTokens), captured[0]["max_tokens"])
	require.NotContains(t, captured[0], "max_completion_tokens")
	responseFormat := captured[0]["response_format"].(map[string]any)
	require.Equal(t, "json_schema", responseFormat["type"])
	require.Equal(t, true, responseFormat["json_schema"].(map[string]any)["strict"])
	require.NotContains(t, logs.String(), privateBody)
	require.NotContains(t, logs.String(), privateProviderBody)
}

func TestRemoteMapRetriesInterruptedResponseBody(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			w.Header().Set("Content-Length", "4096")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"choices":[`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant", "content": validMapEvidenceJSON("mapped"),
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{},
		})
	}))
	defer server.Close()
	model, err := chat.NewRemoteAPIChat(&chat.ChatConfig{
		Source: types.ModelSourceRemote, BaseURL: server.URL + "/v1",
		ModelName: "gpt-4o", ModelID: "model-id", APIKey: "test-key", Provider: "openai",
	})
	require.NoError(t, err)
	unit := newTestAnalysisUnit(0, 0, 4, "正文内容")

	_, attempts, err := analyzeIngestionDocumentUnit(context.Background(), ingestionDocumentMapUnitRequest{
		Model: model, Unit: unit, TotalUnits: 1,
		RetryPolicy: ingestionDocumentAnalysisRetryPolicy{
			Wait: func(context.Context, time.Duration) error { return nil },
		},
	})

	require.NoError(t, err)
	require.Equal(t, 2, attempts)
	require.Equal(t, 2, requests)
}

func TestRemoteReasoningMapUsesRetryAfterWithoutChangingRequest(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var captured []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requestBody map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
		captured = append(captured, requestBody)
		w.Header().Set("Content-Type", "application/json")
		if len(captured) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant", "content": validMapEvidenceJSON("mapped"),
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{},
		})
	}))
	defer server.Close()
	model, err := chat.NewRemoteAPIChat(&chat.ChatConfig{
		Source: types.ModelSourceRemote, BaseURL: server.URL + "/v1",
		ModelName: "gpt-5", ModelID: "gpt-5", APIKey: "test-key", Provider: "openai",
	})
	require.NoError(t, err)
	var waits []time.Duration
	unit := newTestAnalysisUnit(0, 0, 4, "正文内容")

	_, attempts, err := analyzeIngestionDocumentUnit(context.Background(), ingestionDocumentMapUnitRequest{
		Model: model, Unit: unit, TotalUnits: 1, RetryPolicy: recordingRetryPolicy(&waits),
	})

	require.NoError(t, err)
	require.Equal(t, 2, attempts)
	require.Equal(t, []time.Duration{7 * time.Second}, waits)
	require.Len(t, captured, 2)
	require.Equal(t, captured[0], captured[1])
	require.Equal(t, float64(ingestionDocumentAnalysisCompletionTokens), captured[0]["max_completion_tokens"])
	require.NotContains(t, captured[0], "max_tokens")
	require.NotContains(t, captured[0], "temperature")
}

func TestModelIngestionAdvisorReduceFinalFailureDoesNotStartAgent(t *testing.T) {
	retryAfter := time.Duration(0)
	reduceErr := chat.NewProviderErrorWithDetails(chat.ProviderFailureDetails{
		Kind: chat.ProviderFailureRateLimited, StatusCode: http.StatusTooManyRequests,
		RetryAfter: &retryAfter,
	})
	request := validIngestionAdvisorRequest()
	request.Content = strings.Repeat("完整正文。", 5000)
	var progress []types.IngestionDocumentAnalysisProgress
	request.AnalysisProgressFn = func(event types.IngestionDocumentAnalysisProgress) {
		progress = append(progress, event)
	}
	agentModel := &ingestionAdvisorScriptedModel{}
	model := &ingestionAdvisorFullTextModel{
		agent: agentModel, contextWindowTokens: 4096, reduceErr: reduceErr,
	}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	result, err := advisor.Analyze(context.Background(), request, interfaces.IngestionAdvisorRuntime{})

	require.Nil(t, result)
	require.Error(t, err)
	require.Empty(t, agentModel.calls)
	require.Greater(t, countAnalysisCalls(model.mapCalls, "Map 阶段"), 1)
	require.Equal(t,
		ingestionDocumentAnalysisMaximumAttempts,
		countAnalysisCalls(model.mapCalls, "Reduce 阶段"),
	)
	terminal := terminalAnalysisProgress(progress)
	require.NotEmpty(t, terminal)
	require.Equal(t, ingestionAnalysisProgressFailed, terminal[len(terminal)-1].Status)
	require.Equal(t, 2, terminal[len(terminal)-1].RetryCount)
	require.Equal(t, 3, terminal[len(terminal)-1].FailedUnitAttempts)
}
