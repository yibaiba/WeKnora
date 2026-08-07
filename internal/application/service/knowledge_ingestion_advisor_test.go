package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type ingestionAdvisorStub struct {
	responses        []*types.IngestionAnalysis
	results          []*types.IngestionAdvisorResult
	errors           []error
	requests         []types.IngestionAdvisorRequest
	runtimes         []interfaces.IngestionAdvisorRuntime
	analysisProgress []types.IngestionDocumentAnalysisProgress
}

type ingestionTokenModelServiceStub struct {
	interfaces.ModelService
	model *types.Model
	err   error
}

func (s *ingestionTokenModelServiceStub) GetModelByID(
	context.Context,
	string,
) (*types.Model, error) {
	return s.model, s.err
}

func (s *ingestionAdvisorStub) Analyze(
	_ context.Context,
	request types.IngestionAdvisorRequest,
	runtime interfaces.IngestionAdvisorRuntime,
) (*types.IngestionAdvisorResult, error) {
	call := len(s.requests)
	s.requests = append(s.requests, request)
	s.runtimes = append(s.runtimes, runtime)
	if request.AnalysisProgressFn != nil {
		for _, event := range s.analysisProgress {
			request.AnalysisProgressFn(event)
		}
	}
	if call < len(s.errors) && s.errors[call] != nil {
		return nil, s.errors[call]
	}
	if call < len(s.results) && s.results[call] != nil {
		return s.results[call], nil
	}
	if call >= len(s.responses) {
		return nil, errors.New("no stub response")
	}
	if request.AnalysisProgressFn != nil && len(s.analysisProgress) == 0 {
		emitIngestionAnalysisProgressForTest(request.AnalysisProgressFn)
	}
	if request.ProgressFn != nil {
		emitIngestionProgressForTest(request.ProgressFn)
	}
	return ingestionAdvisorResultForTest(s.responses[call]), nil
}

func emitIngestionAnalysisProgressForTest(progress func(types.IngestionDocumentAnalysisProgress)) {
	progress(withIngestionAnalysisBudgetForTest(types.IngestionDocumentAnalysisProgress{
		Phase: "map_document", Status: ingestionAnalysisProgressRunning,
		UnitCount: 2, CoveredCharacters: 22,
	}))
	progress(withIngestionAnalysisBudgetForTest(types.IngestionDocumentAnalysisProgress{
		Phase: "map_document", Status: ingestionAnalysisProgressSucceeded, UnitCount: 2, Completed: 2,
		DurationMS: 7, CoveredCharacters: 22, RetryCount: 1,
	}))
	progress(withIngestionAnalysisBudgetForTest(types.IngestionDocumentAnalysisProgress{
		Phase: "reduce_document", Status: ingestionAnalysisProgressRunning,
		UnitCount: 1, Level: 1, CoveredCharacters: 22,
	}))
	progress(withIngestionAnalysisBudgetForTest(types.IngestionDocumentAnalysisProgress{
		Phase: "reduce_document", Status: ingestionAnalysisProgressSucceeded,
		UnitCount: 1, Completed: 1, Level: 1,
		DurationMS: 3, CoveredCharacters: 22,
	}))
}

