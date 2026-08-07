package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestIngestionPreviewReturnsInvalidStructuralCandidateWithoutToolError(t *testing.T) {
	content := strings.Repeat("a", 60) + "\n\n" + strings.Repeat("b", 60) +
		"\n\n" + strings.Repeat("c", 60)
	document := chunker.SemanticDocument{
		ContentLength: len([]rune(content)),
		Blocks: []chunker.SemanticBlock{
			{ID: "block_1", Kind: chunker.SemanticKindParagraph, Start: 0, End: 50},
			{
				ID: "block_2", Kind: chunker.SemanticKindFAQ, Start: 50, End: 80,
				Atomic: true, Confidence: chunker.SemanticConfidenceHigh,
				ContextKinds: []string{"question", "answer"},
			},
			{ID: "block_3", Kind: chunker.SemanticKindParagraph, Start: 80, End: len([]rune(content))},
		},
	}
	session := newIngestionAgentSessionWithDocument(
		content,
		types.IngestionChunkingConstraints{},
		ingestionSessionDocument{document: document},
	)
	config := ingestionTestConfig(100)
	config.ChunkOverlap = 0

	candidate, err := buildIngestionCandidateForTest(session, config)

	require.NoError(t, err)
	require.False(t, candidate.HardValid)
	require.Contains(t, candidate.Violations, ingestionViolationAtomicSplit)
	require.Equal(t, 1, candidate.StructureQuality.SplitAtomicBlocks)
	require.Len(t, session.candidateSnapshot(), 1)
}

func TestIngestionCandidateValidationChecksCoverageAndTokenLimit(t *testing.T) {
	content := strings.Repeat("a", 120)
	document := analyzeIngestionTestDocument(t, content)
	request := ingestionValidationTestRequest(content, document, []chunker.Chunk{
		{Content: content[:40], Start: 0, End: 40},
		{Content: content[50:], Start: 50, End: 120},
	})
	request.constraints = types.IngestionChunkingConstraints{
		TokenLimit: 10, Languages: []string{chunker.LangEnglish},
	}

	result := requireIngestionCandidateValidation(t, request)

	require.Contains(t, result.violations, ingestionViolationSourceCoverage)
	require.Contains(t, result.violations, ingestionViolationTokenLimit)

	overlap := ingestionValidationTestRequest(content, document, []chunker.Chunk{
		{Content: content[:80], Start: 0, End: 80},
		{Content: content[60:], Start: 60, End: 120},
	})
	overlap.scoreConfig.ChunkOverlap = 10
	overlap.config.ChunkOverlap = 10
	overlapResult := requireIngestionCandidateValidation(t, overlap)
	require.Contains(t, overlapResult.violations, ingestionViolationOverlap)
}

func TestIngestionCandidateValidationCountsProductionTitlePrefix(t *testing.T) {
	content := "compact body words remain valid alone"
	document := analyzeIngestionTestDocument(t, content)
	counter, err := chunker.NewTokenCounter(chunker.TokenCounterConfig{
		Encoding: chunker.TokenizerEncodingCL100KBase,
	})
	require.NoError(t, err)
	bodyCount, err := counter.Count(content)
	require.NoError(t, err)
	prefix := "Long production document title"
	fullCount, err := counter.Count(chunker.PrependEmbeddingPrefix(prefix, content))
	require.NoError(t, err)
	require.Greater(t, fullCount.Count, bodyCount.Count)
	request := ingestionValidationTestRequest(content, document, []chunker.Chunk{{
		Content: content, Start: 0, End: len([]rune(content)),
	}})
	request.constraints = types.IngestionChunkingConstraints{
		TokenLimit: bodyCount.Count, TokenCounter: counter, EmbeddingPrefix: prefix,
	}

	result := requireIngestionCandidateValidation(t, request)

	require.Contains(t, result.violations, ingestionViolationTokenLimit)
	require.Equal(t, fullCount.Count, result.embeddingTokens)
}

func TestIngestionCandidateValidationTreatsTitleOversizeAtomicAsIneligible(t *testing.T) {
	content := "Q: compact question?\nA: compact answer."
	document := chunker.SemanticDocument{
		ContentLength: len([]rune(content)),
		Blocks: []chunker.SemanticBlock{{
			ID: "faq", Kind: chunker.SemanticKindFAQ, Start: 0, End: len([]rune(content)),
			Atomic: true, Confidence: chunker.SemanticConfidenceHigh,
			ContextKinds: []string{"question", "answer"},
		}},
	}
	counter, err := chunker.NewTokenCounter(chunker.TokenCounterConfig{
		Encoding: chunker.TokenizerEncodingCL100KBase,
	})
	require.NoError(t, err)
	bodyCount, err := counter.Count(content)
	require.NoError(t, err)
	cut := len([]rune(content)) / 2
	request := ingestionValidationTestRequest(content, document, []chunker.Chunk{
		{Content: string([]rune(content)[:cut]), Start: 0, End: cut},
		{Content: string([]rune(content)[cut:]), Start: cut, End: len([]rune(content))},
	})
	request.constraints = types.IngestionChunkingConstraints{
		TokenLimit: bodyCount.Count, TokenCounter: counter,
		EmbeddingPrefix: "Production title makes the complete atom too large",
	}
	request.scoreConfig.ChunkSize = len([]rune(content))

	result := requireIngestionCandidateValidation(t, request)

	require.Zero(t, result.quality.SplitAtomicBlocks)
	require.NotContains(t, result.violations, ingestionViolationAtomicSplit)
}

