package chunker

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAutoStrategyUsesSemanticTierFirst(t *testing.T) {
	content := strings.Repeat("A complete sentence ends here. Another sentence follows.\n\n", 8)
	chunks, diagnostics := SplitWithDiagnostics(content, SplitterConfig{
		Strategy: StrategyAuto, ChunkSize: 120, ChunkOverlap: 0, AllowZeroOverlap: true,
	})

	require.NotEmpty(t, chunks)
	require.Equal(t, TierSemantic, diagnostics.SelectedTier)
	require.Equal(t, TierSemantic, diagnostics.TierChain[0])
	requireChunksRestoreSource(t, content, chunks)
}

func TestSemanticPackingPreservesAtomicStructuresWithinBudget(t *testing.T) {
	content := "# Guide\n\nQ: Can I continue?\nA: Yes, keep the pair together.\n\n" +
		"- first item\n- second item\n\n![plot](plot.png)\nFigure caption\n"
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)

	chunks, err := SplitSemanticDocument(content, SplitterConfig{
		ChunkSize: 100, ChunkOverlap: 0, AllowZeroOverlap: true,
	}, document)

	require.NoError(t, err)
	requireChunksRestoreSource(t, content, chunks)
	require.True(t, anyChunkContains(chunks, "Q: Can I continue?\nA: Yes, keep the pair together."))
	require.True(t, anyChunkContains(chunks, "![plot](plot.png)\nFigure caption"))
}

func TestSemanticTableContinuationsUseOriginalHeader(t *testing.T) {
	header := "| Case | Result |\n| --- | --- |\n"
	content := header + strings.Repeat("| TC-001 | passed |\n", 12)
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)

	chunks, err := SplitSemanticDocument(content, SplitterConfig{
		ChunkSize: 90, ChunkOverlap: 0, AllowZeroOverlap: true,
	}, document)

	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)
	requireChunksRestoreSource(t, content, chunks)
	continuations := 0
	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, "TC-001") && !strings.Contains(chunk.Content, "| Case | Result |") {
			continuations++
			require.Equal(t, strings.TrimSpace(header), chunk.ContextHeader)
			require.NotContains(t, firstSemanticSourceLine(chunk.ContextHeader), "TC-001")
		}
	}
	require.Greater(t, continuations, 0)
}

func TestSemanticOversizeRecordAndCodeUseSafeContinuationContext(t *testing.T) {
	record := "Record ID: 42\nOwner: Integration Team\nStatus: Processing\nNotes: " + strings.Repeat("x", 70) + "\n"
	code := "```go\n" + strings.Repeat("fmt.Println(\"long line\")\n", 8) + "```\n"
	content := record + "\n" + code
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)

	chunks, err := SplitSemanticDocument(content, SplitterConfig{
		ChunkSize: 55, ChunkOverlap: 0, AllowZeroOverlap: true,
	}, document)

	require.NoError(t, err)
	requireChunksRestoreSource(t, content, chunks)
	for _, chunk := range chunks {
		require.LessOrEqual(t, len([]rune(chunk.Content)), 55)
		if chunk.Start > strings.Index(content, "```go") && strings.Contains(chunk.Content, "fmt.Println") {
			require.Contains(t, chunk.ContextHeader, "```go")
		}
		if strings.Contains(chunk.Content, "Status: Processing") {
			require.Contains(t, chunk.ContextHeader, "Record ID: 42")
		}
	}
}

func TestSemanticPackingHonorsTokenLimitIncludingContext(t *testing.T) {
	content := "# Context Heading\n\n" + strings.Repeat("plain english words for token budgeting. ", 10)
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)

	counter, err := NewTokenCounter(TokenCounterConfig{Encoding: TokenizerEncodingCL100KBase})
	require.NoError(t, err)
	chunks, err := SplitSemanticDocument(content, SplitterConfig{
		ChunkSize: 200, ChunkOverlap: 0, AllowZeroOverlap: true,
		TokenLimit: 20, Languages: []string{LangEnglish}, TokenCounter: counter,
	}, document)

	require.NoError(t, err)
	for _, chunk := range chunks {
		count, countErr := counter.Count(chunk.EmbeddingContent())
		require.NoError(t, countErr)
		require.LessOrEqual(t, count.Count, 20)
	}
	requireChunksRestoreSource(t, content, chunks)
}

func TestSemanticContextKeepsNearestHeadingAndReportsOmittedAncestor(t *testing.T) {
	content := "# Outer context heading with many descriptive words\n\n## Inner\n\n" +
		strings.Repeat("body words remain within the inner section. ", 8)
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)
	counter, err := NewTokenCounter(TokenCounterConfig{Encoding: TokenizerEncodingCL100KBase})
	require.NoError(t, err)

	chunks, err := SplitSemanticDocument(content, SplitterConfig{
		ChunkSize: 70, AllowZeroOverlap: true, TokenLimit: 30,
		Languages: []string{LangEnglish}, TokenCounter: counter,
	}, document)

	require.NoError(t, err)
	found := false
	for _, current := range chunks {
		if !strings.Contains(current.Content, "body words") || strings.Contains(current.Content, "## Inner") {
			continue
		}
		found = true
		require.Contains(t, current.ContextHeader, "## Inner")
		require.NotContains(t, current.ContextHeader, "Outer context")
		require.Contains(t, current.ContextReasonCodes, SemanticReasonAncestorOmitted)
		count, countErr := counter.Count(current.ContextHeader)
		require.NoError(t, countErr)
		require.LessOrEqual(t, count.Count, 6)
	}
	require.True(t, found)
}

