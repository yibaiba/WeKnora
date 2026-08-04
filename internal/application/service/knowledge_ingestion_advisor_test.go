package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type ingestionAdvisorStub struct {
	responses []*types.IngestionAnalysis
	errors    []error
	requests  []types.IngestionAdvisorRequest
}

func (s *ingestionAdvisorStub) Analyze(
	_ context.Context,
	request types.IngestionAdvisorRequest,
) (*types.IngestionAdvisorResult, error) {
	call := len(s.requests)
	s.requests = append(s.requests, request)
	if call < len(s.errors) && s.errors[call] != nil {
		return nil, s.errors[call]
	}
	if call >= len(s.responses) {
		return nil, errors.New("no stub response")
	}
	if request.ProgressFn != nil {
		emitIngestionProgressForTest(request.ProgressFn)
	}
	return ingestionAdvisorResultForTest(s.responses[call]), nil
}

func emitIngestionProgressForTest(progress func(types.IngestionAgentStep)) {
	for _, toolName := range []string{
		agenttools.ToolKnowledgeSearch, previewIngestionChunkingTool, submitIngestionDecisionTool,
	} {
		progress(types.IngestionAgentStep{Round: 1, ToolName: toolName, Status: "running"})
		progress(types.IngestionAgentStep{
			Round: 1, ToolName: toolName, Status: "succeeded", DurationMS: 5,
		})
	}
}

type ingestionKnowledgeRepoStub struct {
	interfaces.KnowledgeRepository
	updateCalls       int
	updateColumnCalls int
}

func (s *ingestionKnowledgeRepoStub) UpdateKnowledge(context.Context, *types.Knowledge) error {
	s.updateCalls++
	return nil
}

func (s *ingestionKnowledgeRepoStub) UpdateKnowledgeColumn(
	context.Context,
	string,
	string,
	interface{},
) error {
	s.updateColumnCalls++
	return nil
}

type ingestionSpanTrackerStub struct {
	noopSpanTracker
	spans     map[string]*Span
	ended     []string
	failed    []string
	failCodes []string
	skipped   []string
	subspans  []string
	subEnded  []string
}

func newIngestionSpanTrackerStub() *ingestionSpanTrackerStub {
	return &ingestionSpanTrackerStub{spans: make(map[string]*Span)}
}

func (s *ingestionSpanTrackerStub) BeginStage(
	_ context.Context,
	knowledgeID string,
	attempt int,
	stage string,
	_ types.JSONMap,
) *Span {
	span := &Span{KnowledgeID: knowledgeID, Attempt: attempt, SpanID: stage, Name: stage, Kind: types.SpanKindStage}
	s.spans[stage] = span
	return span
}

func (s *ingestionSpanTrackerStub) LookupStage(_ context.Context, _ string, _ int, stage string) *Span {
	return s.spans[stage]
}

func (s *ingestionSpanTrackerStub) BeginSubSpan(
	_ context.Context,
	parent *Span,
	name string,
	kind string,
	_ types.JSONMap,
) *Span {
	s.subspans = append(s.subspans, name)
	return &Span{
		KnowledgeID: parent.KnowledgeID, Attempt: parent.Attempt,
		SpanID: name, ParentSpanID: parent.SpanID, Name: name, Kind: kind,
	}
}

func (s *ingestionSpanTrackerStub) EndSpan(_ context.Context, span *Span, _ types.JSONMap) {
	if span.Kind == types.SpanKindSubSpan {
		s.subEnded = append(s.subEnded, span.Name)
		return
	}
	s.ended = append(s.ended, span.Name)
}

func (s *ingestionSpanTrackerStub) FailSpan(_ context.Context, span *Span, code, _ string, _ error) {
	s.failed = append(s.failed, span.Name)
	s.failCodes = append(s.failCodes, code)
}

