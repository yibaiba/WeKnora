// Package providers registers Ollama Cloud's OpenAI-compatible chat API.
//
// Facts (https://docs.ollama.com/cloud and https://ollama.com):
//   - chat is served from https://ollama.com/v1 with bearer auth;
//   - the output cap is `max_tokens`;
//   - `response_format` is not a reliable structured-output path, so this
//     vendor leaves SupportsResponseFormat off and WeKnora injects a schema
//     prompt instead;
//   - ExtraBody pins temperature 0 when the caller does not set one, which
//     keeps the connection probe and ingestion map/reduce deterministic.
package providers

import (
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// OllamaCloudID is the provider identifier stored on model rows.
const OllamaCloudID = "ollama_cloud"

// OllamaCloudBaseURL is the documented OpenAI-compatible cloud endpoint.
const OllamaCloudBaseURL = "https://ollama.com/v1"

func newOllamaCloudProvider() *Definition {
	return &Definition{
		ID:           OllamaCloudID,
		Name:         "Ollama Cloud",
		Names:        map[string]string{"zh-CN": "Ollama Cloud"},
		Description:  "Ollama Cloud OpenAI-compatible chat (deepseek-v4-flash:cloud, ...)",
		Website:      "https://ollama.com",
		Icon:         genericIcon,
		API:          api.APIOpenAICompletions,
		Order:        31,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"ollama.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OllamaCloudBaseURL,
		},
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
		Compat: VendorCompat{
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField:         api.Ptr("max_tokens"),
				ThinkingFormat:         api.Ptr(api.ThinkingFormatNone),
				SupportsResponseFormat: api.Ptr(false),
				ExtraBody:              map[string]any{"temperature": 0},
			},
		},
	}
}
