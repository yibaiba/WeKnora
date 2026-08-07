package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type failingCandidateTokenCounter struct{}

func (failingCandidateTokenCounter) Count(string) (types.TokenCount, error) {
	return types.TokenCount{}, errors.New("tokenizer unavailable")
}

func TestGenerateIngestionCandidateSpecsBuildsThreeArchetypes(t *testing.T) {
	fallback := ingestionTestConfig(1000)
	session := newIngestionAgentSessionWithFallback(
		ingestionTestContent(), types.IngestionChunkingConstraints{}, fallback,
	)

	specs, err := generateIngestionCandidateSpecs(session)

	require.NoError(t, err)
	require.Len(t, specs, maxIngestionCandidates)
	require.Equal(t, ingestionCandidateArchetypeRetrievalDense, specs[0].archetype)
	require.Equal(t, 700, specs[0].config.ChunkSize)
	require.True(t, specs[0].config.EnableParentChild)
	require.Equal(t, 700, specs[0].config.ChildChunkSize)
	require.Equal(t, 2800, specs[0].config.ParentChunkSize)
	require.Equal(t, ingestionCandidateArchetypeBalanced, specs[1].archetype)
	require.Equal(t, fallback.ChunkSize, specs[1].config.ChunkSize)
	require.Equal(t, fallback.EnableParentChild, specs[1].config.EnableParentChild)
	require.Equal(t, ingestionCandidateArchetypeStructurePreserving, specs[2].archetype)
	require.Equal(t, 1300, specs[2].config.ChunkSize)
	require.Zero(t, specs[2].config.ChunkOverlap)
	require.NotEqual(t, specs[0].config, specs[1].config)
	require.NotEqual(t, specs[1].config, specs[2].config)
}

func TestBalancedCandidateUsesHighConfidenceAtomicP90(t *testing.T) {
	content := strings.Repeat("甲", 1200)
	session := newIngestionAgentSessionWithDocument(content, types.IngestionChunkingConstraints{}, ingestionSessionDocument{
		document: chunker.SemanticDocument{ContentLength: len([]rune(content)), Blocks: []chunker.SemanticBlock{{
			ID: "faq", Kind: chunker.SemanticKindFAQ, Start: 0, End: len([]rune(content)),
			Atomic: true, Confidence: chunker.SemanticConfidenceHigh,
		}}},
		fallback: ingestionTestConfig(512),
	})

	specs, err := generateIngestionCandidateSpecs(session)

	require.NoError(t, err)
	require.Equal(t, 1200, specs[1].config.ChunkSize)
}

func TestSemanticPackingPolicyMapsOnlyValidatedEvidenceEnums(t *testing.T) {
	evidence := ingestionDocumentEvidence{
		DominantStructures: []string{"repeated_records", "table"},
		BoundaryPriorities: []string{"record", "table_row", "section"},
		RiskSignals:        []string{"unreliable_headings", "repeated_headers_footers"},
	}

	policy := semanticPackingPolicyFromEvidence(evidence)

	require.Equal(t, ingestionPackingPolicyVersion, policy.Version)
	require.False(t, policy.TrustSoftHeadings)
	require.True(t, policy.SeparateRecords)
	require.True(t, policy.PreserveRepeatedPageRegions)
	require.Equal(t, []string{"record", "table_row", "section"}, policy.StrongBoundaryOrder)
	require.Equal(t, ingestionContextTokenPercent, policy.ContextTokenPercent)
	require.Equal(t, ingestionContextTokenLimit, policy.ContextTokenLimit)
	evidence.BoundaryPriorities[0] = "section"
	require.Equal(t, "record", policy.StrongBoundaryOrder[0])
}

func TestCandidateGenerationFailsWhenNormalizationCannotProduceThreeConfigs(t *testing.T) {
	fallback := ingestionTestConfig(100)
	fallback.ChunkOverlap = 0
	fallback.EnableParentChild = true
	fallback.ParentChunkSize = minimumAdvisorParentSize
	fallback.ChildChunkSize = 100
	session := newIngestionAgentSessionWithFallback(
		"short content", types.IngestionChunkingConstraints{TokenLimit: 1}, fallback,
	)

	_, err := generateIngestionCandidateSpecs(session)

	require.ErrorContains(t, err, "无法为")
}

func TestCandidateGenerationSurfacesTokenCounterFailure(t *testing.T) {
	content := "Q: question\nA: answer"
	session := newIngestionAgentSessionWithDocument(content, types.IngestionChunkingConstraints{
		TokenLimit: 100, TokenCounter: failingCandidateTokenCounter{},
	}, ingestionSessionDocument{
		document: chunker.SemanticDocument{ContentLength: len([]rune(content)), Blocks: []chunker.SemanticBlock{{
			ID: "faq", Kind: chunker.SemanticKindFAQ, Start: 0, End: len([]rune(content)),
			Atomic: true, Confidence: chunker.SemanticConfidenceHigh,
		}}},
		fallback: ingestionTestConfig(512),
	})

	err := session.generateCandidates(ingestionDocumentEvidence{
		BoundaryPriorities: []string{"faq_pair"}, DominantStructures: []string{"faq"},
	})

	require.ErrorContains(t, err, "tokenizer unavailable")
	require.Empty(t, session.candidateSnapshot())
	require.False(t, session.fallbackReady())
}

func TestBoundaryScoreUsesEvidencePriority(t *testing.T) {
	document := chunker.SemanticDocument{ContentLength: 20, Blocks: []chunker.SemanticBlock{
		{Kind: chunker.SemanticKindParagraph, Start: 0, End: 10},
		{Kind: chunker.SemanticKindRecord, Start: 10, End: 20},
	}}
	weights := ingestionBoundaryWeights(document.Blocks, types.SemanticPackingPolicy{
		StrongBoundaryOrder: []string{"record", "paragraph"},
	})

	require.Equal(t, 1, weights[10])
	require.Equal(t, 2, weights[20])
}
