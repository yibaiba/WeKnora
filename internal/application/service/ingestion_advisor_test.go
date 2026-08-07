package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
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
	streamErr error
}

const defaultCandidatePlaceholder = "__default_candidate__"

type ingestionWebSearchServiceSpy struct {
	mu            sync.Mutex
	compressCalls int
}

func (s *ingestionWebSearchServiceSpy) Search(
	_ context.Context,
	_ string,
	_ *types.WebSearchConfig,
	query string,
) ([]*types.WebSearchResult, error) {
	suffix := "first"
	if query == "second query" {
		suffix = "second"
	}
	return []*types.WebSearchResult{{
		Title: "Chunking", URL: "https://example.com/" + suffix, Content: "raw guidance " + suffix,
	}}, nil
}

func (s *ingestionWebSearchServiceSpy) CompressWithRAG(
	ctx context.Context,
	sessionID string,
	tempKBID string,
	questions []string,
	results []*types.WebSearchResult,
	config *types.WebSearchConfig,
	kb interfaces.WebSearchTemporaryKnowledgeBaseService,
	knowledge interfaces.WebSearchTemporaryKnowledgeService,
	seen map[string]bool,
	knowledgeIDs []string,
) ([]*types.WebSearchResult, string, map[string]bool, []string, error) {
	s.mu.Lock()
	s.compressCalls++
	s.mu.Unlock()
	return (&WebSearchService{}).CompressWithRAG(
		ctx, sessionID, tempKBID, questions, results, config,
		kb, knowledge, seen, knowledgeIDs,
	)
}

type ingestionAdvisorWebKBStub struct {
	interfaces.KnowledgeBaseService
	mu                  sync.Mutex
	knowledge           *ingestionAdvisorWebKnowledgeStub
	kb                  *types.KnowledgeBase
	created             int
	deleted             []string
	firstCreateEntered  chan struct{}
	secondCreateEntered chan struct{}
	releaseFirstCreate  chan struct{}
}

func (s *ingestionAdvisorWebKBStub) CreateKnowledgeBase(
	_ context.Context,
	kb *types.KnowledgeBase,
) (*types.KnowledgeBase, error) {
	s.mu.Lock()
	s.created++
	createIndex := s.created
	s.mu.Unlock()
	if createIndex == 1 && s.firstCreateEntered != nil {
		close(s.firstCreateEntered)
		<-s.releaseFirstCreate
	}
	if createIndex == 2 && s.secondCreateEntered != nil {
		close(s.secondCreateEntered)
	}
	created := *kb
	created.ID = fmt.Sprintf("temp-kb-%d", createIndex)
	s.mu.Lock()
	s.kb = &created
	s.mu.Unlock()
	return &created, nil
}

func (s *ingestionAdvisorWebKBStub) GetKnowledgeBaseByID(
	_ context.Context,
	id string,
) (*types.KnowledgeBase, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kb == nil || s.kb.ID != id {
		return nil, errors.New("temporary knowledge base not found")
	}
	result := *s.kb
	return &result, nil
}

func (s *ingestionAdvisorWebKBStub) HybridSearch(
	context.Context,
	string,
	types.SearchParams,
) ([]*types.SearchResult, error) {
	return s.knowledge.searchResults(), nil
}

func (s *ingestionAdvisorWebKBStub) DeleteKnowledgeBase(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, id)
	return nil
}

type ingestionAdvisorWebKnowledgeStub struct {
	interfaces.WebSearchTemporaryKnowledgeService
	mu       sync.Mutex
	created  int
	contents []string
	deleted  []string
}

func (s *ingestionAdvisorWebKnowledgeStub) CreateKnowledgeFromPassageSync(
	_ context.Context,
	_ string,
	passage []string,
	_ string,
) (*types.Knowledge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created++
	s.contents = append(s.contents, strings.Join(passage, "\n"))
	return &types.Knowledge{ID: fmt.Sprintf("temp-doc-%d", s.created)}, nil
}

func (s *ingestionAdvisorWebKnowledgeStub) DeleteKnowledge(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *ingestionAdvisorWebKnowledgeStub) searchResults() []*types.SearchResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]*types.SearchResult, 0, len(s.contents))
	for index, content := range s.contents {
		results = append(results, &types.SearchResult{
			ID: fmt.Sprintf("chunk-%d", index+1), Content: content,
		})
	}
	return results
}

