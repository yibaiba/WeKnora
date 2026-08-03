package service

import (
	"context"
	"errors"
	"testing"

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
) (*types.IngestionAnalysis, error) {
	call := len(s.requests)
	s.requests = append(s.requests, request)
	if call < len(s.errors) && s.errors[call] != nil {
		return nil, s.errors[call]
	}
	if call >= len(s.responses) {
		return nil, errors.New("no stub response")
	}
	return s.responses[call], nil
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
	spans   map[string]*Span
	ended   []string
	failed  []string
	skipped []string
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

func (s *ingestionSpanTrackerStub) EndSpan(_ context.Context, span *Span, _ types.JSONMap) {
	s.ended = append(s.ended, span.Name)
}

func (s *ingestionSpanTrackerStub) FailSpan(_ context.Context, span *Span, _, _ string, _ error) {
	s.failed = append(s.failed, span.Name)
}

func (s *ingestionSpanTrackerStub) SkipSpan(_ context.Context, span *Span, _ string) {
	s.skipped = append(s.skipped, span.Name)
}

func validIngestionAnalysis() *types.IngestionAnalysis {
	response := validAdvisorModelResponse()
	return &types.IngestionAnalysis{
		DocumentKind:           response.DocumentKind,
		Confidence:             response.Confidence,
		RecommendedContentMode: response.RecommendedContentMode,
		ReasonCodes:            append([]string(nil), response.ReasonCodes...),
		Summary:                response.Summary,
		RecommendedChunking:    cloneChunkingRecommendation(response.RecommendedChunking),
		ModelID:                "untrusted-model",
		PromptVersion:          "untrusted-version",
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

	persisted, err := run.Knowledge.IngestionAnalysis()
	require.NoError(t, err)
	require.Equal(t, "summary-1", persisted.ModelID)
	require.Equal(t, types.IngestionPromptVersionV1, persisted.PromptVersion)
	require.Equal(t, persisted.RecommendedChunking, persisted.AppliedChunking)
	require.Equal(t, 1, repo.updateColumnCalls)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, []string{types.StageDocumentAnalysis}, tracker.ended)
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
	require.NotContains(t, tracker.spans, types.StageChunking)
	require.NotContains(t, tracker.spans, types.StageEmbedding)
	analysis, parseErr := run.Knowledge.IngestionAnalysis()
	require.NoError(t, parseErr)
	require.Nil(t, analysis)
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