func (s *ingestionSpanTrackerStub) SkipSpan(_ context.Context, span *Span, _ string) {
	s.skipped = append(s.skipped, span.Name)
}

func validIngestionAnalysis() *types.IngestionAnalysis {
	return &types.IngestionAnalysis{
		DocumentKind:           types.IngestionDocumentKindPolicyManual,
		Confidence:             0.92,
		RecommendedContentMode: types.IngestionContentModeDocument,
		ReasonCodes:            []string{"heading_rich", "long_sections"},
		Summary:                "层级清晰的制度类文档",
		RecommendedChunking: types.IngestionChunkingRecommendation{
			Strategy: "heading", ChunkSize: 700, ChunkOverlap: 100,
			EnableParentChild: true, ParentChunkSize: 4096, ChildChunkSize: 384,
			Separators: []string{"\n\n", "\n", "。", "！", "？"},
		},
		ModelID:       "untrusted-model",
		PromptVersion: "untrusted-version",
	}
}

func ingestionAdvisorResultForTest(analysis *types.IngestionAnalysis) *types.IngestionAdvisorResult {
	if analysis == nil {
		return &types.IngestionAdvisorResult{}
	}
	candidate := types.IngestionChunkingCandidate{
		ID: "cand_test", Config: cloneChunkingRecommendation(analysis.RecommendedChunking),
		HardValid: true, Score: types.IngestionCandidateScore{Total: 88},
	}
	return &types.IngestionAdvisorResult{
		Analysis: analysis, Candidates: []types.IngestionChunkingCandidate{candidate},
		SelectedCandidateID:  "cand_test",
		SelectionReasonCodes: append([]string(nil), analysis.ReasonCodes...),
		AgentRun: types.IngestionAgentRun{
			MaxRounds: 4, ActualRounds: 2,
			AvailableTools: []string{
				inspectIngestionDocumentTool, previewIngestionChunkingTool, submitIngestionDecisionTool,
			},
			Warnings: []types.IngestionAgentWarning{{
				Code: "optional_tool_failed", Tool: agenttools.ToolWebSearch,
				Message: "可选只读工具执行失败",
			}},
			Steps: []types.IngestionAgentStep{{
				Round: 1, ToolName: previewIngestionChunkingTool, Status: "succeeded",
				DurationMS: 5, CandidateID: "cand_test", Score: 88,
			}},
			StopReason: "termination_tool",
		},
	}
}

func newSmartIngestionKnowledge(t *testing.T, id string) *types.Knowledge {
	t.Helper()
	knowledge := &types.Knowledge{ID: id, Type: "file", ParseStatus: types.ParseStatusProcessing}
	require.NoError(t, knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
		IngestionAdvisor: &types.IngestionAdvisorConfig{Mode: types.IngestionAdvisorModeSmart},
	}))
	return knowledge
}

func smartIngestionRun(knowledge *types.Knowledge) ingestionAdvisorRun {
	return ingestionAdvisorRun{
		Knowledge: knowledge,
		KB: &types.KnowledgeBase{
			ID: "kb-1", Type: types.KnowledgeBaseTypeDocument, SummaryModelID: "summary-1",
		},
		Content: "# Policy\n\nLong section",
		Effective: types.EffectiveProcessConfig{ChunkingConfig: types.ChunkingConfig{
			Strategy: "legacy", ChunkSize: 512, ChunkOverlap: 80,
			Separators: []string{"old"}, TokenLimit: 1024, Languages: []string{"de"},
			TableMetadataInstructions: "preserve me",
			ParserEngineRules:         []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "builtin"}},
		}},
	}
}

