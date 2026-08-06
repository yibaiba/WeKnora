package provider

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

const OllamaCloudBaseURL = "https://ollama.com/v1"

// OllamaCloudProvider describes Ollama's authenticated remote OpenAI-compatible API.
// Local Ollama models continue to use ModelSourceLocal and the native Ollama client.
type OllamaCloudProvider struct{}

func init() {
	Register(&OllamaCloudProvider{})
}

func (p *OllamaCloudProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderOllamaCloud,
		DisplayName: "Ollama Cloud",
		Description: "Cloud-hosted Ollama models via the OpenAI-compatible API",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OllamaCloudBaseURL,
			types.ModelTypeVLLM:        OllamaCloudBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

func (p *OllamaCloudProvider) ValidateConfig(config *Config) error {
	if config.APIKey == "" {
		return fmt.Errorf("API key is required for Ollama Cloud provider")
	}
	if config.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
