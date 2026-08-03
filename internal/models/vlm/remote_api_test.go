package vlm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildChatCompletionRequestUsesGPT5CompatibleParameters(t *testing.T) {
	models := []string{"gpt-5", "gpt-5.6", "o1-mini", "o3", "o4-mini"}
	for _, modelName := range models {
		t.Run(modelName, func(t *testing.T) {
			vlm := &RemoteAPIVLM{modelName: modelName, temperature: defaultTemp}
			req := vlm.buildChatCompletionRequest(nil, "describe the image")

			if req.MaxTokens != 0 {
				t.Fatalf("MaxTokens = %d, want 0", req.MaxTokens)
			}
			if req.MaxCompletionTokens != defaultMaxToks {
				t.Fatalf("MaxCompletionTokens = %d, want %d", req.MaxCompletionTokens, defaultMaxToks)
			}
			if req.Temperature != 0 {
				t.Fatalf("Temperature = %f, want 0", req.Temperature)
			}

			payload, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			body := string(payload)
			if strings.Contains(body, `"max_tokens"`) || strings.Contains(body, `"temperature"`) {
				t.Fatalf("unsupported GPT-5 parameters present in request: %s", body)
			}
			if !strings.Contains(body, `"max_completion_tokens":5000`) {
				t.Fatalf("max_completion_tokens missing from request: %s", body)
			}
		})
	}
}

func TestBuildChatCompletionRequestKeepsLegacyParameters(t *testing.T) {
	vlm := &RemoteAPIVLM{modelName: "gpt-4o", temperature: defaultTemp}
	req := vlm.buildChatCompletionRequest(nil, "describe the image")

	if req.MaxTokens != defaultMaxToks {
		t.Fatalf("MaxTokens = %d, want %d", req.MaxTokens, defaultMaxToks)
	}
	if req.MaxCompletionTokens != 0 {
		t.Fatalf("MaxCompletionTokens = %d, want 0", req.MaxCompletionTokens)
	}
	if req.Temperature != defaultTemp {
		t.Fatalf("Temperature = %f, want %f", req.Temperature, defaultTemp)
	}
}