func TestApplyIngestionAdvisorPersistsAndOnlyOverridesOwnedChunking(t *testing.T) {
	repo := &ingestionKnowledgeRepoStub{}
	tracker := newIngestionSpanTrackerStub()
	original := validIngestionAnalysis()
	advisor := &ingestionAdvisorStub{responses: []*types.IngestionAnalysis{original}}
	service := &knowledgeService{repo: repo, ingestionAdvisor: advisor, spanTracker: tracker}
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "doc-1"))
	overrides, err := run.Knowledge.ProcessOverrides()
	require.NoError(t, err)
	overrides.IngestionAdvisor.AllowWebAccess = true
	overrides.IngestionAdvisor.AllowReadOnlyMCP = true
	require.NoError(t, run.Knowledge.SetProcessOverrides(overrides))

	effective, err := service.applyIngestionAdvisor(withAttempt(context.Background(), 1), run)

	require.NoError(t, err)
	require.Equal(t, "heading", effective.ChunkingConfig.Strategy)
	require.Equal(t, 700, effective.ChunkingConfig.ChunkSize)
	require.Equal(t, 1024, effective.ChunkingConfig.TokenLimit)
	require.Equal(t, []string{"de"}, effective.ChunkingConfig.Languages)
	require.Equal(t, "preserve me", effective.ChunkingConfig.TableMetadataInstructions)
	require.Equal(t, "builtin", effective.ChunkingConfig.ParserEngineRules[0].Engine)
	require.Equal(t, []string{"old"}, run.Effective.ChunkingConfig.Separators)
	require.Equal(t, "untrusted-model", original.ModelID, "advisor-owned result must not be mutated")
	require.True(t, advisor.requests[0].AllowWebAccess)
	require.True(t, advisor.requests[0].AllowReadOnlyMCP)

	persisted, err := run.Knowledge.IngestionAnalysis()
	require.NoError(t, err)
	require.Equal(t, "summary-1", persisted.ModelID)
	require.Equal(t, types.IngestionPromptVersionV1, persisted.PromptVersion)
	require.Equal(t, persisted.RecommendedChunking, persisted.AppliedChunking)
	require.Equal(t, "cand_test", persisted.SelectedCandidateID)
	require.Equal(t, []string{"heading_rich", "long_sections"}, persisted.SelectionReasonCodes)
	require.Len(t, persisted.Candidates, 1)
	require.Equal(t, "termination_tool", persisted.AgentRun.StopReason)
	require.Equal(t, "cand_test", persisted.AgentRun.Steps[0].CandidateID)
	require.Equal(t, "optional_tool_failed", persisted.AgentRun.Warnings[0].Code)
	require.Empty(t, original.Candidates, "advisor-owned result must remain immutable")
	persistedJSON, marshalErr := json.Marshal(persisted)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(persistedJSON), "thought")
	require.NotContains(t, string(persistedJSON), "reasoning_content")
	require.NotContains(t, string(persistedJSON), "First section")
	require.NotContains(t, string(persistedJSON), "tool_output")
	require.Equal(t, 1, repo.updateColumnCalls)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, []string{types.StageDocumentAnalysis}, tracker.ended)
	require.Len(t, tracker.subspans, 5)
	require.Len(t, tracker.subEnded, 5)
	require.Contains(t, tracker.subspans[0], "analyze_document")
	require.Contains(t, tracker.subspans[1], "readonly_tools")
	require.Contains(t, tracker.subspans[2], "preview_candidates")
	require.Contains(t, tracker.subspans[3], "submit_decision")
	require.Contains(t, tracker.subspans[4], "evaluate_and_refine")
}

func TestApplyIngestionAdvisorPreservesExplicitZeroOverlap(t *testing.T) {
	analysis := validIngestionAnalysis()
	analysis.RecommendedChunking.ChunkOverlap = 0
	service := &knowledgeService{
		repo:             &ingestionKnowledgeRepoStub{},
		ingestionAdvisor: &ingestionAdvisorStub{responses: []*types.IngestionAnalysis{analysis}},
	}
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "zero-overlap"))

	effective, err := service.applyIngestionAdvisor(context.Background(), run)

	require.NoError(t, err)
	require.True(t, effective.IngestionAdvisorApplied)
	require.Zero(t, effective.ChunkingConfig.ChunkOverlap)
	require.Zero(t, buildSplitterConfigFromEffective(effective).ChunkOverlap)
	persisted, parseErr := run.Knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Zero(t, persisted.AppliedChunking.ChunkOverlap)
	require.NotZero(t, buildSplitterConfigFromChunking(effective.ChunkingConfig).ChunkOverlap,
		"legacy zero-value configs must keep their historical default")
}