func withIngestionAnalysisBudgetForTest(
	event types.IngestionDocumentAnalysisProgress,
) types.IngestionDocumentAnalysisProgress {
	event.ContextWindowTokens = 8192
	event.CompletionTokenBudget = 1024
	event.PromptSchemaTokens = 300
	event.SafetyTokens = 820
	event.ContentTokenBudget = 6048
	event.EstimatedSourceTokens = 7
	return event
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

func TestIngestionAgentSpanPersistsSafeToolFailure(t *testing.T) {
	tracker := newIngestionSpanTrackerStub()
	parent := &Span{
		KnowledgeID: "knowledge-1", Attempt: 1, SpanID: "analysis-1",
		Name: types.StageDocumentAnalysis, Kind: types.SpanKindStage,
	}
	progress := newIngestionAgentSpanProgress(context.Background(), tracker, parent)
	progress.Handle(types.IngestionAgentStep{
		Round: 1, ToolName: previewIngestionChunkingTool, Status: "running",
	})
	progress.Handle(types.IngestionAgentStep{
		Round: 1, ToolName: previewIngestionChunkingTool, Status: "failed",
		FailureCode: ingestionFailureOverlapInvalid, FailureField: "chunk_overlap",
		FailureConstraint: "at_most_half_chunk_size",
	})

	require.Equal(t, []string{ingestionFailureOverlapInvalid}, tracker.failCodes)
	require.Contains(t, tracker.failMessages[0], "chunk_overlap")
	require.Contains(t, tracker.failMessages[0], "at_most_half_chunk_size")
	require.NotContains(t, tracker.failMessages[0], "private document")
	require.Equal(t, []string{""}, tracker.failErrors)
}

func TestIngestionProgressMatchesParallelSameNameCallsByID(t *testing.T) {
	tracker := newIngestionSpanTrackerStub()
	parent := &Span{KnowledgeID: "knowledge-1", Attempt: 1, SpanID: "analysis-1", Kind: types.SpanKindStage}
	progress := newIngestionAgentSpanProgress(context.Background(), tracker, parent)

	progress.Handle(types.IngestionAgentStep{
		ToolCallID: "preview-first", Round: 1, ToolName: previewIngestionChunkingTool, Status: "running",
	})
	progress.Handle(types.IngestionAgentStep{
		ToolCallID: "preview-second", Round: 1, ToolName: previewIngestionChunkingTool, Status: "running",
	})
	progress.Handle(types.IngestionAgentStep{
		ToolCallID: "preview-second", Round: 1, ToolName: previewIngestionChunkingTool, Status: "failed",
		FailureCode: ingestionFailureCandidatePreview,
	})
	progress.Handle(types.IngestionAgentStep{
		ToolCallID: "preview-first", Round: 1, ToolName: previewIngestionChunkingTool, Status: "succeeded",
	})

	require.Equal(t, []string{tracker.subspans[1]}, tracker.failed)
	require.Equal(t, []string{tracker.subspans[0]}, tracker.subEnded)
}

func TestIngestionProgressRedactsUntrustedFailureMetadata(t *testing.T) {
	tracker := newIngestionSpanTrackerStub()
	parent := &Span{KnowledgeID: "knowledge-1", Attempt: 1, SpanID: "analysis-1", Kind: types.SpanKindStage}
	progress := newIngestionAgentSpanProgress(context.Background(), tracker, parent)
	progress.Handle(types.IngestionAgentStep{
		ToolCallID: "preview-1", Round: 1, ToolName: previewIngestionChunkingTool, Status: "running",
	})
	progress.Handle(types.IngestionAgentStep{
		ToolCallID: "preview-1", Round: 1, ToolName: previewIngestionChunkingTool, Status: "failed",
		FailureCode: "raw-model-code", FailureField: "private-document-fragment",
		FailureConstraint: "raw-model-constraint",
	})

	require.Equal(t, []string{ingestionFailureTool}, tracker.failCodes)
	require.NotContains(t, tracker.failMessages[0], "raw-model")
	require.NotContains(t, tracker.failMessages[0], "private-document")
}

func TestIngestionAnalysisSpanClosesOriginalRunningSpan(t *testing.T) {
	tracker := newIngestionSpanTrackerStub()
	parent := &Span{KnowledgeID: "knowledge-1", Attempt: 1, SpanID: "analysis-1", Kind: types.SpanKindStage}
	progress := newIngestionAgentSpanProgress(context.Background(), tracker, parent)
	progress.HandleAnalysis(withIngestionAnalysisBudgetForTest(types.IngestionDocumentAnalysisProgress{
		Phase: "map_document", Status: ingestionAnalysisProgressRunning,
		UnitCount: 2, CoveredCharacters: 15032,
	}))

	require.Len(t, tracker.subspans, 1)
	require.Empty(t, tracker.subEnded)
	progress.HandleAnalysis(withIngestionAnalysisBudgetForTest(types.IngestionDocumentAnalysisProgress{
		Phase: "map_document", Status: ingestionAnalysisProgressSucceeded,
		UnitCount: 2, Completed: 2, DurationMS: 42, CoveredCharacters: 15032, RetryCount: 1,
	}))

	require.Equal(t, tracker.subspans, tracker.subEnded)
	require.Equal(t, types.JSONMap{
		"completed": 2, "duration_ms": int64(42), "covered_characters": 15032,
		"retry_count": 1,
	}, tracker.subOutputs[0])
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
	spans        map[string]*Span
	ended        []string
	failed       []string
	failCodes    []string
	failMessages []string
	failErrors   []string
	skipped      []string
	subspans     []string
	subEnded     []string
	subInputs    []types.JSONMap
	subOutputs   []types.JSONMap
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
	input types.JSONMap,
) *Span {
	s.subspans = append(s.subspans, name)
	s.subInputs = append(s.subInputs, input)
	return &Span{
		KnowledgeID: parent.KnowledgeID, Attempt: parent.Attempt,
		SpanID: name, ParentSpanID: parent.SpanID, Name: name, Kind: kind,
	}
}

func (s *ingestionSpanTrackerStub) EndSpan(_ context.Context, span *Span, output types.JSONMap) {
	if span.Kind == types.SpanKindSubSpan {
		s.subEnded = append(s.subEnded, span.Name)
		s.subOutputs = append(s.subOutputs, output)
		return
	}
	s.ended = append(s.ended, span.Name)
}

func (s *ingestionSpanTrackerStub) FailSpan(_ context.Context, span *Span, code, message string, err error) {
	s.failed = append(s.failed, span.Name)
	s.failCodes = append(s.failCodes, code)
	s.failMessages = append(s.failMessages, message)
	if err == nil {
		s.failErrors = append(s.failErrors, "")
		return
	}
	s.failErrors = append(s.failErrors, err.Error())
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
		ModelID: "untrusted-model",
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
				previewIngestionChunkingTool, submitIngestionDecisionTool,
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

func ingestionAdvisorOnConfig() *appconfig.Config {
	return &appconfig.Config{KnowledgeBase: &appconfig.KnowledgeBaseConfig{
		SemanticChunkingV2Mode: appconfig.SemanticChunkingV2ModeOn,
	}}
}

func TestApplyIngestionAdvisorOffPreservesCurrentPath(t *testing.T) {
	advisor := &ingestionAdvisorStub{}
	service := &knowledgeService{
		config: ingestionRolloutConfig(appconfig.SemanticChunkingV2ModeOff, nil),
		repo:   &ingestionKnowledgeRepoStub{}, ingestionAdvisor: advisor,
	}
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "rollout-off"))

	effective, err := service.applyIngestionAdvisor(context.Background(), run)

	require.NoError(t, err)
	require.Equal(t, run.Effective, effective)
	require.Empty(t, advisor.requests)
	analysis, parseErr := run.Knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Nil(t, analysis)
}

