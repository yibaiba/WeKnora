package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type ingestionAdvisorV2Model struct {
	agent               *ingestionAdvisorScriptedModel
	mapErr              error
	mapWaitForContext   bool
	agentWaitForContext bool

	mu         sync.Mutex
	mapCalls   [][]chat.Message
	mapOptions []chat.ChatOptions
}

func (m *ingestionAdvisorV2Model) Chat(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.mu.Lock()
	m.mapCalls = append(m.mapCalls, append([]chat.Message(nil), messages...))
	m.mapOptions = append(m.mapOptions, *options)
	m.mu.Unlock()
	if m.mapWaitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if m.mapErr != nil {
		return nil, m.mapErr
	}
	return mapEvidenceResponse("完整正文的聚合证据"), nil
}

func (m *ingestionAdvisorV2Model) ChatStream(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	if m.agentWaitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return m.agent.ChatStream(ctx, messages, options)
}

func (m *ingestionAdvisorV2Model) GetModelName() string { return "advisor-v2" }
func (m *ingestionAdvisorV2Model) GetModelID() string   { return "model-1" }

func TestModelIngestionAdvisorV2MapsFullTextBeforePreviewAndSubmission(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.PromptVersion = ""
	config := validIngestionRecommendation()
	normalized, err := normalizeIngestionPreviewConfig(config, request.ChunkingConstraints)
	require.NoError(t, err)
	candidateID, err := ingestionCandidateID(normalized)
	require.NoError(t, err)
	previewArgs, err := jsonMarshalForTest(config)
	require.NoError(t, err)
	submitArgs := fmt.Sprintf(
		`{"candidate_id":%q,"document_kind":"policy_manual","confidence":0.92,`+
			`"recommended_content_mode":"document","reason_codes":["full_text_evidence"],`+
			`"summary":"根据全文证据选择标题切分"}`,
		candidateID,
	)
	agentModel := &ingestionAdvisorScriptedModel{responses: [][]types.StreamResponse{
		toolResponse("preview-v2", previewIngestionChunkingTool, previewArgs),
		toolResponse("submit-v2", submitIngestionDecisionTool, submitArgs),
	}}
	model := &ingestionAdvisorV2Model{agent: agentModel}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	result, err := advisor.Analyze(context.Background(), request, interfaces.IngestionAdvisorRuntime{})

	require.NoError(t, err)
	require.Equal(t, candidateID, result.SelectedCandidateID)
	require.Equal(t, 2, result.AgentRun.ActualRounds)
	require.NotContains(t, result.AgentRun.AvailableTools, inspectIngestionDocumentTool)
	require.Contains(t, result.AgentRun.AvailableTools, previewIngestionChunkingTool)
	require.Contains(t, result.AgentRun.AvailableTools, submitIngestionDecisionTool)
	require.Len(t, model.mapCalls, 1)
	require.Contains(t, model.mapCalls[0][1].Content, request.Content)
	require.Equal(t, ingestionDocumentAnalysisCompletionTokens, model.mapOptions[0].MaxCompletionTokens)
	require.Len(t, agentModel.calls, 2)
	require.Contains(t, agentModel.calls[0][0].Content, "Map-Reduce")
	require.Contains(t, agentModel.calls[0][1].Content, `"aggregated_evidence"`)
	require.Contains(t, agentModel.calls[0][1].Content, "完整正文的聚合证据")
	require.NotContains(t, agentModel.calls[0][1].Content, request.Content)
}

func TestModelIngestionAdvisorV2AnalysisFailureDoesNotStartAgent(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.PromptVersion = types.IngestionPromptVersionV2
	agentModel := &ingestionAdvisorScriptedModel{}
	model := &ingestionAdvisorV2Model{
		agent: agentModel, mapErr: errors.New("provider error echoed private body"),
	}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	result, err := advisor.Analyze(context.Background(), request, interfaces.IngestionAdvisorRuntime{})

	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorDocumentAnalysis, ingestionAdvisorRunErrorCode(err))
	require.NotContains(t, err.Error(), "private body")
	require.Empty(t, agentModel.calls)
}

func TestModelIngestionAdvisorV2TotalTimeoutStopsDuringMap(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.PromptVersion = types.IngestionPromptVersionV2
	request.Timeout = 20 * time.Millisecond
	agentModel := &ingestionAdvisorScriptedModel{}
	model := &ingestionAdvisorV2Model{agent: agentModel, mapWaitForContext: true}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)
	started := time.Now()

	result, err := advisor.Analyze(context.Background(), request, interfaces.IngestionAdvisorRuntime{})

	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorDocumentAnalysis, ingestionAdvisorRunErrorCode(err))
	require.Contains(t, err.Error(), "总超时")
	require.Less(t, time.Since(started), time.Second)
	require.Empty(t, agentModel.calls)
}

func TestModelIngestionAdvisorV2TotalTimeoutCoversAgent(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.PromptVersion = types.IngestionPromptVersionV2
	request.Timeout = 20 * time.Millisecond
	model := &ingestionAdvisorV2Model{
		agent: &ingestionAdvisorScriptedModel{}, agentWaitForContext: true,
	}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)
	started := time.Now()

	_, err := advisor.Analyze(context.Background(), request, interfaces.IngestionAdvisorRuntime{})

	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorDocumentAnalysis, ingestionAdvisorRunErrorCode(err))
	require.Contains(t, err.Error(), "总超时")
	require.Less(t, time.Since(started), time.Second)
	require.Len(t, model.mapCalls, 1)
}

func TestNewIngestionAgentSessionV2DoesNotRetainSampleBody(t *testing.T) {
	content := "sensitive full body"

	session := newIngestionAgentSessionForPromptVersion(
		content, types.IngestionChunkingConstraints{}, types.IngestionPromptVersionV2,
	)

	require.Equal(t, len([]rune(content)), session.profile.Statistics.CharacterCount)
	require.Equal(t, types.DocumentContentSample{}, session.profile.Sample)
}