func (m *ingestionAdvisorScriptedModel) Chat(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (*types.ChatResponse, error) {
	return mapEvidenceResponse("完整正文的聚合证据"), nil
}

func (m *ingestionAdvisorScriptedModel) ChatStream(
	_ context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	index := len(m.calls)
	if index >= len(m.responses) {
		return nil, fmt.Errorf("unexpected ChatStream call %d", index+1)
	}
	m.calls = append(m.calls, append([]chat.Message(nil), messages...))
	m.options = append(m.options, options)
	result := make(chan types.StreamResponse, len(m.responses[index]))
	for _, response := range m.responses[index] {
		for callIndex := range response.ToolCalls {
			response.ToolCalls[callIndex].Function.Arguments = strings.ReplaceAll(
				response.ToolCalls[callIndex].Function.Arguments,
				defaultCandidatePlaceholder,
				defaultCandidateIDFromMessages(messages),
			)
		}
		result <- response
	}
	close(result)
	return result, nil
}

func defaultCandidateIDFromMessages(messages []chat.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		var context ingestionAgentContext
		payload := messages[index].Content
		if jsonStart := strings.LastIndex(payload, "\n{"); jsonStart >= 0 {
			payload = payload[jsonStart+1:]
		}
		if json.Unmarshal([]byte(payload), &context) == nil {
			return context.DefaultCandidateID
		}
	}
	return ""
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
	}
}

func toolResponse(id, name, arguments string) []types.StreamResponse {
	return toolCallsResponse(types.LLMToolCall{
		ID: id, Function: types.FunctionCall{Name: name, Arguments: arguments},
	})
}

func toolCallsResponse(calls ...types.LLMToolCall) []types.StreamResponse {
	return []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer,
		ToolCalls:    calls,
		Done:         true, FinishReason: "tool_calls",
	}}
}

func naturalResponse(content string) []types.StreamResponse {
	return []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer, Content: content,
		Done: true, FinishReason: "stop",
	}}
}

func TestModelIngestionAdvisorSubmitsBackendGeneratedCandidate(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.ChunkingConstraints = types.IngestionChunkingConstraints{
		TokenLimit: 100,
		Languages:  []string{chunker.LangEnglish},
	}
	submitArgs := fmt.Sprintf(
		`{"candidate_id":%q,"document_kind":"policy_manual","confidence":0.92,`+
			`"recommended_content_mode":"document","reason_codes":["heading_rich","balanced_chunks"],`+
			`"summary":"层级清晰的制度类文档"}`,
		defaultCandidatePlaceholder,
	)
	model := &ingestionAdvisorScriptedModel{responses: [][]types.StreamResponse{
		toolResponse("submit-1", submitIngestionDecisionTool, submitArgs),
	}}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)
	request.AllowWebAccess = true
	request.AllowReadOnlyMCP = true

	result, err := advisor.Analyze(context.Background(), request, interfaces.IngestionAdvisorRuntime{})

	require.NoError(t, err)
	require.NotNil(t, result.Analysis)
	require.NotEmpty(t, result.SelectedCandidateID)
	require.Equal(t, "termination_tool", result.AgentRun.StopReason)
	require.Equal(t, 1, result.AgentRun.ActualRounds)
	require.Len(t, result.Candidates, maxIngestionCandidates)
	require.Equal(t, []string{"highest_total_score"}, result.SelectionReasonCodes)
	require.Equal(t, float64(0), model.options[0].Temperature)
	require.Contains(t, model.calls[0][0].Content, "submit_ingestion_decision")
	require.Contains(t, model.calls[0][0].Content, "不得创建、修改或搜索分块参数")
	require.Contains(t, model.calls[0][1].Content, `"candidates"`)
	require.Contains(t, model.calls[0][1].Content, `"default_candidate_id"`)
	require.NotContains(t, model.calls[0][1].Content, "First section")
	require.NotContains(t, result.AgentRun.AvailableTools, previewIngestionChunkingTool)
	require.Contains(t, result.AgentRun.AvailableTools, submitIngestionDecisionTool)
	require.NotContains(t, result.AgentRun.AvailableTools, submitIngestionFallbackTool)
	require.Contains(t, result.AgentRun.Warnings, types.IngestionAgentWarning{
		Code: "readonly_tools_unavailable", Message: "只读 Agent 工具工厂未配置",
	})
}