func TestApplyIngestionAdvisorShadowPersistsComparisonWithoutApplyingV2(t *testing.T) {
	one := 1.0
	result := ingestionAdvisorResultForTest(validIngestionAnalysis())
	result.Candidates[0].TokenCountMode = chunker.TokenCountModeExact
	result.Candidates[0].TokenizerID = chunker.TokenizerEncodingO200KBase
	result.Candidates[0].Lengths.Average = 321
	result.Candidates[0].ContextTokenRatio = 0.125
	advisor := &ingestionAdvisorStub{results: []*types.IngestionAdvisorResult{result}}
	service := &knowledgeService{
		config: ingestionRolloutConfig(appconfig.SemanticChunkingV2ModeShadow, &one),
		repo:   &ingestionKnowledgeRepoStub{}, ingestionAdvisor: advisor,
	}
	knowledge := newSmartIngestionKnowledge(t, "shadow-doc")
	knowledge.FileType = "application/pdf"
	knowledge.FileName = "report.pdf"
	run := smartIngestionRun(knowledge)
	run.Document.Diagnostics = types.SemanticDiagnostics{
		HintsProvided: 4, HintsAccepted: 3, HintsRejected: 1,
		ReasonCodes:      []string{"hint_parent_missing"},
		ReasonCodeCounts: map[string]int{"hint_parent_missing": 1},
	}
	original := run.Effective

	effective, err := service.applyIngestionAdvisor(context.Background(), run)

	require.NoError(t, err)
	require.Equal(t, original, effective)
	require.Len(t, advisor.requests, 1)
	persisted, parseErr := knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Equal(t, types.IngestionAppliedModeShadow, persisted.AppliedMode)
	require.Equal(t, ordinaryChunkingRecommendation(original.ChunkingConfig), persisted.AppliedChunking)
	require.Equal(t, ingestionCandidateGeneratorVersion, persisted.CandidateGeneratorVersion)
	require.Equal(t, "pdf", persisted.SemanticDiagnostics.SourceFormat)
	require.Equal(t, 0.75, persisted.SemanticDiagnostics.HintAcceptanceRate)
	require.Equal(t, map[string]int{"hint_parent_missing": 1},
		persisted.SemanticDiagnostics.HintRejectionReasonCounts)
	require.Equal(t, map[string]int{chunker.TokenCountModeExact: 1},
		persisted.SemanticDiagnostics.TokenCountModeCounts)
	require.Equal(t, chunker.TokenizerEncodingO200KBase, persisted.Candidates[0].TokenizerID)
	require.Equal(t, 321.0, persisted.SemanticDiagnostics.SelectedAverageChunkTokens)
	require.Equal(t, 0.125, persisted.SemanticDiagnostics.SelectedContextTokenRatio)
	require.NotNil(t, persisted.ShadowComparison)
	require.Equal(t, types.IngestionAppliedModeSmart, persisted.ShadowComparison.V2AppliedMode)
	require.True(t, persisted.ShadowComparison.ChunkingChanged)
	require.Equal(t, []string{"shadow_v2_not_applied"}, persisted.ShadowComparison.ReasonCodes)
	persistedJSON, marshalErr := json.Marshal(persisted)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(persistedJSON), run.Content)
}

