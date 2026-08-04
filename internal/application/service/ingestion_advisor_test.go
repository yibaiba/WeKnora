package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type ingestionAdvisorScriptedModel struct {
	mu        sync.Mutex
	responses [][]types.StreamResponse
	calls     [][]chat.Message
	options   []*chat.ChatOptions
}

func (m *ingestionAdvisorScriptedModel) Chat(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (*types.ChatResponse, error) {
	return nil, errors.New("unexpected non-streaming call")
}

func (m *ingestionAdvisorScriptedModel) ChatStream(
	_ context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := len(m.calls)
	if index >= len(m.responses) {
		return nil, fmt.Errorf("unexpected ChatStream call %d", index+1)
	}
	m.calls = append(m.calls, append([]chat.Message(nil), messages...))
	m.options = append(m.options, options)
	result := make(chan types.StreamResponse, len(m.responses[index]))
	for _, response := range m.responses[index] {
		result <- response
	}
	close(result)
	return result, nil
}

func (m *ingestionAdvisorScriptedModel) GetModelName() string { return "advisor-script" }
func (m *ingestionAdvisorScriptedModel) GetModelID() string   { return "model-1" }

type ingestionAdvisorModelServiceStub struct {
	interfaces.ModelService
	model chat.Chat
	err   error
}

func (s *ingestionAdvisorModelServiceStub) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, s.err
}

func validReactIngestionAnalysis() *types.IngestionAnalysis {
	return &types.IngestionAnalysis{
		DocumentKind:           types.IngestionDocumentKindPolicyManual,
		Confidence:             0.92,
		RecommendedContentMode: types.IngestionContentModeDocument,
		ReasonCodes:            []string{"heading_rich", "balanced_chunks"},
		Summary:                "层级清晰的制度类文档",
		RecommendedChunking:    validIngestionRecommendation(),
	}
}

func validIngestionRecommendation() types.IngestionChunkingRecommendation {
	return types.IngestionChunkingRecommendation{
		Strategy:          "heading",
		ChunkSize:         700,
		ChunkOverlap:      100,
		EnableParentChild: true,
		ParentChunkSize:   4096,
		ChildChunkSize:    384,
		Separators:        []string{"\n\n", "\n", "。", "！", "？"},
	}
}

func validIngestionAdvisorRequest() types.IngestionAdvisorRequest {
	return types.IngestionAdvisorRequest{
		Content:           "# Policy\n\nFirst section.\n\n## Scope\n\nSecond section.",
		KnowledgeID:       "knowledge-1",
		KnowledgeBaseID:   "kb-1",
		KnowledgeBaseName: "Policies",
		KnowledgeBaseType: types.KnowledgeBaseTypeDocument,
		TenantID:          1,
		VectorEnabled:     true,
		KeywordEnabled:    true,
		ModelID:           "model-1",
		PromptVersion:     types.IngestionPromptVersionV1,
	}
}

func toolResponse(id, name, arguments string) []types.StreamResponse {
	return []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		ToolCalls: []types.LLMToolCall{{
			ID: id, Function: types.FunctionCall{Name: name, Arguments: arguments},
		}},
		Done: true, FinishReason: "tool_calls",
	}}
}

