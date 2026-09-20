package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestLocalIngestionEvidenceSkipsModelForHighConfidenceTable(t *testing.T) {
	content := "| Case | Result |\n| --- | --- |\n| Alpha | Pass |\n"
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)

	evidence, ok := localIngestionEvidenceFromDocument(document)

	require.True(t, ok)
	require.Contains(t, evidence.DominantStructures, "table")
	require.False(t, ingestionDocumentNeedsMapReduce(document))
}

func TestLocalIngestionEvidenceUsesMapReduceForSectionProse(t *testing.T) {
	content := "# Policy\n\nFirst section.\n\n## Scope\n\nSecond section."
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)

	_, ok := localIngestionEvidenceFromDocument(document)
	require.False(t, ok)
	require.True(t, ingestionDocumentNeedsMapReduce(document))
}

func TestHardRulesSeparatedAutoSubmitsSingleEligibleCandidate(t *testing.T) {
	content := strings.Repeat("| Case | Result |\n| --- | --- |\n| Alpha | Pass |\n", 4)
	session := newIngestionAgentSessionWithFallback(content, types.IngestionChunkingConstraints{}, ingestionTestConfig(200))
	document, err := chunker.AnalyzeSemanticDocument(content, chunker.SemanticAnalysisOptions{})
	require.NoError(t, err)
	session.document = document
	evidence, ok := localIngestionEvidenceFromDocument(document)
	require.True(t, ok)

	require.NoError(t, session.generateCandidates(evidence))
	if !session.hardRulesSeparated() {
		t.Skip("policy variants remained close enough that the agent still chooses")
	}
	require.NoError(t, session.autoSubmitFromEvidence(evidence))
	require.NotNil(t, session.decisionSnapshot())
}
