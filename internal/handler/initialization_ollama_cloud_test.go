package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckChatModelConnectionOllamaCloudUsesStrictSchemaProbe(t *testing.T) {
	var request map[string]any
	server := newOllamaCloudConnectionServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeConnectionChatResponse(w, `{"ok":true}`)
	})
	defer server.Close()
	model := ollamaCloudConnectionModel(server.URL)

	available, message := (&InitializationHandler{}).checkChatModelConnection(
		context.Background(), model, "", "",
	)

	require.True(t, available)
	assert.Contains(t, message, "结构化输出验证通过")
	assert.Equal(t, float64(0), request["temperature"])
	assert.Equal(t, float64(32), request["max_tokens"])
	assert.NotContains(t, request, "max_completion_tokens")
	assert.NotContains(t, request, "response_format")
	assert.NotContains(t, request, "format")
	assert.NotContains(t, request, "tools")
	messages := request["messages"].([]any)
	require.Len(t, messages, 2)
	system := messages[0].(map[string]any)["content"].(string)
	schemaStart := strings.Index(system, "<json_schema>")
	schemaEnd := strings.Index(system, "</json_schema>")
	require.GreaterOrEqual(t, schemaStart, 0)
	require.Greater(t, schemaEnd, schemaStart)
	actualSchema := system[schemaStart+len("<json_schema>") : schemaEnd]
	assert.JSONEq(t, ollamaCloudConnectionResponseSchema, actualSchema)
	assert.Contains(t, system, `<json_example>{"ok":true}</json_example>`)
}

func TestCheckChatModelConnectionOllamaCloudRejectsMalformedOutputWithoutRetry(t *testing.T) {
	tests := []string{
		"```json\n{\"ok\":true}\n```",
		`{}`,
		`{"ok":false}`,
		`{"ok":true,"extra":"unexpected"}`,
		`{"ok":true} {"ok":true}`,
	}
	for _, content := range tests {
		t.Run(content, func(t *testing.T) {
			requests := 0
			server := newOllamaCloudConnectionServer(t, func(w http.ResponseWriter, _ *http.Request) {
				requests++
				writeConnectionChatResponse(w, content)
			})
			defer server.Close()

			available, message := (&InitializationHandler{}).checkChatModelConnection(
				context.Background(), ollamaCloudConnectionModel(server.URL), "", "",
			)

			assert.False(t, available)
			assert.Equal(t, "模型连接成功，但结构化输出验证失败", message)
			assert.Equal(t, 1, requests)
		})
	}
}

func TestCheckChatModelConnectionOllamaCloudDoesNotAcceptHTTP400(t *testing.T) {
	const privateProviderBody = "private upstream response"
	var logs bytes.Buffer
	logger.SetOutput(&logs)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	requests := 0
	server := newOllamaCloudConnectionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"` + privateProviderBody + `"}}`))
	})
	defer server.Close()

	available, message := (&InitializationHandler{}).checkChatModelConnection(
		context.Background(), ollamaCloudConnectionModel(server.URL), "", "",
	)

	assert.False(t, available)
	assert.Equal(t, 1, requests)
	assert.NotContains(t, message, privateProviderBody)
	assert.NotContains(t, logs.String(), privateProviderBody)
}

func newOllamaCloudConnectionServer(
	t *testing.T,
	handler http.HandlerFunc,
) *httptest.Server {
	t.Helper()
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	return httptest.NewServer(handler)
}

func ollamaCloudConnectionModel(serverURL string) *types.Model {
	return &types.Model{
		Name: "deepseek-v3.1:671b-cloud", Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL: serverURL + "/v1", APIKey: "test-key",
			Provider: string(provider.ProviderOllamaCloud),
		},
	}
}

func writeConnectionChatResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{},
	})
}

func TestValidateOllamaCloudConnectionResponseAcceptsSurroundingWhitespace(t *testing.T) {
	require.NoError(t, validateOllamaCloudConnectionResponse(&types.ChatResponse{
		Content: strings.Repeat(" ", 2) + `{"ok":true}` + strings.Repeat("\n", 2),
	}))
}