func TestModelIngestionAdvisorRunsPreviewThenTerminalSubmission(t *testing.T) {
	config := validIngestionRecommendation()
	normalized, err := normalizeIngestionPreviewConfig(config)
	require.NoError(t, err)
	candidateID, err := ingestionCandidateID(normalized)
	require.NoError(t, err)
	previewArgs, err := jsonMarshalForTest(config)
	require.NoError(t, err)
	submitArgs := fmt.Sprintf(
		`{"candidate_id":%q,"document_kind":"policy_manual","confidence":0.92,`+
			`"recommended_content_mode":"document","reason_codes":["heading_rich","balanced_chunks"],`+
			`"summary":"层级清晰的制度类文档"}`,
		candidateID,
	)
	model := &ingestionAdvisorScriptedModel{responses: [][]types.StreamResponse{
		toolResponse("preview-1", previewIngestionChunkingTool, previewArgs),
		toolResponse("submit-1", submitIngestionDecisionTool, submitArgs),
	}}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	result, err := advisor.Analyze(context.Background(), validIngestionAdvisorRequest())

	require.NoError(t, err)
	require.NotNil(t, result.Analysis)
	require.Equal(t, candidateID, result.SelectedCandidateID)
	require.Equal(t, "termination_tool", result.AgentRun.StopReason)
	require.Equal(t, 2, result.AgentRun.ActualRounds)
	require.Len(t, result.Candidates, 1)
	require.Equal(t, normalized, result.Analysis.RecommendedChunking)
	require.Equal(t, float64(0), model.options[0].Temperature)
	require.Contains(t, model.calls[0][0].Content, "submit_ingestion_decision")
	require.NotContains(t, model.calls[0][1].Content, "First section")
	require.Contains(t, result.AgentRun.AvailableTools, inspectIngestionDocumentTool)
	require.Contains(t, result.AgentRun.AvailableTools, previewIngestionChunkingTool)
	require.Contains(t, result.AgentRun.AvailableTools, submitIngestionDecisionTool)
}

func TestModelIngestionAdvisorRejectsNaturalAnswerWithoutTools(t *testing.T) {
	model := &ingestionAdvisorScriptedModel{responses: [][]types.StreamResponse{{{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "plain answer",
		Done:         true,
		FinishReason: "stop",
	}}}}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	result, err := advisor.Analyze(context.Background(), validIngestionAdvisorRequest())

	require.ErrorContains(t, err, "不支持原生工具调用")
	require.NotNil(t, result)
	require.Nil(t, result.Analysis)
}

func TestModelIngestionAdvisorSurfacesMissingModelAndProviderErrors(t *testing.T) {
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{}, nil)
	request := validIngestionAdvisorRequest()
	request.ModelID = ""
	_, err := advisor.Analyze(context.Background(), request)
	require.ErrorContains(t, err, "未配置摘要模型")

	providerErr := errors.New("provider unavailable")
	advisor = NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{err: providerErr}, nil)
	_, err = advisor.Analyze(context.Background(), validIngestionAdvisorRequest())
	require.ErrorIs(t, err, providerErr)
}

func TestValidateIngestionAnalysisRejectsInvalidEnumsAndBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.IngestionAnalysis)
	}{
		{name: "document kind", mutate: func(v *types.IngestionAnalysis) { v.DocumentKind = "other" }},
		{name: "content mode", mutate: func(v *types.IngestionAnalysis) { v.RecommendedContentMode = "faq" }},
		{name: "confidence", mutate: func(v *types.IngestionAnalysis) { v.Confidence = 1.1 }},
		{name: "confidence NaN", mutate: func(v *types.IngestionAnalysis) { v.Confidence = math.NaN() }},
		{name: "confidence infinity", mutate: func(v *types.IngestionAnalysis) { v.Confidence = math.Inf(1) }},
		{name: "strategy", mutate: func(v *types.IngestionAnalysis) { v.RecommendedChunking.Strategy = "semantic" }},
		{name: "chunk size", mutate: func(v *types.IngestionAnalysis) { v.RecommendedChunking.ChunkSize = 99 }},
		{name: "overlap", mutate: func(v *types.IngestionAnalysis) { v.RecommendedChunking.ChunkOverlap = 351 }},
		{name: "parent size", mutate: func(v *types.IngestionAnalysis) { v.RecommendedChunking.ParentChunkSize = 511 }},
		{name: "child size", mutate: func(v *types.IngestionAnalysis) { v.RecommendedChunking.ChildChunkSize = 2049 }},
		{name: "child larger than parent", mutate: func(v *types.IngestionAnalysis) {
			v.RecommendedChunking.ParentChunkSize = 512
			v.RecommendedChunking.ChildChunkSize = 1024
		}},
		{name: "separator", mutate: func(v *types.IngestionAnalysis) {
			v.RecommendedChunking.Separators = []string{"<cut>"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validReactIngestionAnalysis()
			test.mutate(value)
			require.Error(t, ValidateIngestionAnalysis(value))
		})
	}
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

func jsonMarshalForTest(value any) (string, error) {
	payload, err := json.Marshal(value)
	return string(payload), err
}
