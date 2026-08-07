package service

import (
	"strings"
	"testing"

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

func buildIngestionCandidateForTest(
	session *ingestionAgentSession,
	config types.IngestionChunkingRecommendation,
) (types.IngestionChunkingCandidate, error) {
	normalized, err := normalizeIngestionPreviewConfig(config, session.constraints)
	if err != nil {
		return types.IngestionChunkingCandidate{}, err
	}
	id, err := ingestionCandidateID(normalized)
	if err != nil {
		return types.IngestionChunkingCandidate{}, err
	}
	candidate, err := session.buildCandidate(ingestionCandidateBuildRequest{
		content: session.content, config: normalized, constraints: session.constraints,
		id: id, document: session.document, documentErr: session.documentErr,
		policy: cloneSemanticPackingPolicy(session.policy),
	})
	if err != nil {
		return types.IngestionChunkingCandidate{}, err
	}
	generated := []types.IngestionChunkingCandidate{candidate}
	attachIngestionComparisonFacts(generated, nil)
	candidate = generated[0]
	session.candidates[candidate.ID] = candidate
	return candidate, nil
}

func TestIngestionPreviewUsesNormalizedRealChunkerWithoutMutatingInput(t *testing.T) {
	content := ingestionTestContent()
	session := newTestIngestionAgentSession(content)
	input := ingestionTestConfig(320)
	originalSeparators := append([]string(nil), input.Separators...)

	candidate, err := buildIngestionCandidateForTest(session, input)
	require.NoError(t, err)
	require.True(t, candidate.HardValid)
	require.NotEmpty(t, candidate.ID)
	require.NotZero(t, candidate.ChunkCount)
	require.Equal(t, chunker.TokenCountModeConservative, candidate.TokenCountMode)
	require.Equal(t, chunker.TokenizerEncodingByteUpperBound, candidate.TokenizerID)
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

	candidate, err := buildIngestionCandidateForTest(session, config)
	require.NoError(t, err)
	formalConfig := normalizeSplitterConfig(ingestionChunkingConfig(
		candidate.Config,
		types.IngestionChunkingConstraints{TokenLimit: 100, Languages: []string{chunker.LangEnglish}},
	), true)
	formalChunks, diagnostics := chunker.SplitWithDiagnostics(content, formalConfig)

	require.Equal(t, formalConfig.ChunkSize, candidate.Config.ChunkSize)
	require.Equal(t, formalConfig.ChunkOverlap, candidate.Config.ChunkOverlap)
	require.Equal(t, len(formalChunks), candidate.ChunkCount)
	require.Equal(t, ingestionTokenLengthDistribution(
		countTestChunkTokens(t, formalChunks, nil),
	), candidate.Lengths)
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

	candidate, err := buildIngestionCandidateForTest(session, config)
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
	require.Equal(t, ingestionTokenLengthDistribution(
		countTestChunkTokens(t, children, nil),
	), candidate.Lengths)
}

func TestIngestionPreviewAcceptsLongTableSourcePositions(t *testing.T) {
	content := "# 测试报告\n\n| 用例 | 模块 | 结果 |\n| --- | --- | --- |\n" +
		strings.Repeat("| TC-001 | 客户端 | 通过 |\n", 80)
	tests := []struct {
		name        string
		strategy    string
		parentChild bool
	}{
		{name: "legacy", strategy: chunker.StrategyLegacy},
		{name: "auto", strategy: chunker.StrategyAuto},
		{name: "legacy parent child", strategy: chunker.StrategyLegacy, parentChild: true},
		{name: "auto parent child", strategy: chunker.StrategyAuto, parentChild: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := ingestionTestConfig(120)
			config.Strategy = test.strategy
			config.ChunkOverlap = 10
			config.EnableParentChild = test.parentChild
			config.ParentChunkSize = 512
			config.ChildChunkSize = 120

			candidate, err := buildIngestionCandidateForTest(newTestIngestionAgentSession(content), config)

			require.NoError(t, err)
			require.True(t, candidate.HardValid)
			require.Greater(t, candidate.ChunkCount, 1)
		})
	}
}

func TestIngestionPreviewValidatesProductionParentChildMapping(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	config := ingestionTestConfig(300)
	config.EnableParentChild = true
	config.ParentChunkSize = 900
	config.ChildChunkSize = 180

	candidate, err := buildIngestionCandidateForTest(session, config)

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
	}), "位置未递增")

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
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)
	metrics := ingestionPreviewMetrics(ingestionCandidateMetricsRequest{
		content: content, document: document, chunks: chunks,
		config:      ingestionTestConfig(length),
		scoreConfig: chunker.SplitterConfig{ChunkSize: length, ChunkOverlap: 0},
		validation: ingestionCandidateValidationResult{
			atomicEligible: 4, atomicRetained: 4, contextValid: true,
			sourceTokens: []int{10}, embeddingTokens: 10,
		},
	})
	require.Equal(t, semanticIntegrityWeight, metrics.score.SemanticIntegrity)
	require.Equal(t, boundaryQualityWeight, metrics.score.BoundaryQuality)
	require.Equal(t, sizeFitWeight, metrics.score.SizeFit)
	require.Equal(t, contextEfficiencyWeight, metrics.score.ContextEfficiency)
	require.Equal(t, parentChildWeight, metrics.score.ParentChild)
	require.Equal(t, 100.0, metrics.score.Total)
	require.ElementsMatch(t, []string{"heading", "faq", "table"}, metrics.structure.PresentTypes)
}

