package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSplitKnowledgeDocumentUsesFinalSemanticTreeForSmartAuto(t *testing.T) {
	header := "| Case | Result |\n| --- | --- |\n"
	content := header + strings.Repeat("| TC-001 | passed |\n", 12)
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)

	result, err := splitKnowledgeDocument(knowledgeDocumentSplitRequest{
		content: content, document: document,
		effective: semanticEffectiveConfig(false, 90),
	})

	require.NoError(t, err)
	require.Greater(t, len(result.chunks), 1)
	requireParsedChunksCoverSource(t, content, result.chunks)
	continuations := 0
	for _, current := range result.chunks {
		if strings.Contains(current.Content, "TC-001") && !strings.Contains(current.Content, "| Case | Result |") {
			continuations++
			require.Equal(t, strings.TrimSpace(header), current.ContextHeader)
		}
	}
	require.Greater(t, continuations, 0)
}

func TestSplitKnowledgeDocumentUsesSemanticTreeForParentChild(t *testing.T) {
	content := "# Alpha\n\n" + strings.Repeat("Alpha sentence. ", 18) +
		"\n\n# Beta\n\n" + strings.Repeat("Beta sentence. ", 18)
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)
	effective := semanticEffectiveConfig(true, 150)
	effective.ChunkingConfig.ParentChunkSize = 150
	effective.ChunkingConfig.ChildChunkSize = 55

	result, err := splitKnowledgeDocument(knowledgeDocumentSplitRequest{
		content: content, document: document, effective: effective,
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.parents)
	requireParsedChunksCoverSource(t, content, result.chunks)
	for _, child := range result.chunks {
		if child.ParentIndex < 0 {
			continue
		}
		require.Less(t, child.ParentIndex, len(result.parents))
		parent := result.parents[child.ParentIndex]
		require.GreaterOrEqual(t, child.Start, parent.Start)
		require.LessOrEqual(t, child.End, parent.End)
	}
}

func TestSplitKnowledgeDocumentCountsTitleInFinalEmbeddingBudget(t *testing.T) {
	content := "# Scope\n\n" + strings.Repeat("production embedding input words. ", 12)
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)
	counter, err := chunker.NewTokenCounter(chunker.TokenCounterConfig{
		Encoding: chunker.TokenizerEncodingCL100KBase,
	})
	require.NoError(t, err)
	prefix := "Quarterly operations verification handbook"

	for _, parentChild := range []bool{false, true} {
		t.Run(map[bool]string{false: "flat", true: "parent_child"}[parentChild], func(t *testing.T) {
			effective := semanticEffectiveConfig(parentChild, 200)
			effective.ChunkingConfig.TokenLimit = 20
			effective.ChunkingConfig.Languages = []string{chunker.LangEnglish}
			effective.ChunkingConfig.ParentChunkSize = 200
			effective.ChunkingConfig.ChildChunkSize = 80
			result, splitErr := splitKnowledgeDocument(knowledgeDocumentSplitRequest{
				content: content, document: document, effective: effective,
				tokenCounter: counter, embeddingPrefix: prefix,
			})
			require.NoError(t, splitErr)
			require.Greater(t, len(result.chunks), 1)
			requireParsedChunksCoverSource(t, content, result.chunks)
			for _, current := range result.chunks {
				count, countErr := counter.Count(chunker.PrependEmbeddingPrefix(
					prefix, current.EmbeddingContent(),
				))
				require.NoError(t, countErr)
				require.LessOrEqual(t, count.Count, 20)
			}
		})
	}
}

func TestValidateKnowledgeEmbeddingBudgetRejectsTitleOverflow(t *testing.T) {
	counter, err := chunker.NewTokenCounter(chunker.TokenCounterConfig{
		Encoding: chunker.TokenizerEncodingCL100KBase,
	})
	require.NoError(t, err)
	effective := semanticEffectiveConfig(false, 100)
	effective.ChunkingConfig.TokenLimit = 5
	request := knowledgeDocumentSplitRequest{
		effective: effective, tokenCounter: counter,
		embeddingPrefix: "A title that consumes the complete budget",
	}

	err = validateKnowledgeEmbeddingBudget(request, []types.ParsedChunk{{Content: "body"}})

	require.ErrorContains(t, err, "embedding token 超限")
}

func TestSplitKnowledgeDocumentFallbackUsesOrdinaryConfig(t *testing.T) {
	content := strings.Repeat("ordinary paragraph. ", 30)
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)
	effective := types.EffectiveProcessConfig{
		IngestionAppliedMode: types.IngestionAppliedModeFallback,
		ChunkingConfig: types.ChunkingConfig{
			Strategy: chunker.StrategyLegacy, ChunkSize: 80, ChunkOverlap: 10,
		},
	}

	result, err := splitKnowledgeDocument(knowledgeDocumentSplitRequest{
		content: content, document: document, effective: effective,
	})

	require.NoError(t, err)
	expected := chunker.Split(content, buildSplitterConfigFromEffective(effective))
	require.Len(t, result.chunks, len(expected))
	for index, current := range result.chunks {
		require.Equal(t, expected[index].Content, current.Content)
		require.Equal(t, expected[index].Start, current.Start)
		require.Equal(t, expected[index].End, current.End)
	}
}

func TestSplitKnowledgeDocumentFallbackFailsWhenOrdinarySplitIsInvalid(t *testing.T) {
	_, err := splitKnowledgeDocument(knowledgeDocumentSplitRequest{
		effective: types.EffectiveProcessConfig{
			IngestionAppliedMode: types.IngestionAppliedModeFallback,
			ChunkingConfig:       types.ChunkingConfig{Strategy: chunker.StrategyLegacy},
		},
	})

	require.ErrorContains(t, err, "普通分块回退校验失败")
	require.ErrorContains(t, err, ingestionViolationSourceCoverage)
}

func semanticEffectiveConfig(parentChild bool, size int) types.EffectiveProcessConfig {
	return types.EffectiveProcessConfig{
		IngestionAdvisorApplied: true, IngestionAppliedMode: types.IngestionAppliedModeSmart,
		ChunkingConfig: types.ChunkingConfig{
			Strategy: chunker.StrategyAuto, ChunkSize: size, ChunkOverlap: 0,
			EnableParentChild: parentChild,
		},
	}
}

func requireParsedChunksCoverSource(t *testing.T, content string, chunks []types.ParsedChunk) {
	t.Helper()
	runes := []rune(content)
	coveredEnd := 0
	for _, current := range chunks {
		require.LessOrEqual(t, current.Start, coveredEnd)
		require.Equal(t, string(runes[current.Start:current.End]), current.Content)
		coveredEnd = max(coveredEnd, current.End)
	}
	require.Equal(t, len(runes), coveredEnd)
}