func TestApplyIngestionAdvisorShadowFailureDoesNotDegrade(t *testing.T) {
	one := 1.0
	advisorErr := errors.New("token counter failed")
	service := &knowledgeService{
		config:           ingestionRolloutConfig(appconfig.SemanticChunkingV2ModeShadow, &one),
		repo:             &ingestionKnowledgeRepoStub{},
		ingestionAdvisor: &ingestionAdvisorStub{errors: []error{advisorErr}},
	}
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "shadow-failure"))

	effective, err := service.applyIngestionAdvisor(context.Background(), run)

	require.ErrorIs(t, err, advisorErr)
	require.Equal(t, run.Effective, effective)
	require.Equal(t, types.ParseStatusFailed, run.Knowledge.ParseStatus)
	require.Empty(t, effective.IngestionAppliedMode)
}

func TestApplyIngestionAdvisorPersistsAndOnlyOverridesOwnedChunking(t *testing.T) {
	repo := &ingestionKnowledgeRepoStub{}
	tracker := newIngestionSpanTrackerStub()
	original := validIngestionAnalysis()
	advisor := &ingestionAdvisorStub{responses: []*types.IngestionAnalysis{original}}
	service := &knowledgeService{
		config: ingestionAdvisorOnConfig(), repo: repo,
		ingestionAdvisor: advisor, spanTracker: tracker,
	}
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "doc-1"))
	run.Knowledge.Title = "Production document title"
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
	require.Same(t, service, advisor.runtimes[0].WebSearchKnowledge)
	require.Equal(t, 1024, advisor.requests[0].ChunkingConstraints.TokenLimit)
	require.Equal(t, []string{"de"}, advisor.requests[0].ChunkingConstraints.Languages)
	require.Equal(t, run.Knowledge.Title, advisor.requests[0].ChunkingConstraints.EmbeddingPrefix)
	require.Equal(t, appconfig.DefaultIngestionAdvisorTimeout, advisor.requests[0].Timeout)
	require.Equal(t, types.IngestionAppliedModeSmart, effective.IngestionAppliedMode)
	require.Equal(t, types.IngestionAppliedModeSmart, persistedAppliedMode(t, run.Knowledge))

	persisted, err := run.Knowledge.IngestionAnalysis()
	require.NoError(t, err)
	require.Equal(t, "summary-1", persisted.ModelID)
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
	require.Len(t, tracker.subspans, 7)
	require.Len(t, tracker.subEnded, 7)
	require.Contains(t, tracker.subspans[0], "analyze_document")
	require.Contains(t, tracker.subspans[1], "map_document")
	require.Contains(t, tracker.subspans[2], "reduce_document")
	require.Contains(t, tracker.subspans[3], "readonly_tools")
	require.Contains(t, tracker.subspans[4], "preview_candidates")
	require.Contains(t, tracker.subspans[5], "submit_decision")
	require.Contains(t, tracker.subspans[6], "evaluate_and_refine")
	require.Equal(t, types.JSONMap{
		"unit_count": 2, "level": 0, "covered_characters": 22,
		"context_window_tokens": 8192, "completion_token_budget": 1024,
		"prompt_schema_tokens": 300, "safety_tokens": 820,
		"content_token_budget": 6048, "estimated_source_tokens": 7,
	}, tracker.subInputs[1])
	require.Equal(t, types.JSONMap{
		"unit_count": 1, "level": 1, "covered_characters": 22,
		"context_window_tokens": 8192, "completion_token_budget": 1024,
		"prompt_schema_tokens": 300, "safety_tokens": 820,
		"content_token_budget": 6048, "estimated_source_tokens": 7,
	}, tracker.subInputs[2])
	require.Equal(t, types.JSONMap{
		"completed": 2, "duration_ms": int64(7), "covered_characters": 22,
		"retry_count": 1,
	}, tracker.subOutputs[1])
	require.Equal(t, types.JSONMap{
		"completed": 1, "duration_ms": int64(3), "covered_characters": 22,
		"retry_count": 0,
	}, tracker.subOutputs[2])
	progressJSON, marshalProgressErr := json.Marshal([]types.JSONMap{
		tracker.subInputs[1], tracker.subInputs[2], tracker.subOutputs[1], tracker.subOutputs[2],
	})
	require.NoError(t, marshalProgressErr)
	require.NotContains(t, string(progressJSON), run.Content)
	require.NotContains(t, string(progressJSON), "aggregated_evidence")
}