func TestApplyIngestionAdvisorPersistsTokenNormalizedAppliedValues(t *testing.T) {
	analysis := validIngestionAnalysis()
	analysis.RecommendedChunking.ChunkSize = 4000
	analysis.RecommendedChunking.ChunkOverlap = 500
	service := &knowledgeService{
		repo:             &ingestionKnowledgeRepoStub{},
		ingestionAdvisor: &ingestionAdvisorStub{responses: []*types.IngestionAnalysis{analysis}},
	}
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "token-normalized"))
	run.Effective.ChunkingConfig.TokenLimit = 100
	run.Effective.ChunkingConfig.Languages = []string{"en"}

	effective, err := service.applyIngestionAdvisor(context.Background(), run)

	require.NoError(t, err)
	actualSplitter := buildSplitterConfigFromEffective(effective)
	require.Less(t, actualSplitter.ChunkSize, analysis.RecommendedChunking.ChunkSize)
	persisted, parseErr := run.Knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Equal(t, actualSplitter.ChunkSize, persisted.AppliedChunking.ChunkSize)
	require.Equal(t, actualSplitter.ChunkOverlap, persisted.AppliedChunking.ChunkOverlap)
}

func TestApplyIngestionAdvisorFailureStopsBeforeDownstreamStages(t *testing.T) {
	repo := &ingestionKnowledgeRepoStub{}
	tracker := newIngestionSpanTrackerStub()
	advisorErr := errors.New("model timeout")
	service := &knowledgeService{
		repo: repo, ingestionAdvisor: &ingestionAdvisorStub{errors: []error{advisorErr}}, spanTracker: tracker,
	}
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "doc-1"))

	_, err := service.applyIngestionAdvisor(withAttempt(context.Background(), 1), run)

	require.ErrorIs(t, err, advisorErr)
	require.Equal(t, types.ParseStatusFailed, run.Knowledge.ParseStatus)
	require.Equal(t, []string{types.StageDocumentAnalysis}, tracker.failed)
	require.Equal(t, []string{ingestionAdvisorErrorExecution}, tracker.failCodes)
	require.NotContains(t, tracker.spans, types.StageChunking)
	require.NotContains(t, tracker.spans, types.StageEmbedding)
	analysis, parseErr := run.Knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Nil(t, analysis)
}

func TestApplyIngestionAdvisorClearsStaleAnalysisBeforeFailedRetry(t *testing.T) {
	repo := &ingestionKnowledgeRepoStub{}
	knowledge := newSmartIngestionKnowledge(t, "doc-stale")
	require.NoError(t, knowledge.SetIngestionAnalysis(validIngestionAnalysis()))
	service := &knowledgeService{
		repo: repo,
		ingestionAdvisor: &ingestionAdvisorStub{
			errors: []error{newIngestionAdvisorRunError(
				ingestionAdvisorErrorMaxRounds, "four rounds exhausted",
			)},
		},
	}

	_, err := service.applyIngestionAdvisor(context.Background(), smartIngestionRun(knowledge))

	require.Error(t, err)
	persisted, parseErr := knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Nil(t, persisted)
	require.Equal(t, 1, repo.updateColumnCalls)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
}