func TestSemanticPackingSurfacesInjectedCounterFailure(t *testing.T) {
	content := "# Heading\n\nbody"
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)

	_, err = SplitSemanticDocument(content, SplitterConfig{
		ChunkSize: 20, TokenLimit: 10, TokenCounter: failingTokenCounter{},
	}, document)

	require.ErrorContains(t, err, "counter failed")
}

type failingTokenCounter struct{}

func (failingTokenCounter) Count(string) (TokenCount, error) {
	return TokenCount{}, errors.New("counter failed")
}

func TestSemanticPackingDoesNotRepeatSoftHeadingAsContext(t *testing.T) {
	content := "Potential heading\n\nBody text remains searchable without inferred context.\n"
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)
	heading := firstSemanticKind(document.Blocks, SemanticKindHeading)
	require.Equal(t, SemanticConfidenceSoft, heading.Confidence)

	chunks, err := SplitSemanticDocument(content, SplitterConfig{
		ChunkSize: 40, ChunkOverlap: 0, AllowZeroOverlap: true,
	}, document)

	require.NoError(t, err)
	for _, current := range chunks {
		require.NotContains(t, current.ContextHeader, "Potential heading")
	}
}

func TestSemanticPackingPolicyCanTrustSoftHeadingContext(t *testing.T) {
	content := "Potential heading\n\nBody text remains searchable with evidence-backed context.\n"
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)

	chunks, err := SplitSemanticDocument(content, SplitterConfig{
		ChunkSize: 40, ChunkOverlap: 0, AllowZeroOverlap: true,
		SemanticPackingPolicy: types.SemanticPackingPolicy{TrustSoftHeadings: true},
	}, document)

	require.NoError(t, err)
	found := false
	for _, current := range chunks {
		if strings.Contains(current.Content, "Body text") {
			found = true
			require.Contains(t, current.ContextHeader, "Potential heading")
		}
	}
	require.True(t, found)
}

func TestSemanticParentChildReusesRanges(t *testing.T) {
	content := "# Alpha\n\n" + strings.Repeat("Alpha sentence. ", 18) +
		"\n\n# Beta\n\n" + strings.Repeat("Beta sentence. ", 18)
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)

	result, err := SplitParentChildSemanticDocument(SemanticParentChildRequest{
		Content:      content,
		ParentConfig: SplitterConfig{ChunkSize: 150, Strategy: StrategyAuto, AllowZeroOverlap: true},
		ChildConfig:  SplitterConfig{ChunkSize: 55, Strategy: StrategyAuto, AllowZeroOverlap: true},
		Document:     document,
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.Children)
	runes := []rune(content)
	for _, child := range result.Children {
		require.Equal(t, string(runes[child.Start:child.End]), child.Content)
		if child.ParentIndex < 0 {
			continue
		}
		require.Less(t, child.ParentIndex, len(result.Parents))
		parent := result.Parents[child.ParentIndex]
		require.GreaterOrEqual(t, child.Start, parent.Start)
		require.LessOrEqual(t, child.End, parent.End)
	}
}

func TestSemanticParentChildRecountsFinalEmbeddingContent(t *testing.T) {
	content := "# Outer heading with verbose context words that should be omitted\n\n## Inner\n\n" +
		strings.Repeat("compact body sentence. ", 20)
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})
	require.NoError(t, err)
	counter, err := NewTokenCounter(TokenCounterConfig{Encoding: TokenizerEncodingCL100KBase})
	require.NoError(t, err)

	result, err := SplitParentChildSemanticDocument(SemanticParentChildRequest{
		Content:      content,
		ParentConfig: SplitterConfig{ChunkSize: 180, Strategy: StrategyAuto, AllowZeroOverlap: true},
		ChildConfig: SplitterConfig{
			ChunkSize: 70, Strategy: StrategyAuto, AllowZeroOverlap: true,
			TokenLimit: 30, TokenCounter: counter,
		},
		Document: document,
	})

	require.NoError(t, err)
	for _, child := range result.Children {
		count, countErr := counter.Count(child.EmbeddingContent())
		require.NoError(t, countErr)
		require.LessOrEqual(t, count.Count, 30)
	}
}

func TestExplicitLegacyStrategyRemainsUnchanged(t *testing.T) {
	content := strings.Repeat("legacy paragraph. ", 30)
	config := SplitterConfig{Strategy: StrategyLegacy, ChunkSize: 80, ChunkOverlap: 10}

	require.Equal(t, SplitText(content, NormalizeConfig(config)), Split(content, config))
}

func requireChunksRestoreSource(t *testing.T, content string, chunks []Chunk) {
	t.Helper()
	var restored strings.Builder
	expected := 0
	for _, chunk := range chunks {
		require.Equal(t, expected, chunk.Start)
		require.Equal(t, string([]rune(content)[chunk.Start:chunk.End]), chunk.Content)
		restored.WriteString(chunk.Content)
		expected = chunk.End
	}
	require.Equal(t, len([]rune(content)), expected)
	require.Equal(t, content, restored.String())
}

func anyChunkContains(chunks []Chunk, value string) bool {
	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, value) {
			return true
		}
	}
	return false
}
