package service

import (
	"context"
	"encoding/json"
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

func TestInspectIngestionDocumentUsesRuneOffsetsAndHardLimit(t *testing.T) {
	session := newIngestionAgentSession("甲乙丙丁")
	tool := newInspectIngestionDocument(session)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"offset":1,"limit":2}`))
	require.NoError(t, err)
	require.True(t, result.Success)

	var output inspectIngestionDocumentOutput
	require.NoError(t, json.Unmarshal([]byte(result.Output), &output))
	require.Equal(t, "乙丙", output.Content)
	require.Equal(t, 3, output.NextOffset)
	require.True(t, output.HasMore)
	require.Equal(t, 4, output.Statistics.CharacterCount)

	result, err = tool.Execute(context.Background(), json.RawMessage(`{"offset":0,"limit":8001}`))
	require.NoError(t, err)
	require.False(t, result.Success)
}

func TestIngestionPreviewDeduplicatesAndCapsDistinctCandidates(t *testing.T) {
	session := newIngestionAgentSession(ingestionTestContent())
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

func TestIngestionPreviewUsesNormalizedRealChunkerWithoutMutatingInput(t *testing.T) {
	content := ingestionTestContent()
	session := newIngestionAgentSession(content)
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

func TestIngestionCandidateHardValidationRejectsInvalidPositions(t *testing.T) {
	err := validateIngestionChunkPositions("正文", []chunker.Chunk{{
		Content: "错误", Start: 0, End: 2,
	}})
	require.ErrorContains(t, err, "位置与内容不一致")

	children := []chunker.Chunk{{Content: "正文", Start: 0, End: 2}}
	parents := []chunker.Chunk{{Content: "正", Start: 0, End: 1}}
	require.Error(t, validateParentChildPreview(children, parents, []int{0}))
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

func TestSubmitIngestionDecisionRequiresPreviewAndRejectsDuplicate(t *testing.T) {
	session := newIngestionAgentSession(ingestionTestContent())
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