func TestModelIngestionAdvisorCompressesWebResultsAndCleansTemporaryKnowledge(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.VectorEnabled = false
	request.KeywordEnabled = false
	request.AllowWebAccess = true
	submitArgs := fmt.Sprintf(
		`{"candidate_id":%q,"document_kind":"policy_manual","confidence":0.9,`+
			`"recommended_content_mode":"document","reason_codes":["web_validated"],`+
			`"summary":"结合 Web 检索验证切分方案"}`,
		defaultCandidatePlaceholder,
	)
	model := &ingestionAdvisorScriptedModel{responses: [][]types.StreamResponse{
		toolCallsResponse(
			types.LLMToolCall{ID: "web-1", Function: types.FunctionCall{
				Name: agenttools.ToolWebSearch, Arguments: `{"query":"first query"}`,
			}},
			types.LLMToolCall{ID: "web-2", Function: types.FunctionCall{
				Name: agenttools.ToolWebSearch, Arguments: `{"query":"second query"}`,
			}},
		),
		toolResponse("submit-1", submitIngestionDecisionTool, submitArgs),
	}}
	webSearch := &ingestionWebSearchServiceSpy{}
	knowledge := &ingestionAdvisorWebKnowledgeStub{}
	kb := &ingestionAdvisorWebKBStub{
		knowledge:          knowledge,
		firstCreateEntered: make(chan struct{}), secondCreateEntered: make(chan struct{}),
		releaseFirstCreate: make(chan struct{}),
	}
	go func() {
		<-kb.firstCreateEntered
		select {
		case <-kb.secondCreateEntered:
		case <-time.After(100 * time.Millisecond):
		}
		close(kb.releaseFirstCreate)
	}()
	factory := NewReadOnlyAgentToolFactory(readOnlyAgentToolFactoryParams{
		WebSearchService: webSearch, KnowledgeBaseService: kb,
	})
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, factory)
	tenant := &types.Tenant{ID: request.TenantID, WebSearchConfig: &types.WebSearchConfig{
		CompressionMethod: "rag", EmbeddingModelID: "embedding-1",
	}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, request.TenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	result, err := advisor.Analyze(ctx, request, interfaces.IngestionAdvisorRuntime{
		WebSearchKnowledge: knowledge,
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.SelectedCandidateID)
	require.Equal(t, 2, webSearch.compressCalls)
	require.Equal(t, 1, kb.created)
	require.Equal(t, []string{"temp-doc-1", "temp-doc-2"}, knowledge.deleted)
	require.Equal(t, []string{"temp-kb-1"}, kb.deleted)
	for _, warning := range result.AgentRun.Warnings {
		require.False(t, warning.Tool == agenttools.ToolWebSearch, warning.Message)
	}
}

func TestBuildIngestionAgentRunRedactsPayloadsAndWarnsOnOptionalToolFailure(t *testing.T) {
	state := &types.AgentState{RoundSteps: []types.AgentStep{{
		Iteration: 0, Thought: "private chain of thought", ReasoningContent: "private reasoning content",
		ToolCalls: []types.ToolCall{{
			Name: agenttools.ToolWebSearch,
			Args: map[string]interface{}{"query": "raw document excerpt"},
			Result: &types.ToolResult{
				Success: false, Output: "complete external output", Error: "service unavailable",
			},
		}},
	}}}

	run := buildIngestionAgentRun(newIngestionAgentRun(nil, nil), state)
	persistable, err := json.Marshal(run)

	require.NoError(t, err)
	require.Contains(t, run.Warnings, types.IngestionAgentWarning{
		Code: "optional_tool_failed", Tool: agenttools.ToolWebSearch,
		Message: "可选只读工具执行失败",
	})
	require.NotContains(t, string(persistable), "private chain of thought")
	require.NotContains(t, string(persistable), "private reasoning content")
	require.NotContains(t, string(persistable), "raw document excerpt")
	require.NotContains(t, string(persistable), "complete external output")
}

func TestBuildIngestionAgentRunPersistsSafeFailureMetadata(t *testing.T) {
	state := &types.AgentState{RoundSteps: []types.AgentStep{{
		Iteration: 0,
		ToolCalls: []types.ToolCall{{
			Name: previewIngestionChunkingTool,
			Result: &types.ToolResult{
				Success: false, Error: "private failure details",
				Failure: &types.ToolFailure{
					Code: ingestionFailureOverlapInvalid, Field: "chunk_overlap",
					Constraint: "at_most_half_chunk_size",
				},
			},
		}},
	}}}

	run := buildIngestionAgentRun(newIngestionAgentRun(nil, nil), state)
	persisted, err := json.Marshal(run)

	require.NoError(t, err)
	require.Equal(t, ingestionFailureOverlapInvalid, run.Steps[0].FailureCode)
	require.Equal(t, "chunk_overlap", run.Steps[0].FailureField)
	require.Equal(t, "at_most_half_chunk_size", run.Steps[0].FailureConstraint)
	require.NotContains(t, string(persisted), "private failure details")
}

func TestBuildIngestionAgentRunRedactsUntrustedFailureMetadata(t *testing.T) {
	state := &types.AgentState{RoundSteps: []types.AgentStep{{
		Iteration: 0,
		ToolCalls: []types.ToolCall{{
			Name: previewIngestionChunkingTool,
			Result: &types.ToolResult{Success: false, Failure: &types.ToolFailure{
				Code: "raw-model-code", Field: "private-document-fragment",
				Constraint: "raw-model-constraint",
			}},
		}},
	}}}

	run := buildIngestionAgentRun(newIngestionAgentRun(nil, nil), state)
	persisted, err := json.Marshal(run)

	require.NoError(t, err)
	require.Equal(t, ingestionFailureTool, run.Steps[0].FailureCode)
	require.Empty(t, run.Steps[0].FailureField)
	require.Empty(t, run.Steps[0].FailureConstraint)
	require.NotContains(t, string(persisted), "raw-model")
	require.NotContains(t, string(persisted), "private-document")
}

func TestIngestionProgressReceiverCarriesCallIDAndSanitizesFailure(t *testing.T) {
	var received types.IngestionAgentStep
	receiver := ingestionProgressReceiver(func(step types.IngestionAgentStep) {
		received = step
	})

	receiver(interfaces.AgentTaskEvent{
		Kind: "tool_finished", ToolCallID: "preview-1", Round: 2,
		ToolName: previewIngestionChunkingTool, Status: "failed",
		Failure: &types.ToolFailure{
			Code: "raw-model-code", Field: "private-document-fragment",
			Constraint: "raw-model-constraint",
		},
	})

	require.Equal(t, "preview-1", received.ToolCallID)
	require.Equal(t, ingestionFailureTool, received.FailureCode)
	require.Empty(t, received.FailureField)
	require.Empty(t, received.FailureConstraint)
}

func TestIngestionSelectionAllowsEvidenceBackedNearScoreCandidate(t *testing.T) {
	candidates := []types.IngestionChunkingCandidate{
		{ID: "highest", Config: ingestionTestConfig(300), HardValid: true,
			Score: types.IngestionCandidateScore{Total: 90, BoundaryQuality: 10}},
		{ID: "boundary", Config: ingestionTestConfig(320), HardValid: true,
			Score: types.IngestionCandidateScore{Total: 86, BoundaryQuality: 15}},
		{ID: "far", Config: ingestionTestConfig(340), HardValid: true,
			Score: types.IngestionCandidateScore{Total: 80, BoundaryQuality: 20}},
	}
	attachIngestionComparisonFacts(candidates, []string{"boundary_quality"})
	session := newFallbackTestSession(candidates...)

	analysis, err := session.submit(validIngestionDecisionInput("boundary"))

	require.NoError(t, err)
	require.Equal(t, "boundary", session.selectedCandidateID())
	require.Equal(t, analysis.ReasonCodes, []string{"valid_candidate"})
	require.Equal(t, []string{"evidence_boundary_quality_advantage"}, session.selectionReasons())
	require.False(t, candidates[2].ComparisonFacts.SelectionEligible)
	require.Equal(t, []string{"total_score_gap_exceeded"}, candidates[2].ComparisonFacts.ReasonCodes)
	rejected := newFallbackTestSession(candidates...)
	_, err = rejected.submit(validIngestionDecisionInput("far"))
	require.ErrorContains(t, err, "不满足后端选择约束")
}

func TestModelIngestionAdvisorRejectsNaturalAnswerWithoutTools(t *testing.T) {
	model := &ingestionAdvisorScriptedModel{responses: [][]types.StreamResponse{naturalResponse("plain answer")}}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	result, err := advisor.Analyze(
		context.Background(), validIngestionAdvisorRequest(), interfaces.IngestionAdvisorRuntime{},
	)

	require.ErrorContains(t, err, "不支持原生工具调用")
	require.NotNil(t, result)
	require.Nil(t, result.Analysis)
	require.Equal(t, ingestionAdvisorErrorToolCalling, ingestionAdvisorRunErrorCode(err))
}

func TestModelIngestionAdvisorClassifiesFailedCoreTools(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     string
		code     string
	}{
		{name: "unknown candidate", toolName: submitIngestionDecisionTool,
			args: `{"candidate_id":"cand_unknown","document_kind":"policy_manual","confidence":0.8,"recommended_content_mode":"document","reason_codes":["unknown"],"summary":"unknown"}`,
			code: ingestionAdvisorErrorCandidate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := &ingestionAdvisorScriptedModel{responses: [][]types.StreamResponse{
				toolResponse("tool-1", test.toolName, test.args), naturalResponse("stop without decision"),
			}}
			advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

			result, err := advisor.Analyze(
				context.Background(), validIngestionAdvisorRequest(), interfaces.IngestionAdvisorRuntime{},
			)

			require.Error(t, err)
			require.Equal(t, test.code, ingestionAdvisorRunErrorCode(err))
			require.Contains(t, err.Error(), "错误码")
			require.NotContains(t, err.Error(), "cand_unknown")
			require.NotNil(t, result)
			require.Nil(t, result.Analysis)
		})
	}
}

