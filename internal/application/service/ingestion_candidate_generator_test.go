package service

import (
	"errors"
	"strings"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/models/chat"
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
			ContextKinds: []string{"question", "answer"},
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
			ContextKinds: []string{"question", "answer"},
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

func TestAgentQueryCarriesDefaultBackendCandidateWithoutSourceText(t *testing.T) {
	request := validIngestionAdvisorRequest()
	request.FallbackChunking = ingestionTestConfig(512)
	session := newIngestionAgentSessionFromRequest(request)
	evidence := ingestionDocumentEvidence{
		Summary: "aggregate", DocumentKindCandidates: []string{types.IngestionDocumentKindReport},
		ContentModeCandidates: []string{types.IngestionContentModeDocument},
		DominantStructures:    []string{"section_body"}, BoundaryPriorities: []string{"section"},
	}
	require.NoError(t, session.generateCandidates(evidence))

	query, err := buildIngestionAgentQuery(session, evidence)

	require.NoError(t, err)
	require.NotEmpty(t, session.defaultCandidateID())
	require.Equal(t, session.defaultCandidateID(), defaultCandidateIDFromMessages([]chat.Message{{
		Role: "user", Content: query,
	}}))
	require.NotContains(t, query, request.Content)
	require.NotContains(t, query, "block_descriptions")
}

func TestDecisionToolRegistrationNeverExposesArbitraryPreview(t *testing.T) {
	valid := types.IngestionChunkingCandidate{
		ID: "valid", Config: ingestionTestConfig(300), HardValid: true,
		ComparisonFacts: types.IngestionCandidateComparisonFacts{
			ReferenceCandidateID: "valid", SelectionEligible: true,
		},
	}
	registry := agenttools.NewToolRegistry()
	registerIngestionDecisionTools(registry, newFallbackTestSession(valid))

	require.Contains(t, registry.ListTools(), submitIngestionDecisionTool)
	require.NotContains(t, registry.ListTools(), previewIngestionChunkingTool)
	require.NotContains(t, registry.ListTools(), submitIngestionFallbackTool)

	registry = agenttools.NewToolRegistry()
	registerIngestionDecisionTools(registry, newFallbackTestSession(
		ingestionInvalidCandidate("one", "invalid"),
		ingestionInvalidCandidate("two", "invalid"),
		ingestionInvalidCandidate("three", "invalid"),
	))
	require.Contains(t, registry.ListTools(), submitIngestionFallbackTool)
	require.NotContains(t, registry.ListTools(), previewIngestionChunkingTool)
}

func TestNearScoreCandidateStillRequiresEvidenceDimensionAdvantage(t *testing.T) {
	candidates := []types.IngestionChunkingCandidate{
		{ID: "highest", HardValid: true, Score: types.IngestionCandidateScore{
			Total: 90, BoundaryQuality: 15,
		}},
		{ID: "near", HardValid: true, Score: types.IngestionCandidateScore{
			Total: 87, BoundaryQuality: 18,
		}},
	}

	attachIngestionComparisonFacts(candidates, []string{"boundary_quality"})

	require.False(t, candidates[1].ComparisonFacts.SelectionEligible)
	require.Equal(t, []string{"no_evidence_dimension_advantage"}, candidates[1].ComparisonFacts.ReasonCodes)
}
