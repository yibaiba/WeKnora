package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func ingestionTestConfig(size int) types.IngestionChunkingRecommendation {
	return types.IngestionChunkingRecommendation{
		Strategy:          chunker.StrategyLegacy,
		ChunkSize:         size,
		ChunkOverlap:      20,
		EnableParentChild: false,
		ParentChunkSize:   1024,
		ChildChunkSize:    256,
		Separators:        []string{"\n\n", "\n", "。"},
	}
}

func ingestionTestContent() string {
	return strings.Repeat("# 标题\n\nQ: 如何处理？\nA: 按照步骤处理。\n\n|列一|列二|\n|---|---|\n|甲|乙|\n\n正文内容用于真实预切分。\n\n", 80)
}

func newTestIngestionAgentSession(content string) *ingestionAgentSession {
	return newIngestionAgentSession(content, types.IngestionChunkingConstraints{})
}

func TestIngestionPreviewDeduplicatesAndCapsDistinctCandidates(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	first, err := session.preview(ingestionTestConfig(300))
	require.NoError(t, err)
	duplicate, err := session.preview(ingestionTestConfig(300))
	require.NoError(t, err)
	require.Equal(t, first.ID, duplicate.ID)
	require.Len(t, session.candidateSnapshot(), 1)

	_, err = session.preview(ingestionTestConfig(400))
	require.NoError(t, err)
	_, err = session.preview(ingestionTestConfig(500))
	require.NoError(t, err)
	_, err = session.preview(ingestionTestConfig(600))
	require.ErrorContains(t, err, "最多预览 3 个")
}

func TestPreviewIngestionToolExposesSubmissionProtocol(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	tool := newPreviewIngestionChunking(session)
	for index, size := range []int{300, 400, 500} {
		arguments, err := json.Marshal(ingestionTestConfig(size))
		require.NoError(t, err)
		result, err := tool.Execute(context.Background(), arguments)
		require.NoError(t, err)
		require.True(t, result.Success)

		var output previewIngestionChunkingOutput
		require.NoError(t, json.Unmarshal([]byte(result.Output), &output))
		require.Equal(t, output.Candidate.ID, output.CandidateID)
		require.Equal(t, index+1, output.SavedCandidateCount)
		require.Equal(t, maxIngestionCandidates, output.CandidateLimit)
		if index < maxIngestionCandidates-1 {
			require.Equal(t, "preview_or_submit", output.NextAction)
			continue
		}
		require.Equal(t, submitIngestionDecisionTool, output.NextAction)
	}
}

func TestPreviewIngestionToolReturnsSafeConstraintFailure(t *testing.T) {
	config := ingestionTestConfig(200)
	config.ChunkOverlap = 101
	arguments, err := json.Marshal(config)
	require.NoError(t, err)

	result, err := newPreviewIngestionChunking(
		newTestIngestionAgentSession(ingestionTestContent()),
	).Execute(context.Background(), arguments)

	require.NoError(t, err)
	require.False(t, result.Success)
	require.ErrorContains(t, errors.New(result.Error), "chunk_overlap 必须在 0 到 100 之间")
	require.Equal(t, &types.ToolFailure{
		Code: ingestionFailureOverlapInvalid, Field: "chunk_overlap",
		Constraint: "at_most_half_chunk_size",
	}, result.Failure)
}