func TestModelIngestionAdvisorFailsAtMaxRoundsWithoutSubmission(t *testing.T) {
	steps := make([]types.AgentStep, ingestionAdvisorMaxRounds)
	for index := range steps {
		steps[index] = types.AgentStep{Iteration: index, ToolCalls: []types.ToolCall{{
			Name: agenttools.ToolWebSearch, Result: &types.ToolResult{Success: true},
		}}}
	}
	session := newTestIngestionAgentSession(ingestionTestContent())
	err := validateIngestionAgentOutcome(&types.AgentState{
		StopReason: "max_iterations", RoundSteps: steps,
	}, session)

	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorMaxRounds, ingestionAdvisorRunErrorCode(err))
}

func TestValidateIngestionAgentOutcomeRejectsOptionalToolWithoutSubmission(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	err := validateIngestionAgentOutcome(&types.AgentState{
		StopReason: "completed", RoundSteps: []types.AgentStep{{ToolCalls: []types.ToolCall{{
			Name: agenttools.ToolWebSearch, Result: &types.ToolResult{Success: true},
		}}}},
	}, session)

	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorNotSubmitted, ingestionAdvisorRunErrorCode(err))
}

func TestModelIngestionAdvisorClassifiesProviderToolCallingFailure(t *testing.T) {
	model := &ingestionAdvisorScriptedModel{streamErr: chat.NewProviderError(
		chat.ProviderFailureRequestInvalid, 400, "tools",
	)}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	result, err := advisor.Analyze(
		context.Background(), validIngestionAdvisorRequest(), interfaces.IngestionAdvisorRuntime{},
	)

	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorToolCalling, ingestionAdvisorRunErrorCode(err))
	require.NotNil(t, result)
}