func TestApplyIngestionAdvisorSkipsLegacyOffAndUnsupportedSources(t *testing.T) {
	tests := []struct {
		name      string
		knowledge func(*testing.T) *types.Knowledge
	}{
		{name: "missing config", knowledge: func(t *testing.T) *types.Knowledge {
			return &types.Knowledge{ID: "doc", Type: "file"}
		}},
		{name: "off", knowledge: func(t *testing.T) *types.Knowledge {
			knowledge := &types.Knowledge{ID: "doc", Type: "file"}
			require.NoError(t, knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
				IngestionAdvisor: &types.IngestionAdvisorConfig{Mode: types.IngestionAdvisorModeOff},
			}))
			return knowledge
		}},
		{name: "url", knowledge: func(t *testing.T) *types.Knowledge {
			knowledge := newSmartIngestionKnowledge(t, "url-doc")
			knowledge.Type = "url"
			return knowledge
		}},
		{name: "data source", knowledge: func(t *testing.T) *types.Knowledge {
			knowledge := &types.Knowledge{ID: "source-doc", Type: "file", Metadata: types.JSON(`{"datasource_id":"ds-1"}`)}
			require.NoError(t, knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
				IngestionAdvisor: &types.IngestionAdvisorConfig{Mode: types.IngestionAdvisorModeSmart},
			}))
			return knowledge
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			advisor := &ingestionAdvisorStub{}
			tracker := newIngestionSpanTrackerStub()
			service := &knowledgeService{repo: &ingestionKnowledgeRepoStub{}, ingestionAdvisor: advisor, spanTracker: tracker}
			run := smartIngestionRun(test.knowledge(t))

			got, err := service.applyIngestionAdvisor(withAttempt(context.Background(), 1), run)

			require.NoError(t, err)
			require.Equal(t, run.Effective, got)
			require.Empty(t, advisor.requests)
			require.Equal(t, []string{types.StageDocumentAnalysis}, tracker.skipped)
		})
	}
}

func TestApplyIngestionAdvisorRetryRerunsAnalysis(t *testing.T) {
	firstErr := errors.New("temporary model error")
	advisor := &ingestionAdvisorStub{
		errors:    []error{firstErr, nil},
		responses: []*types.IngestionAnalysis{nil, validIngestionAnalysis()},
	}
	service := &knowledgeService{repo: &ingestionKnowledgeRepoStub{}, ingestionAdvisor: advisor}
	knowledge := newSmartIngestionKnowledge(t, "doc-retry")
	run := smartIngestionRun(knowledge)

	_, err := service.applyIngestionAdvisor(context.Background(), run)
	require.ErrorIs(t, err, firstErr)
	knowledge.ParseStatus = types.ParseStatusProcessing
	_, err = service.applyIngestionAdvisor(context.Background(), run)

	require.NoError(t, err)
	require.Len(t, advisor.requests, 2)
	analysis, parseErr := knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.NotNil(t, analysis)
}

func TestApplyIngestionAdvisorKeepsBatchResultsIndependent(t *testing.T) {
	advisor := &ingestionAdvisorStub{
		errors:    []error{errors.New("first document invalid"), nil},
		responses: []*types.IngestionAnalysis{nil, validIngestionAnalysis()},
	}
	service := &knowledgeService{repo: &ingestionKnowledgeRepoStub{}, ingestionAdvisor: advisor}
	first := newSmartIngestionKnowledge(t, "doc-1")
	second := newSmartIngestionKnowledge(t, "doc-2")

	_, firstErr := service.applyIngestionAdvisor(context.Background(), smartIngestionRun(first))
	_, secondErr := service.applyIngestionAdvisor(context.Background(), smartIngestionRun(second))

	require.Error(t, firstErr)
	require.NoError(t, secondErr)
	require.Equal(t, types.ParseStatusFailed, first.ParseStatus)
	firstAnalysis, err := first.IngestionAnalysis()
	require.NoError(t, err)
	require.Nil(t, firstAnalysis)
	secondAnalysis, err := second.IngestionAnalysis()
	require.NoError(t, err)
	require.NotNil(t, secondAnalysis)
}