func TestResolveIngestionTokenCounterUsesEmbeddingModelConfiguration(t *testing.T) {
	service := &knowledgeService{modelService: &ingestionTokenModelServiceStub{model: &types.Model{
		Name: "custom-model",
		Parameters: types.ModelParameters{EmbeddingParameters: types.EmbeddingParameters{
			TokenizerEncoding: types.TokenizerEncodingO200KBase,
		}},
	}}}

	counter, err := service.resolveIngestionTokenCounter(context.Background(), &types.KnowledgeBase{
		EmbeddingModelID: "embedding-1",
	})
	require.NoError(t, err)
	count, err := counter.Count("hello 世界")
	require.NoError(t, err)
	require.Equal(t, chunker.TokenCountModeExact, count.Mode)
	require.Equal(t, chunker.TokenizerEncodingO200KBase, count.TokenizerID)
}

func TestApplyIngestionAdvisorFallbackPreservesOriginalChunking(t *testing.T) {
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "fallback-doc"))
	run.Effective.ChunkingConfig = types.ChunkingConfig{
		Strategy: chunker.StrategyLegacy, ChunkSize: 640, ChunkOverlap: 64,
		Separators: []string{"\n\n", "\n"}, TokenLimit: 1024, Languages: []string{"zh"},
	}
	document, err := chunker.AnalyzeSemanticDocument(run.Content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)
	run.Document = document
	fallback := ordinaryChunkingRecommendation(run.Effective.ChunkingConfig)
	advisor := &ingestionAdvisorStub{results: []*types.IngestionAdvisorResult{
		fallbackAdvisorResultForTest(fallback),
	}}
	service := &knowledgeService{
		config: ingestionAdvisorOnConfig(),
		repo:   &ingestionKnowledgeRepoStub{}, ingestionAdvisor: advisor,
	}
	original := run.Effective.ChunkingConfig

	effective, err := service.applyIngestionAdvisor(context.Background(), run)

	require.NoError(t, err)
	require.Equal(t, original, effective.ChunkingConfig)
	require.False(t, effective.IngestionAdvisorApplied)
	require.Equal(t, types.IngestionAppliedModeFallback, effective.IngestionAppliedMode)
	require.Equal(t, fallback, advisor.requests[0].FallbackChunking)
	require.NotNil(t, advisor.requests[0].SemanticDocument)
	require.Equal(t, document, *advisor.requests[0].SemanticDocument)
	persisted, parseErr := run.Knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Equal(t, types.IngestionAppliedModeFallback, persisted.AppliedMode)
	require.Equal(t, fallback, persisted.RecommendedChunking)
	require.Equal(t, fallback, persisted.AppliedChunking)
	require.Equal(t, []string{
		"all_candidates_structurally_invalid", "atomic_block_split", "source_coverage_gap",
	}, persisted.FallbackReasonCodes)
	require.True(t, persisted.SemanticDiagnostics.FallbackApplied)
}