func TestPreviewIngestionSchemaUsesBackendConstraints(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Minimum     int      `json:"minimum"`
			Maximum     int      `json:"maximum"`
			Description string   `json:"description"`
			Enum        []string `json:"enum"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(previewIngestionChunkingSchema(), &schema))
	require.Equal(t, minimumAdvisorChunkSize, schema.Properties["chunk_size"].Minimum)
	require.Equal(t, maximumAdvisorChunkSize, schema.Properties["chunk_size"].Maximum)
	require.Equal(t, maximumAdvisorOverlap, schema.Properties["chunk_overlap"].Maximum)
	require.Contains(t, schema.Properties["chunk_overlap"].Description, "half")
	require.Equal(t, allowedChunkingStrategyValues[:], schema.Properties["strategy"].Enum)
}

func TestIngestionPreviewBuildsDistinctCandidatesConcurrently(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	session.buildCandidate = func(request ingestionCandidateBuildRequest) (types.IngestionChunkingCandidate, error) {
		started <- struct{}{}
		<-release
		return buildIngestionCandidate(request)
	}

	results := make(chan error, 2)
	go func() { _, err := session.preview(ingestionTestConfig(300)); results <- err }()
	go func() { _, err := session.preview(ingestionTestConfig(400)); results <- err }()
	waitForIngestionPreviewSignals(t, started, 2)
	close(release)
	require.NoError(t, <-results)
	require.NoError(t, <-results)

	snapshot := session.candidateSnapshot()
	require.Len(t, snapshot, 2)
	require.Less(t, snapshot[0].ID, snapshot[1].ID)
}

func TestIngestionPreviewSharesSameCandidateInFlight(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var buildCalls atomic.Int32
	session.buildCandidate = func(request ingestionCandidateBuildRequest) (types.IngestionChunkingCandidate, error) {
		buildCalls.Add(1)
		started <- struct{}{}
		<-release
		return buildIngestionCandidate(request)
	}

	type previewResult struct {
		candidate types.IngestionChunkingCandidate
		err       error
	}
	results := make(chan previewResult, 2)
	go func() {
		candidate, err := session.preview(ingestionTestConfig(300))
		results <- previewResult{candidate: candidate, err: err}
	}()
	waitForIngestionPreviewSignals(t, started, 1)
	go func() {
		candidate, err := session.preview(ingestionTestConfig(300))
		results <- previewResult{candidate: candidate, err: err}
	}()
	close(release)
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Equal(t, int32(1), buildCalls.Load())
	require.Equal(t, first.candidate.ID, second.candidate.ID)
}

func TestIngestionPreviewFailureReleasesReservation(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	var buildCalls atomic.Int32
	session.buildCandidate = func(request ingestionCandidateBuildRequest) (types.IngestionChunkingCandidate, error) {
		if buildCalls.Add(1) == 1 {
			return types.IngestionChunkingCandidate{}, errors.New("chunker unavailable")
		}
		return buildIngestionCandidate(request)
	}

	_, err := session.preview(ingestionTestConfig(300))
	require.ErrorContains(t, err, "chunker unavailable")
	candidate, err := session.preview(ingestionTestConfig(300))
	require.NoError(t, err)
	require.NotEmpty(t, candidate.ID)
	require.Len(t, session.candidateSnapshot(), 1)
}

func TestIngestionPreviewFlightPropagatesFailureToWaiter(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	normalized, err := normalizeIngestionPreviewConfig(
		ingestionTestConfig(300), types.IngestionChunkingConstraints{},
	)
	require.NoError(t, err)
	id, err := ingestionCandidateID(normalized)
	require.NoError(t, err)

	_, ownerFlight, owner, err := session.reservePreview(id)
	require.NoError(t, err)
	require.True(t, owner)
	_, waiterFlight, waiterOwns, err := session.reservePreview(id)
	require.NoError(t, err)
	require.False(t, waiterOwns)
	require.Same(t, ownerFlight, waiterFlight)

	buildErr := errors.New("chunker unavailable")
	session.completePreview(id, ownerFlight, ingestionCandidateBuildResult{err: buildErr})
	<-waiterFlight.done
	require.ErrorIs(t, waiterFlight.err, buildErr)
	require.Empty(t, session.candidateSnapshot())
}

func TestIngestionPreviewConcurrentReservationsEnforceCandidateCap(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	started := make(chan struct{}, maxIngestionCandidates)
	release := make(chan struct{})
	session.buildCandidate = func(request ingestionCandidateBuildRequest) (types.IngestionChunkingCandidate, error) {
		started <- struct{}{}
		<-release
		return buildIngestionCandidate(request)
	}

	results := make(chan error, maxIngestionCandidates)
	for _, size := range []int{300, 400, 500} {
		go func() { _, err := session.preview(ingestionTestConfig(size)); results <- err }()
	}
	waitForIngestionPreviewSignals(t, started, maxIngestionCandidates)
	_, err := session.preview(ingestionTestConfig(600))
	require.ErrorContains(t, err, "最多预览 3 个")
	close(release)
	for range maxIngestionCandidates {
		require.NoError(t, <-results)
	}
}

func waitForIngestionPreviewSignals(t *testing.T, signals <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-signals:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent ingestion previews")
		}
	}
}

func TestIngestionPreviewUsesNormalizedRealChunkerWithoutMutatingInput(t *testing.T) {
	content := ingestionTestContent()
	session := newTestIngestionAgentSession(content)
	input := ingestionTestConfig(320)
	originalSeparators := append([]string(nil), input.Separators...)

	candidate, err := session.preview(input)
	require.NoError(t, err)
	require.True(t, candidate.HardValid)
	require.NotEmpty(t, candidate.ID)
	require.NotZero(t, candidate.ChunkCount)
	require.Equal(t, originalSeparators, input.Separators)
	require.Equal(t, chunker.StrategyLegacy, candidate.Diagnostics.SelectedTier)

	actual, diagnostics := chunker.SplitWithDiagnostics(content, chunker.SplitterConfig{
		ChunkSize: input.ChunkSize, ChunkOverlap: input.ChunkOverlap,
		AllowZeroOverlap: true, Separators: input.Separators, Strategy: input.Strategy,
	})
	require.Equal(t, len(actual), candidate.ChunkCount)
	require.Equal(t, string(diagnostics.SelectedTier), candidate.Diagnostics.SelectedTier)
}

func TestIngestionPreviewMatchesTokenAndLanguageConstrainedFormalSplitter(t *testing.T) {
	content := ingestionTestContent()
	constraints := types.IngestionChunkingConstraints{
		TokenLimit: 100,
		Languages:  []string{chunker.LangEnglish},
	}
	session := newIngestionAgentSession(content, constraints)
	constraints.Languages[0] = chunker.LangChinese
	config := ingestionTestConfig(4000)
	config.ChunkOverlap = 500

	candidate, err := session.preview(config)
	require.NoError(t, err)
	formalConfig := normalizeSplitterConfig(ingestionChunkingConfig(
		candidate.Config,
		types.IngestionChunkingConstraints{TokenLimit: 100, Languages: []string{chunker.LangEnglish}},
	), true)
	formalChunks, diagnostics := chunker.SplitWithDiagnostics(content, formalConfig)

	require.Equal(t, formalConfig.ChunkSize, candidate.Config.ChunkSize)
	require.Equal(t, formalConfig.ChunkOverlap, candidate.Config.ChunkOverlap)
	require.Equal(t, len(formalChunks), candidate.ChunkCount)
	require.Equal(t, ingestionLengthDistribution(formalChunks), candidate.Lengths)
	require.Equal(t, string(diagnostics.SelectedTier), candidate.Diagnostics.SelectedTier)
}

func TestParentChildPreviewMatchesFormalConstrainedConfigs(t *testing.T) {
	content := ingestionTestContent()
	constraints := types.IngestionChunkingConstraints{
		TokenLimit: 100,
		Languages:  []string{chunker.LangEnglish},
	}
	config := ingestionTestConfig(4000)
	config.ChunkOverlap = 500
	config.EnableParentChild = true
	config.ParentChunkSize = 900
	config.ChildChunkSize = 180
	session := newIngestionAgentSession(content, constraints)

	candidate, err := session.preview(config)
	require.NoError(t, err)
	formalChunking := ingestionChunkingConfig(candidate.Config, constraints)
	formalBase := normalizeSplitterConfig(formalChunking, true)
	parentConfig, childConfig := buildParentChildConfigs(formalChunking, formalBase)
	formal := chunker.SplitParentChild(content, parentConfig, childConfig)
	children := make([]chunker.Chunk, len(formal.Children))
	for index, child := range formal.Children {
		children[index] = child.Chunk
	}

	require.Equal(t, len(formal.Parents), candidate.ParentChunkCount)
	require.Equal(t, len(children), candidate.ChunkCount)
	require.Equal(t, ingestionLengthDistribution(children), candidate.Lengths)
}

func TestIngestionPreviewValidatesProductionParentChildMapping(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	config := ingestionTestConfig(300)
	config.EnableParentChild = true
	config.ParentChunkSize = 900
	config.ChildChunkSize = 180

	candidate, err := session.preview(config)

	require.NoError(t, err)
	require.True(t, candidate.HardValid)
	require.NotZero(t, candidate.ChunkCount)
	require.NotZero(t, candidate.ParentChunkCount)
	require.Equal(t, parentChildWeight, candidate.Score.ParentChild)
}

func TestIngestionCandidateHardValidationRejectsInvalidPositions(t *testing.T) {
	err := validateIngestionChunkPositions("正文", []chunker.Chunk{{
		Content: "错误", Start: 0, End: 2,
	}})
	require.ErrorContains(t, err, "位置与内容不一致")
	require.ErrorContains(t, validateIngestionChunkOrder([]chunker.Chunk{
		{Content: "正文", Start: 0, End: 2},
		{Content: "文", Start: 1, End: 2},
	}), "结束位置未递增")

	children := []chunker.Chunk{{Content: "正文", Start: 0, End: 2}}
	parents := []chunker.Chunk{{Content: "正", Start: 0, End: 1}}
	require.Error(t, validateParentChildPreview(children, parents, []int{0}))
	require.ErrorContains(t, validateParentChildPreview(
		[]chunker.Chunk{{Start: 0, End: 2}, {Start: 1, End: 2}},
		[]chunker.Chunk{{Start: 0, End: 2}}, []int{0, 0},
	), "结束位置未递增")
}

func TestIngestionCandidateScoresAllNamedDimensions(t *testing.T) {
	content := "# 标题\nQ: 问题？\nA: 回答。\n|a|b|\n|---|---|\n|1|2|"
	length := len([]rune(content))
	chunks := []chunker.Chunk{{Content: content, Start: 0, End: length}}
	metrics := ingestionPreviewMetrics(
		content,
		chunks,
		nil,
		nil,
		ingestionTestConfig(length),
		chunker.SplitterConfig{ChunkSize: length, ChunkOverlap: 0},
	)
	require.Equal(t, structureIntegrityWeight, metrics.score.StructureIntegrity)
	require.Equal(t, chunkSizeBalanceWeight, metrics.score.ChunkSizeBalance)
	require.Equal(t, boundaryQualityWeight, metrics.score.BoundaryQuality)
	require.Equal(t, overlapEfficiencyWeight, metrics.score.OverlapEfficiency)
	require.Equal(t, parentChildWeight, metrics.score.ParentChild)
	require.Equal(t, 100.0, metrics.score.Total)
	require.ElementsMatch(t, []string{"heading", "faq", "table"}, metrics.structure.PresentTypes)
}

func TestIngestionCandidateScoreComponentsPenalizeMismatches(t *testing.T) {
	spans := []sourceSpan{{kind: "heading", start: 0, end: 10}}
	_, retained := scoreStructureRetention(spans, []chunker.Chunk{{Start: 0, End: 10}})
	_, split := scoreStructureRetention(spans, []chunker.Chunk{{Start: 0, End: 5}, {Start: 5, End: 10}})
	require.Equal(t, 1.0, retained)
	require.Zero(t, split)

	balanced := []chunker.Chunk{{Content: strings.Repeat("a", 100)}, {Content: "tail"}}
	unbalanced := []chunker.Chunk{{Content: strings.Repeat("a", 40)}, {Content: "tail"}}
	require.Equal(t, 1.0, scoreChunkSizeBalance(balanced, 100))
	require.Zero(t, scoreChunkSizeBalance(unbalanced, 100))

	content := "alpha\n\nbeta"
	goodBoundary := []chunker.Chunk{{Start: 0, End: 7}, {Start: 7, End: len([]rune(content))}}
	badBoundary := []chunker.Chunk{{Start: 0, End: 5}, {Start: 5, End: len([]rune(content))}}
	require.Equal(t, 1.0, scoreBoundaryQuality(content, goodBoundary, nil, []string{"\n\n"}))
	require.Zero(t, scoreBoundaryQuality(content, badBoundary, nil, []string{"\n\n"}))

	matchedOverlap := []chunker.Chunk{{Start: 0, End: 100}, {Start: 80, End: 150}}
	mismatchedOverlap := []chunker.Chunk{{Start: 0, End: 100}, {Start: 100, End: 150}}
	require.Equal(t, 1.0, scoreOverlapEfficiency(matchedOverlap, 20))
	require.Zero(t, scoreOverlapEfficiency(mismatchedOverlap, 20))

	children := []chunker.Chunk{{Start: 10, End: 20}}
	parents := []chunker.Chunk{{Start: 0, End: 30}}
	require.Equal(t, 1.0, scoreParentChild(children, parents, []int{0}, true))
	require.Zero(t, scoreParentChild(children, parents, []int{1}, true))
}

func TestSubmitIngestionDecisionRequiresPreviewAndRejectsDuplicate(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	_, err := session.submit(submitIngestionDecisionInput{CandidateID: "cand_unknown"})
	require.ErrorContains(t, err, "未预览")

	candidate, err := session.preview(ingestionTestConfig(300))
	require.NoError(t, err)
	input := submitIngestionDecisionInput{
		CandidateID:            candidate.ID,
		DocumentKind:           types.IngestionDocumentKindPolicyManual,
		Confidence:             0.9,
		RecommendedContentMode: types.IngestionContentModeDocument,
		ReasonCodes:            []string{"structure_retained"},
		Summary:                "选择已经真实预览的候选",
	}
	analysis, err := session.submit(input)
	require.NoError(t, err)
	require.Equal(t, candidate.Config, analysis.RecommendedChunking)
	require.Equal(t, candidate.ID, session.selectedCandidateID())

	_, err = session.submit(input)
	require.ErrorContains(t, err, "已经提交")
}

func TestValidateIngestionAgentOutcomeTreatsSuccessfulSubmitAsAuthoritative(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	candidate, err := session.preview(ingestionTestConfig(300))
	require.NoError(t, err)
	_, err = session.submit(submitIngestionDecisionInput{
		CandidateID:            candidate.ID,
		DocumentKind:           types.IngestionDocumentKindPolicyManual,
		Confidence:             0.9,
		RecommendedContentMode: types.IngestionContentModeDocument,
		ReasonCodes:            []string{"structure_retained"},
		Summary:                "选择已经真实预览的候选",
	})
	require.NoError(t, err)

	state := &types.AgentState{
		TerminatedByTool: submitIngestionDecisionTool,
		RoundSteps: []types.AgentStep{{ToolCalls: []types.ToolCall{{
			Name: previewIngestionChunkingTool,
			Result: &types.ToolResult{
				Success: false,
				Error:   "earlier candidate was invalid",
			},
		}}}},
	}
	require.NoError(t, validateIngestionAgentOutcome(state, session))
}

func TestValidateIngestionAgentOutcomeReportsMaxRoundsAfterRecoveredPreview(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	_, err := session.preview(ingestionTestConfig(300))
	require.NoError(t, err)
	state := &types.AgentState{
		StopReason: "max_iterations",
		RoundSteps: []types.AgentStep{{ToolCalls: []types.ToolCall{
			{Name: previewIngestionChunkingTool, Result: &types.ToolResult{Success: false}},
			{Name: previewIngestionChunkingTool, Result: &types.ToolResult{Success: true}},
		}}},
	}

	err = validateIngestionAgentOutcome(state, session)

	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorMaxRounds, ingestionAdvisorRunErrorCode(err))
}