func TestIngestionCandidateValidationRejectsRequiredContextOverBudget(t *testing.T) {
	header := "| Very descriptive column name | Another descriptive column |\n| --- | --- |\n"
	content := header + "| value | result |\n"
	document := analyzeIngestionTestDocument(t, content)
	counter, err := chunker.NewTokenCounter(chunker.TokenCounterConfig{
		Encoding: chunker.TokenizerEncodingCL100KBase,
	})
	require.NoError(t, err)
	chunkSize := len([]rune(header))
	config := chunker.SplitterConfig{
		ChunkSize: chunkSize, AllowZeroOverlap: true,
		TokenLimit: 40, TokenCounter: counter,
	}
	chunks, err := chunker.SplitSemanticDocument(content, config, document)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)

	request := ingestionValidationTestRequest(content, document, chunks)
	request.scoreConfig = config
	request.constraints = types.IngestionChunkingConstraints{
		TokenLimit: 40, TokenCounter: counter,
	}
	result, err := validateIngestionCandidate(request)

	require.NoError(t, err)
	require.Contains(t, result.violations, ingestionViolationContextBudget)
	require.Contains(t, result.violations, ingestionViolationRequiredContext)
}

func TestIngestionPreviewSurfacesInjectedTokenCounterFailure(t *testing.T) {
	content := "# Heading\n\nbody"
	session := newIngestionAgentSession(content, types.IngestionChunkingConstraints{
		TokenLimit: 10, TokenCounter: failingIngestionTokenCounter{},
	})
	config := ingestionTestConfig(100)
	config.Strategy = chunker.StrategyAuto

	_, err := buildIngestionCandidateForTest(session, config)

	require.ErrorContains(t, err, "counter failed")
	require.Empty(t, session.candidateSnapshot())
}

type failingIngestionTokenCounter struct{}

func (failingIngestionTokenCounter) Count(string) (types.TokenCount, error) {
	return types.TokenCount{}, errors.New("counter failed")
}

func TestIngestionCandidateValidationRejectsReversedSourceOrder(t *testing.T) {
	content := "abcdefghijklmnop"
	document := analyzeIngestionTestDocument(t, content)
	request := ingestionValidationTestRequest(content, document, []chunker.Chunk{
		{Content: content[4:8], Start: 4, End: 8},
		{Content: content[:12], Start: 0, End: 12},
		{Content: content[12:], Start: 12, End: 16},
	})

	result := requireIngestionCandidateValidation(t, request)

	require.Contains(t, result.violations, ingestionViolationSourceOrder)
}

func TestIngestionPreviewSurfacesInvalidSemanticDocumentAsExecutionError(t *testing.T) {
	content := "abcdef"
	document := chunker.SemanticDocument{
		ContentLength: len([]rune(content)),
		Blocks:        []chunker.SemanticBlock{{ID: "broken", Start: 1, End: 6}},
	}
	session := newIngestionAgentSessionWithDocument(
		content,
		types.IngestionChunkingConstraints{},
		ingestionSessionDocument{document: document},
	)
	config := ingestionTestConfig(100)
	config.Strategy = chunker.StrategyAuto

	candidate, err := buildIngestionCandidateForTest(session, config)

	require.ErrorContains(t, err, "文档结构校验失败")
	require.Empty(t, candidate.ID)
	require.Empty(t, session.candidateSnapshot())
}

func TestIngestionCandidateValidationRequiresOriginalTableHeader(t *testing.T) {
	content := "| Name | Result |\n| --- | --- |\n| case-1 | pass |\n"
	document := analyzeIngestionTestDocument(t, content)
	headerEnd := document.Blocks[0].End
	row := document.Blocks[1]
	baseChunks := []chunker.Chunk{
		{Content: string([]rune(content)[:headerEnd]), Start: 0, End: headerEnd},
		{Content: string([]rune(content)[row.Start:]), Start: row.Start, End: row.End},
	}

	missing := requireIngestionCandidateValidation(
		t, ingestionValidationTestRequest(content, document, baseChunks),
	)
	require.Contains(t, missing.violations, ingestionViolationTableHeaderMissing)
	require.Equal(t, 1, missing.quality.HeaderlessContinuations)

	baseChunks[1].ContextHeader = "| case-1 | pass |"
	invalid := requireIngestionCandidateValidation(
		t, ingestionValidationTestRequest(content, document, baseChunks),
	)
	require.Contains(t, invalid.violations, ingestionViolationTableHeaderInvalid)
	require.Contains(t, invalid.violations, ingestionViolationContextSource)
}