func fallbackAdvisorResultForTest(
	fallback types.IngestionChunkingRecommendation,
) *types.IngestionAdvisorResult {
	candidates := []types.IngestionChunkingCandidate{
		{ID: "cand_1", Violations: []string{"atomic_block_split"}},
		{ID: "cand_2", Violations: []string{"source_coverage_gap"}},
		{ID: "cand_3", Violations: []string{"atomic_block_split", "source_coverage_gap"}},
	}
	reasons := ingestionFallbackReasonCodes(candidates)
	analysis := validIngestionAnalysis()
	analysis.AppliedMode = types.IngestionAppliedModeFallback
	analysis.FallbackReasonCodes = append([]string(nil), reasons...)
	analysis.RecommendedChunking = cloneChunkingRecommendation(fallback)
	return &types.IngestionAdvisorResult{
		Analysis: analysis, Candidates: candidates,
		SelectionReasonCodes: append([]string(nil), reasons...),
	}
}

func persistedAppliedMode(t *testing.T, knowledge *types.Knowledge) string {
	t.Helper()
	analysis, err := knowledge.IngestionAnalysis()
	require.NoError(t, err)
	return analysis.AppliedMode
}

func TestApplyIngestionAdvisorUsesConfiguredTotalTimeout(t *testing.T) {
	advisor := &ingestionAdvisorStub{responses: []*types.IngestionAnalysis{validIngestionAnalysis()}}
	service := &knowledgeService{
		config: &appconfig.Config{KnowledgeBase: &appconfig.KnowledgeBaseConfig{
			IngestionAdvisorTimeout: 12 * time.Minute,
			SemanticChunkingV2Mode:  appconfig.SemanticChunkingV2ModeOn,
		}},
		repo: &ingestionKnowledgeRepoStub{}, ingestionAdvisor: advisor,
	}

	_, err := service.applyIngestionAdvisor(
		context.Background(), smartIngestionRun(newSmartIngestionKnowledge(t, "configured-timeout")),
	)

	require.NoError(t, err)
	require.Equal(t, 12*time.Minute, advisor.requests[0].Timeout)
}