func TestModelIngestionAdvisorDoesNotClassifyUnstructuredProviderMessage(t *testing.T) {
	model := &ingestionAdvisorScriptedModel{streamErr: errors.New("tool calling unsupported by provider")}
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{model: model}, nil)

	_, err := advisor.Analyze(
		context.Background(), validIngestionAdvisorRequest(), interfaces.IngestionAdvisorRuntime{},
	)

	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorExecution, ingestionAdvisorRunErrorCode(err))
	require.NotContains(t, err.Error(), "tool calling unsupported by provider")
}

func TestModelIngestionAdvisorSurfacesMissingModelAndProviderErrors(t *testing.T) {
	advisor := NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{}, nil)
	request := validIngestionAdvisorRequest()
	request.ModelID = ""
	_, err := advisor.Analyze(context.Background(), request, interfaces.IngestionAdvisorRuntime{})
	require.ErrorContains(t, err, "未配置摘要模型")

	providerErr := errors.New("provider unavailable")
	advisor = NewIngestionAdvisor(&ingestionAdvisorModelServiceStub{err: providerErr}, nil)
	_, err = advisor.Analyze(
		context.Background(), validIngestionAdvisorRequest(), interfaces.IngestionAdvisorRuntime{},
	)
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
		{name: "applied mode", mutate: func(v *types.IngestionAnalysis) { v.AppliedMode = "automatic" }},
		{name: "fallback reasons missing", mutate: func(v *types.IngestionAnalysis) {
			v.AppliedMode = types.IngestionAppliedModeFallback
		}},
		{name: "smart fallback reasons", mutate: func(v *types.IngestionAnalysis) {
			v.AppliedMode = types.IngestionAppliedModeSmart
			v.FallbackReasonCodes = []string{"not_allowed"}
		}},
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