func countTestChunkTokens(
	t *testing.T,
	chunks []chunker.Chunk,
	counter types.TokenCounter,
) []int {
	t.Helper()
	if counter == nil {
		var err error
		counter, err = chunker.NewTokenCounter(chunker.TokenCounterConfig{
			Encoding: chunker.TokenizerEncodingByteUpperBound,
		})
		require.NoError(t, err)
	}
	result := make([]int, len(chunks))
	for index, current := range chunks {
		count, err := counter.Count(current.Content)
		require.NoError(t, err)
		result[index] = count.Count
	}
	return result
}

func TestIngestionCandidateScoreComponentsPenalizeMismatches(t *testing.T) {
	require.Equal(t, 1.0, scoreSemanticIntegrity(ingestionCandidateValidationResult{
		atomicEligible: 1, atomicRetained: 1,
	}))
	require.Zero(t, scoreSemanticIntegrity(ingestionCandidateValidationResult{
		atomicEligible: 1,
	}))

	balanced := []chunker.Chunk{{Content: strings.Repeat("a", 100)}, {Content: "tail"}}
	unbalanced := []chunker.Chunk{{Content: strings.Repeat("a", 40)}, {Content: "tail"}}
	require.Equal(t, 1.0, scoreChunkSizeBalance(balanced, 100))
	require.Equal(t, 1.0, scoreChunkSizeBalance(unbalanced, 100))
	require.Zero(t, scoreChunkSizeBalance(
		[]chunker.Chunk{{Content: strings.Repeat("a", 101)}}, 100,
	))

	content := "alpha\n\nbeta"
	goodBoundary := []chunker.Chunk{{Start: 0, End: 7}, {Start: 7, End: len([]rune(content))}}
	badBoundary := []chunker.Chunk{{Start: 0, End: 5}, {Start: 5, End: len([]rune(content))}}
	document := chunker.SemanticDocument{ContentLength: len([]rune(content)), Blocks: []chunker.SemanticBlock{
		{Start: 0, End: 7}, {Start: 7, End: len([]rune(content))},
	}}
	require.Equal(t, 1.0, scoreBoundaryQuality(ingestionCandidateMetricsRequest{
		content: content, document: document, chunks: goodBoundary,
		config: types.IngestionChunkingRecommendation{Separators: []string{"\n\n"}},
	}))
	require.Zero(t, scoreBoundaryQuality(ingestionCandidateMetricsRequest{
		content: content, document: document, chunks: badBoundary,
		config: types.IngestionChunkingRecommendation{Separators: []string{"\n\n"}},
	}))

	require.Equal(t, 1.0, scoreContextEfficiency(goodBoundary, true))
	require.Zero(t, scoreContextEfficiency(goodBoundary, false))

	children := []chunker.Chunk{{Start: 10, End: 20}}
	parents := []chunker.Chunk{{Start: 0, End: 30}}
	require.Equal(t, 1.0, scoreParentChild(parentChildScoreRequest{
		children: children, parents: parents, parentIndexes: []int{0}, enabled: true,
	}))
	require.Zero(t, scoreParentChild(parentChildScoreRequest{
		children: children, parents: parents, parentIndexes: []int{1}, enabled: true,
	}))
}

func TestSubmitIngestionDecisionRequiresBackendCandidateAndRejectsDuplicate(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	_, err := session.submit(submitIngestionDecisionInput{CandidateID: "cand_unknown"})
	require.ErrorContains(t, err, "后端候选")

	candidate, err := buildIngestionCandidateForTest(session, ingestionTestConfig(300))
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
	candidate, err := buildIngestionCandidateForTest(session, ingestionTestConfig(300))
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
	_, err := buildIngestionCandidateForTest(session, ingestionTestConfig(300))
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

func TestValidateIngestionAgentOutcomeReportsUnresolvedSubmitFailureBeforeMaxRounds(t *testing.T) {
	session := newTestIngestionAgentSession(ingestionTestContent())
	_, err := buildIngestionCandidateForTest(session, ingestionTestConfig(300))
	require.NoError(t, err)
	state := &types.AgentState{
		StopReason: "max_iterations",
		RoundSteps: []types.AgentStep{{ToolCalls: []types.ToolCall{
			{Name: previewIngestionChunkingTool, Result: &types.ToolResult{Success: true}},
			{Name: submitIngestionDecisionTool, Result: &types.ToolResult{
				Success: false,
				Failure: &types.ToolFailure{
					Code: ingestionFailureDecisionInvalid, Field: "candidate_id",
					Constraint: "backend_selection_eligible_candidate",
				},
			}},
		}}},
	}

	err = validateIngestionAgentOutcome(state, session)

	require.Error(t, err)
	require.Equal(t, ingestionAdvisorErrorCandidate, ingestionAdvisorRunErrorCode(err))
	require.Contains(t, err.Error(), ingestionFailureDecisionInvalid)
}
