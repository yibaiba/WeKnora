package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaCloudUsesSchemaPromptWithStableStrictRequest(t *testing.T) {
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	requests := make([]map[string]any, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests = append(requests, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"summary\":\"Canada\"}"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer server.Close()

	model, err := NewRemoteAPIChat(&ChatConfig{
		Source: types.ModelSourceRemote, BaseURL: server.URL + "/v1",
		ModelName: "deepseek-v4-flash:cloud", APIKey: "test-key",
		Provider: string(provider.ProviderOllamaCloud),
	})
	require.NoError(t, err)
	messages := []Message{
		{Role: "system", Content: "Analyze the document."},
		{Role: "user", Content: "Tell me about Canada."},
	}
	schema := json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","minLength":1}},"required":["summary"],"additionalProperties":false}`)
	thinking := false
	opts := &ChatOptions{
		Temperature: 0, TemperatureSet: true, MaxTokens: 1024,
		Thinking: &thinking, Format: schema,
	}

	for range 2 {
		response, callErr := model.Chat(context.Background(), messages, opts)
		require.NoError(t, callErr)
		require.JSONEq(t, `{"summary":"Canada"}`, response.Content)
	}

	require.Len(t, requests, 2)
	assert.Equal(t, requests[0], requests[1])
	assertOllamaCloudSchemaPromptRequest(t, requests[0])
	assert.Equal(t, "Analyze the document.", messages[0].Content)
	assert.JSONEq(t, string(schema), string(opts.Format))
}

func assertOllamaCloudSchemaPromptRequest(
	t *testing.T,
	request map[string]any,
) {
	t.Helper()
	assert.Equal(t, float64(0), request["temperature"])
	assert.Equal(t, float64(1024), request["max_tokens"])
	assert.NotContains(t, request, "max_completion_tokens")
	assert.NotContains(t, request, "response_format")
	assert.NotContains(t, request, "format")
	assert.NotContains(t, request, "tools")

	messages, ok := request["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 2)
	system, ok := messages[0].(map[string]any)
	require.True(t, ok)
	content, ok := system["content"].(string)
	require.True(t, ok)
	assert.Contains(t, content, "Analyze the document.")
	assert.Contains(t, content, "不得输出 Markdown")
	assert.Contains(t, content, `"summary":{"minLength":1,"type":"string"}`)
	assert.Contains(t, content, `<json_example>{"summary":"value"}</json_example>`)
}

func TestOllamaCloudRejectsInvalidPromptSchemaBeforeRequest(t *testing.T) {
	model := newOutboundChat(t, string(provider.ProviderOllamaCloud), "cloud-model", nil)

	_, _, _, err := model.buildOutbound(
		[]Message{{Role: "user", Content: "analyze"}},
		&ChatOptions{Format: json.RawMessage(`{"type":"object"} trailing`)},
		false,
	)

	require.Error(t, err)
	details, ok := ProviderErrorDetails(err)
	require.True(t, ok)
	assert.Equal(t, ProviderFailureRequestInvalid, details.Kind)
	assert.Equal(t, "response_format", details.Parameter)
}

func TestOllamaCloudSchemaPromptPrependsSystemMessage(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`)
	messages, err := withStructuredOutputPrompt([]openai.ChatCompletionMessage{{
		Role: openai.ChatMessageRoleUser, Content: "analyze",
	}}, schema)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, openai.ChatMessageRoleSystem, messages[0].Role)
	assert.Equal(t, "analyze", messages[1].Content)
}
