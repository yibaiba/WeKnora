package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
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
	mapResponse         func([]chat.Message) *types.ChatResponse
	agentRequiredText   string
	logSensitivePayload bool

	mu            sync.Mutex
	mapCalls      [][]chat.Message
	mapOptions    []chat.ChatOptions
	agentRedacted bool
}

func (m *ingestionAdvisorV2Model) Chat(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (*types.ChatResponse, error) {
	if m.logSensitivePayload {
		logger.Infof(ctx, "sensitive document map/reduce payload: %v", messages)
	}
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
	if m.mapResponse != nil {
		return m.mapResponse(messages), nil
	}
	return mapEvidenceResponse("完整正文的聚合证据"), nil
}

func (m *ingestionAdvisorV2Model) ChatStream(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	if m.logSensitivePayload {
		logger.Infof(ctx, "sensitive document agent payload: %v", messages)
	}
	m.mu.Lock()
	m.agentRedacted = types.LLMTracePayloadsRedacted(ctx)
	m.mu.Unlock()
	if m.agentWaitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if m.agentRequiredText != "" && !messagesContainText(messages, m.agentRequiredText) {
		return nil, errors.New("agent did not receive required aggregate evidence")
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
	model := &ingestionAdvisorV2Model{agent: agentModel, logSensitivePayload: true}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)
	var logs bytes.Buffer
	logger.SetOutput(&logs)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })

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
	require.True(t, model.agentRedacted)
	agentRunJSON, marshalErr := json.Marshal(result.AgentRun)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(agentRunJSON), request.Content)
	require.NotContains(t, string(agentRunJSON), "完整正文的聚合证据")
	require.NotContains(t, logs.String(), request.Content)
	require.NotContains(t, logs.String(), "完整正文的聚合证据")
}

func TestModelIngestionAdvisorV2UsesEvidenceOutsideFormerSampleWindows(t *testing.T) {
	const evidenceMarker = "faq_marker_outside_sample"
	request := validIngestionAdvisorRequest()
	request.PromptVersion = types.IngestionPromptVersionV2
	request.Content = strings.Repeat("常", 5000) + evidenceMarker + strings.Repeat("规", 19000)
	config := validIngestionRecommendation()
	normalized, err := normalizeIngestionPreviewConfig(config, request.ChunkingConstraints)
	require.NoError(t, err)
	candidateID, err := ingestionCandidateID(normalized)
	require.NoError(t, err)
	previewArgs, err := jsonMarshalForTest(config)
	require.NoError(t, err)
	submitArgs := fmt.Sprintf(
		`{"candidate_id":%q,"document_kind":"faq","confidence":0.95,`+
			`"recommended_content_mode":"faq_candidate","reason_codes":["full_text_faq_signal"],`+
			`"summary":"全文证据包含 FAQ 信号"}`,
		candidateID,
	)
	agentModel := &ingestionAdvisorScriptedModel{responses: [][]types.StreamResponse{
		toolResponse("preview-faq", previewIngestionChunkingTool, previewArgs),
		toolResponse("submit-faq", submitIngestionDecisionTool, submitArgs),
	}}
	model := &ingestionAdvisorV2Model{
		agent: agentModel, agentRequiredText: evidenceMarker,
		mapResponse: evidenceAwareMapResponse(evidenceMarker),
	}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	result, err := advisor.Analyze(context.Background(), request, interfaces.IngestionAdvisorRuntime{})

	require.NoError(t, err)
	require.Equal(t, types.IngestionDocumentKindFAQ, result.Analysis.DocumentKind)
	require.Equal(t, types.IngestionContentModeFAQCandidate, result.Analysis.RecommendedContentMode)
	require.Greater(t, countAnalysisCalls(model.mapCalls, "Map 阶段"), 1)
	require.Equal(t, 1, countAnalysisCalls(model.mapCalls, "Reduce 阶段"))
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

func messagesContainText(messages []chat.Message, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}

func evidenceAwareMapResponse(marker string) func([]chat.Message) *types.ChatResponse {
	return func(messages []chat.Message) *types.ChatResponse {
		if !messagesContainText(messages, marker) {
			return mapEvidenceResponse("ordinary section")
		}
		return ingestionEvidenceResponse(ingestionDocumentEvidence{
			Summary: marker, DocumentKindCandidates: []string{types.IngestionDocumentKindFAQ},
			ContentModeCandidates: []string{types.IngestionContentModeFAQCandidate},
			StructureSignals:      []string{"question and answer pairs"},
			ChunkingSignals:       []string{"preserve question-answer boundaries"},
		})
	}
}

func ingestionEvidenceResponse(evidence ingestionDocumentEvidence) *types.ChatResponse {
	payload, err := json.Marshal(evidence)
	if err != nil {
		panic(err)
	}
	return &types.ChatResponse{Content: string(payload)}
}

func countAnalysisCalls(calls [][]chat.Message, stage string) int {
	count := 0
	for _, messages := range calls {
		if messagesContainText(messages, stage) {
			count++
		}
	}
	return count
}