func TestConstrainedValidationAcceptsOnlyTokenNormalizedSmallChunk(t *testing.T) {
	constraints := types.IngestionChunkingConstraints{
		TokenLimit: 1,
		Languages:  []string{chunker.LangEnglish},
	}
	analysis := validReactIngestionAnalysis()
	normalized, err := normalizeIngestionPreviewConfig(
		analysis.RecommendedChunking, constraints,
	)
	require.NoError(t, err)
	require.Less(t, normalized.ChunkSize, 100)
	analysis.RecommendedChunking = normalized

	require.NoError(t, validateIngestionAnalysisWithConstraints(analysis, constraints))
	require.Error(t, ValidateIngestionAnalysis(analysis))
	analysis.RecommendedChunking.ChunkSize--
	require.Error(t, validateIngestionAnalysisWithConstraints(analysis, constraints))
}

func TestValidateIngestionAdvisorResultRequiresPreviewedHardValidSelection(t *testing.T) {
	newResult := func() *types.IngestionAdvisorResult {
		analysis := validReactIngestionAnalysis()
		candidate := types.IngestionChunkingCandidate{
			ID: "cand_valid", Config: cloneChunkingRecommendation(analysis.RecommendedChunking),
			HardValid: true,
		}
		return &types.IngestionAdvisorResult{
			Analysis: analysis, Candidates: []types.IngestionChunkingCandidate{candidate},
			SelectedCandidateID: "cand_valid", SelectionReasonCodes: []string{"previewed"},
		}
	}

	require.NoError(t, ValidateIngestionAdvisorResult(newResult()))

	unknown := newResult()
	unknown.SelectedCandidateID = "cand_unknown"
	require.ErrorContains(t, ValidateIngestionAdvisorResult(unknown), "不存在")

	invalid := newResult()
	invalid.Candidates[0].HardValid = false
	require.ErrorContains(t, ValidateIngestionAdvisorResult(invalid), "未通过硬校验")

	mismatched := newResult()
	mismatched.Candidates[0].Config.ChunkSize++
	require.ErrorContains(t, ValidateIngestionAdvisorResult(mismatched), "不一致")

	missingReasons := newResult()
	missingReasons.SelectionReasonCodes = nil
	require.ErrorContains(t, ValidateIngestionAdvisorResult(missingReasons), "selection_reason_codes")
}

func TestValidateIngestionAdvisorResultRequiresExactFallbackEvidence(t *testing.T) {
	candidates := []types.IngestionChunkingCandidate{
		ingestionInvalidCandidate("cand_1", "atomic_block_split"),
		ingestionInvalidCandidate("cand_2", "source_coverage_gap"),
		ingestionInvalidCandidate("cand_3", "token_limit_exceeded"),
	}
	reasons := ingestionFallbackReasonCodes(candidates)
	analysis := validReactIngestionAnalysis()
	analysis.AppliedMode = types.IngestionAppliedModeFallback
	analysis.FallbackReasonCodes = append([]string(nil), reasons...)
	result := &types.IngestionAdvisorResult{
		Analysis: analysis, Candidates: candidates,
		SelectionReasonCodes: append([]string(nil), reasons...),
	}

	require.NoError(t, ValidateIngestionAdvisorResult(result))

	result.Candidates[0].HardValid = true
	require.ErrorContains(t, ValidateIngestionAdvisorResult(result), "有效候选")
	result.Candidates[0].HardValid = false
	result.SelectionReasonCodes = []string{"incomplete"}
	require.ErrorContains(t, ValidateIngestionAdvisorResult(result), "回退原因")
}

func TestValidateIngestionAdvisorConfigModes(t *testing.T) {
	require.NoError(t, ValidateIngestionAdvisorConfig(nil))
	require.NoError(t, ValidateIngestionAdvisorConfig(&types.IngestionAdvisorConfig{Mode: types.IngestionAdvisorModeSmart}))
	require.NoError(t, ValidateIngestionAdvisorConfig(&types.IngestionAdvisorConfig{Mode: types.IngestionAdvisorModeOff}))
	require.Error(t, ValidateIngestionAdvisorConfig(&types.IngestionAdvisorConfig{Mode: "automatic"}))
}

func jsonMarshalForTest(value any) (string, error) {
	payload, err := json.Marshal(value)
	return string(payload), err
}