func TestApplyIngestionAdvisorPreservesExplicitZeroOverlap(t *testing.T) {
	analysis := validIngestionAnalysis()
	analysis.RecommendedChunking.ChunkOverlap = 0
	service := &knowledgeService{
		config:           ingestionAdvisorOnConfig(),
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
	constraints := types.IngestionChunkingConstraints{
		TokenLimit: 100,
		Languages:  []string{"en"},
	}
	normalized, normalizeErr := normalizeIngestionPreviewConfig(
		analysis.RecommendedChunking, constraints,
	)
	require.NoError(t, normalizeErr)
	analysis.RecommendedChunking = normalized
	service := &knowledgeService{
		config:           ingestionAdvisorOnConfig(),
		repo:             &ingestionKnowledgeRepoStub{},
		ingestionAdvisor: &ingestionAdvisorStub{responses: []*types.IngestionAnalysis{analysis}},
	}
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "token-normalized"))
	run.Effective.ChunkingConfig.TokenLimit = 100
	run.Effective.ChunkingConfig.Languages = []string{"en"}

	effective, err := service.applyIngestionAdvisor(context.Background(), run)

	require.NoError(t, err)
	actualSplitter := buildSplitterConfigFromEffective(effective)
	require.Less(t, actualSplitter.ChunkSize, 4000)
	persisted, parseErr := run.Knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Equal(t, actualSplitter.ChunkSize, persisted.AppliedChunking.ChunkSize)
	require.Equal(t, actualSplitter.ChunkOverlap, persisted.AppliedChunking.ChunkOverlap)
	require.Equal(t, persisted.RecommendedChunking, persisted.AppliedChunking)
}

func TestApplyIngestionAdvisorFailureStopsBeforeDownstreamStages(t *testing.T) {
	repo := &ingestionKnowledgeRepoStub{}
	tracker := newIngestionSpanTrackerStub()
	advisorErr := newIngestionAdvisorRunError(
		ingestionAdvisorErrorDocumentAnalysis, "文档全文 Map 分析失败：调用超时或已取消",
	)
	service := &knowledgeService{
		config: ingestionAdvisorOnConfig(),
		repo:   repo,
		ingestionAdvisor: &ingestionAdvisorStub{
			errors: []error{advisorErr},
			analysisProgress: []types.IngestionDocumentAnalysisProgress{withIngestionAnalysisBudgetForTest(types.IngestionDocumentAnalysisProgress{
				Phase: "map_document", Status: ingestionAnalysisProgressRunning,
				UnitCount: 2, CoveredCharacters: 15032,
			}), withIngestionAnalysisBudgetForTest(types.IngestionDocumentAnalysisProgress{
				Phase: "map_document", Status: ingestionAnalysisProgressFailed,
				UnitCount: 2, Completed: 1,
				DurationMS: 9, CoveredCharacters: 8000, Failed: true,
				RetryCount: 2, FailedUnitAttempts: 3,
				FailureKind: ingestionAnalysisFailureStrictSchema, FailedUnit: 2,
				ProviderFailureKind: string(chat.ProviderFailureRequestInvalid),
				HTTPStatus:          400, FailureParameter: "response_format",
			})},
		},
		spanTracker: tracker,
	}
	run := smartIngestionRun(newSmartIngestionKnowledge(t, "doc-1"))

	_, err := service.applyIngestionAdvisor(withAttempt(context.Background(), 1), run)

	require.ErrorIs(t, err, advisorErr)
	require.Equal(t, types.ParseStatusFailed, run.Knowledge.ParseStatus)
	require.Len(t, tracker.failed, 2)
	require.Equal(t, []string{
		ingestionAdvisorErrorDocumentAnalysis, ingestionAdvisorErrorDocumentAnalysis,
	}, tracker.failCodes)
	require.Contains(t, tracker.failed[0], "map_document")
	require.Contains(t, tracker.failMessages[0], "失败单元 2")
	require.Contains(t, tracker.failMessages[0], "尝试 3 次")
	require.Contains(t, tracker.failMessages[0], "重试 2 次")
	require.Contains(t, tracker.failMessages[0], "严格 JSON Schema")
	require.Contains(t, tracker.failMessages[0], "request_invalid")
	require.Contains(t, tracker.failMessages[0], "HTTP 400")
	require.Contains(t, tracker.failMessages[0], "response_format")
	require.Equal(t, types.StageDocumentAnalysis, tracker.failed[1])
	require.NotContains(t, tracker.spans, types.StageChunking)
	require.NotContains(t, tracker.spans, types.StageEmbedding)
	analysis, parseErr := run.Knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Nil(t, analysis)
}

