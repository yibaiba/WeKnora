package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type ingestionAdvisorChatStub struct {
	content     string
	err         error
	options     *chat.ChatOptions
	prompt      string
	deadline    time.Time
	hasDeadline bool
}

func (s *ingestionAdvisorChatStub) Chat(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (*types.ChatResponse, error) {
	s.options = options
	s.deadline, s.hasDeadline = ctx.Deadline()
	if len(messages) > 0 {
		s.prompt = messages[0].Content
	}
	if s.err != nil {
		return nil, s.err
	}
	return &types.ChatResponse{Content: s.content}, nil
}

func (s *ingestionAdvisorChatStub) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *ingestionAdvisorChatStub) GetModelName() string { return "advisor-stub" }
func (s *ingestionAdvisorChatStub) GetModelID() string   { return "model-1" }

type ingestionAdvisorModelServiceStub struct {
	interfaces.ModelService
	model chat.Chat
	err   error
}

func (s *ingestionAdvisorModelServiceStub) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, s.err
}

func validAdvisorModelResponse() advisorModelResponse {
	return advisorModelResponse{
		DocumentKind:           types.IngestionDocumentKindPolicyManual,
		Confidence:             0.92,
		RecommendedContentMode: types.IngestionContentModeDocument,
		ReasonCodes:            []string{"heading_rich", "long_sections"},
		Summary:                "层级清晰的制度类文档",
		RecommendedChunking: types.IngestionChunkingRecommendation{
			Strategy:          "heading",
			ChunkSize:         700,
			ChunkOverlap:      100,
			EnableParentChild: true,
			ParentChunkSize:   4096,
			ChildChunkSize:    384,
			Separators:        []string{"\n\n", "\n", "。", "！", "？"},
		},
	}
}

func TestParseIngestionAdvisorResponseAcceptsOnlyStrictValidJSON(t *testing.T) {
	valid := validAdvisorModelResponse()
	validJSON, err := json.Marshal(valid)
	require.NoError(t, err)

	tests := []struct {
		name string
		raw  string
	}{
		{name: "malformed", raw: `{"document_kind":`},
		{name: "markdown fence", raw: "```json\n" + string(validJSON) + "\n```"},
		{name: "unknown field", raw: string(validJSON[:len(validJSON)-1]) + `,"extra":true}`},
		{name: "trailing value", raw: string(validJSON) + `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseIngestionAdvisorResponse(test.raw)
			require.Error(t, err)
		})
	}

	parsed, err := parseIngestionAdvisorResponse(string(validJSON))
	require.NoError(t, err)
	require.Equal(t, valid, *parsed)
}

func TestValidateIngestionAnalysisRejectsInvalidEnumsAndBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*advisorModelResponse)
	}{
		{name: "document kind", mutate: func(v *advisorModelResponse) { v.DocumentKind = "other" }},
		{name: "content mode", mutate: func(v *advisorModelResponse) { v.RecommendedContentMode = "faq" }},
		{name: "confidence", mutate: func(v *advisorModelResponse) { v.Confidence = 1.1 }},
		{name: "strategy", mutate: func(v *advisorModelResponse) { v.RecommendedChunking.Strategy = "semantic" }},
		{name: "chunk size", mutate: func(v *advisorModelResponse) { v.RecommendedChunking.ChunkSize = 99 }},
		{name: "overlap", mutate: func(v *advisorModelResponse) { v.RecommendedChunking.ChunkOverlap = 351 }},
		{name: "parent size", mutate: func(v *advisorModelResponse) { v.RecommendedChunking.ParentChunkSize = 511 }},
		{name: "child size", mutate: func(v *advisorModelResponse) { v.RecommendedChunking.ChildChunkSize = 2049 }},
		{name: "child larger than parent", mutate: func(v *advisorModelResponse) {
			v.RecommendedChunking.ParentChunkSize = 512
			v.RecommendedChunking.ChildChunkSize = 1024
		}},
		{name: "separator", mutate: func(v *advisorModelResponse) { v.RecommendedChunking.Separators = []string{"<cut>"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validAdvisorModelResponse()
			test.mutate(&value)
			require.Error(t, validateAdvisorModelResponse(value))
		})
	}
}

func TestModelIngestionAdvisorUsesSchemaAndServerProvenance(t *testing.T) {
	modelResponse := validAdvisorModelResponse()
	raw, err := json.Marshal(modelResponse)
	require.NoError(t, err)
	model := &ingestionAdvisorChatStub{content: string(raw)}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model})

	analysis, err := advisor.Analyze(context.Background(), types.IngestionAdvisorRequest{
		Content:       "# Policy\n\nContent",
		ModelID:       "model-1",
		PromptVersion: types.IngestionPromptVersionV1,
	})

	require.NoError(t, err)
	require.Equal(t, "model-1", analysis.ModelID)
	require.Equal(t, types.IngestionPromptVersionV1, analysis.PromptVersion)
	require.NotNil(t, model.options)
	require.NotEmpty(t, model.options.Format)
	require.True(t, model.hasDeadline)
	require.WithinDuration(t, time.Now().Add(ingestionAdvisorTimeout), model.deadline, time.Second)
	require.Contains(t, model.prompt, `"statistics"`)
	require.Contains(t, model.prompt, `"sample"`)
}

func TestModelIngestionAdvisorSurfacesMissingModelAndProviderErrors(t *testing.T) {
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{})
	_, err := advisor.Analyze(context.Background(), types.IngestionAdvisorRequest{
		Content:       "content",
		PromptVersion: types.IngestionPromptVersionV1,
	})
	require.ErrorContains(t, err, "未配置摘要模型")

	providerErr := errors.New("provider unavailable")
	advisor = NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{err: providerErr})
	_, err = advisor.Analyze(context.Background(), types.IngestionAdvisorRequest{
		Content:       "content",
		ModelID:       "model-1",
		PromptVersion: types.IngestionPromptVersionV1,
	})
	require.ErrorIs(t, err, providerErr)
}

func TestValidateIngestionAdvisorConfigModes(t *testing.T) {
	require.NoError(t, ValidateIngestionAdvisorConfig(nil))
	require.NoError(t, ValidateIngestionAdvisorConfig(&types.IngestionAdvisorConfig{Mode: types.IngestionAdvisorModeSmart}))
	require.NoError(t, ValidateIngestionAdvisorConfig(&types.IngestionAdvisorConfig{Mode: types.IngestionAdvisorModeOff}))
	require.Error(t, ValidateIngestionAdvisorConfig(&types.IngestionAdvisorConfig{Mode: "automatic"}))
	require.Error(t, ValidateIngestionAdvisorConfig(&types.IngestionAdvisorConfig{
		Mode: types.IngestionAdvisorModeSmart, PromptVersion: "v2",
	}))
}