func TestIngestionCandidateValidationRebuildsOrphanTableSyntaxLocally(t *testing.T) {
	content := "| orphan | row |\n\n# Alpha\ntext\n\n# Beta\ntext\n"
	document := analyzeIngestionTestDocument(t, content)
	length := len([]rune(content))
	chunks := []chunker.Chunk{{Content: content, Start: 0, End: length}}
	request := ingestionValidationTestRequest(content, document, chunks)
	request.scoreConfig.ChunkSize = length

	result := requireIngestionCandidateValidation(t, request)

	require.Empty(t, result.violations)
	require.Zero(t, result.quality.OrphanTableRows)
	require.Equal(t, 1, result.quality.MixedSections)
	require.Equal(t, chunker.SemanticKindPreamble, document.Blocks[0].Kind)
	require.Empty(t, document.Blocks[0].TableID)
}

func TestIngestionCandidateValidationCannotReceiveOrphanTableRow(t *testing.T) {
	content := "| orphan | row |\n"
	document := analyzeIngestionTestDocument(t, content)
	document.Blocks[0].Kind = chunker.SemanticKindTableRow
	document.Blocks[0].TableID = "orphan-table"
	length := len([]rune(content))

	err := chunker.ValidateSemanticDocument(document)
	require.ErrorContains(t, err, "has no header")
	require.Equal(t, length, document.ContentLength)
}

func TestIngestionCandidateValidationReportsOversizeAtomicWithoutHardFailure(t *testing.T) {
	body := strings.Repeat("x", 140)
	content := "```\n" + body + "\n```\n"
	document := analyzeIngestionTestDocument(t, content)
	runes := []rune(content)
	chunks := []chunker.Chunk{
		{Content: string(runes[:100]), Start: 0, End: 100},
		{Content: string(runes[100:]), ContextHeader: "```", Start: 100, End: len(runes)},
	}

	result := requireIngestionCandidateValidation(
		t, ingestionValidationTestRequest(content, document, chunks),
	)

	require.NotContains(t, result.violations, ingestionViolationAtomicSplit)
	require.NotContains(t, result.violations, ingestionViolationCodeContextMissing)
	require.Equal(t, 1, result.quality.OversizeAtomicBlocks)

	chunks[1].ContextHeader = ""
	missingContext := requireIngestionCandidateValidation(
		t, ingestionValidationTestRequest(content, document, chunks),
	)
	require.Contains(t, missingContext.violations, ingestionViolationCodeContextMissing)
}

func TestIngestionCandidatePreviewDescriptionsAreContentFree(t *testing.T) {
	content := "# Confidential Title\n\nRecord ID: secret-42\nOwner: Alice\n"
	session := newTestIngestionAgentSession(content)
	config := ingestionTestConfig(200)
	config.Strategy = chunker.StrategyAuto

	candidate, err := buildIngestionCandidateForTest(session, config)
	require.NoError(t, err)
	payload, err := json.Marshal(candidate.BlockDescriptions)
	require.NoError(t, err)

	require.NotContains(t, string(payload), "Confidential")
	require.NotContains(t, string(payload), "secret-42")
	require.NotContains(t, string(payload), "Alice")
	require.NotEmpty(t, candidate.BlockDescriptions)
	require.Equal(t, chunker.TierSemantic, chunker.StrategyTier(candidate.Diagnostics.SelectedTier))
}

func TestIngestionSemanticScorePenalizesBrokenTableContinuation(t *testing.T) {
	validation := ingestionCandidateValidationResult{
		atomicEligible: 1,
		atomicRetained: 1,
		quality: types.IngestionStructureQuality{
			HeaderlessContinuations: 1,
		},
		violations: []string{ingestionViolationTableHeaderMissing},
	}

	require.Equal(t, 0.5, scoreSemanticIntegrity(validation))
}

func analyzeIngestionTestDocument(t *testing.T, content string) chunker.SemanticDocument {
	t.Helper()
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)
	return document
}

func ingestionValidationTestRequest(
	content string,
	document chunker.SemanticDocument,
	chunks []chunker.Chunk,
) ingestionCandidateValidationRequest {
	return ingestionCandidateValidationRequest{
		content: content, document: document, chunks: chunks,
		config: ingestionTestConfig(100),
		scoreConfig: chunker.SplitterConfig{
			ChunkSize: 100, ChunkOverlap: 0, AllowZeroOverlap: true,
		},
	}
}

func requireIngestionCandidateValidation(
	t *testing.T,
	request ingestionCandidateValidationRequest,
) ingestionCandidateValidationResult {
	t.Helper()
	result, err := validateIngestionCandidate(request)
	require.NoError(t, err)
	return result
}