func TestApplyIngestionAdvisorDoesNotPersistAgentProviderErrorDetails(t *testing.T) {
	const sensitiveEvidence = "provider echoed aggregated private evidence"
	providerErr := errors.New(sensitiveEvidence)
	model := &ingestionAdvisorFullTextModel{
		agent: &ingestionAdvisorScriptedModel{streamErr: providerErr},
	}
	tracker := newIngestionSpanTrackerStub()
	knowledge := newSmartIngestionKnowledge(t, "private-provider-error")
	service := &knowledgeService{
		config: ingestionAdvisorOnConfig(),
		repo:   &ingestionKnowledgeRepoStub{},
		ingestionAdvisor: NewIngestionAdvisor(
			&ingestionAdvisorModelServiceStub{model: model}, nil,
		),
		spanTracker: tracker,
	}

	_, err := service.applyIngestionAdvisor(
		withAttempt(context.Background(), 1), smartIngestionRun(knowledge),
	)

	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorExecution, ingestionAdvisorRunErrorCode(err))
	require.NotContains(t, err.Error(), sensitiveEvidence)
	require.NotContains(t, knowledge.ErrorMessage, sensitiveEvidence)
	spanFailures, marshalErr := json.Marshal([][]string{tracker.failMessages, tracker.failErrors})
	require.NoError(t, marshalErr)
	require.NotContains(t, string(spanFailures), sensitiveEvidence)
}

func TestApplyIngestionAdvisorClearsStaleAnalysisBeforeFailedRetry(t *testing.T) {
	repo := &ingestionKnowledgeRepoStub{}
	knowledge := newSmartIngestionKnowledge(t, "doc-stale")
	require.NoError(t, knowledge.SetIngestionAnalysis(validIngestionAnalysis()))
	service := &knowledgeService{
		config: ingestionAdvisorOnConfig(),
		repo:   repo,
		ingestionAdvisor: &ingestionAdvisorStub{
			errors: []error{newIngestionAdvisorRunError(
				ingestionAdvisorErrorMaxRounds, "six rounds exhausted",
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
			service := &knowledgeService{
				config: ingestionAdvisorOnConfig(), repo: &ingestionKnowledgeRepoStub{},
				ingestionAdvisor: advisor, spanTracker: tracker,
			}
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
	service := &knowledgeService{
		config: ingestionAdvisorOnConfig(),
		repo:   &ingestionKnowledgeRepoStub{}, ingestionAdvisor: advisor,
	}
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
	service := &knowledgeService{
		config: ingestionAdvisorOnConfig(),
		repo:   &ingestionKnowledgeRepoStub{}, ingestionAdvisor: advisor,
	}
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
