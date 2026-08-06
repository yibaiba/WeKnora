package handler

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	ollamaCloudConnectionCompletionTokens = 32
	ollamaCloudConnectionResponseSchema   = `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`
)

type ollamaCloudConnectionResponse struct {
	OK *bool `json:"ok"`
}

func isOllamaCloudModel(model *types.Model) bool {
	if model == nil {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(model.Parameters.Provider))
	return provider.ProviderName(name) == provider.ProviderOllamaCloud
}

func ollamaCloudConnectionProbe() ([]chat.Message, *chat.ChatOptions) {
	thinking := false
	return []chat.Message{{
			Role: "user", Content: `请验证结构化输出能力，并返回 {"ok":true}。`,
		}}, &chat.ChatOptions{
			Temperature: 0, TemperatureSet: true, MaxTokens: ollamaCloudConnectionCompletionTokens,
			Thinking: &thinking, Format: json.RawMessage(ollamaCloudConnectionResponseSchema),
		}
}

func validateOllamaCloudConnectionResponse(response *types.ChatResponse) error {
	if response == nil {
		return errors.New("empty response")
	}
	decoder := json.NewDecoder(strings.NewReader(response.Content))
	decoder.DisallowUnknownFields()
	var payload ollamaCloudConnectionResponse
	if err := decoder.Decode(&payload); err != nil {
		return errors.New("invalid JSON object")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("additional JSON value")
	}
	if payload.OK == nil || !*payload.OK {
		return errors.New("missing successful probe result")
	}
	return nil
}
