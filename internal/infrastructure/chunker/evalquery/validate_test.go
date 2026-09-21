package evalquery

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseModelResponseExtractsFencedJSON(t *testing.T) {
	parsed, err := parseModelResponse("here\n```json\n{\"question\":\"Q1\",\"references\":[\" The fact. \"]}\n```\n")
	require.NoError(t, err)
	require.Equal(t, "Q1", parsed.Question)
	require.Equal(t, []string{"The fact."}, parsed.References)
}

func TestFirstExactExcerptRejectsAnyInvalidReference(t *testing.T) {
	markdown := "Alpha sentence lives here. Beta sentence follows."
	excerpt, err := firstExactExcerpt(markdown, []string{"Alpha sentence lives here.", "Beta sentence follows."})
	require.NoError(t, err)
	require.Equal(t, "Alpha sentence lives here.", excerpt)

	_, err = firstExactExcerpt(markdown, []string{"Alpha sentence lives here.", "not in the document"})
	require.Error(t, err)
	_, err = firstExactExcerpt(markdown, nil)
	require.Error(t, err)
	_, err = firstExactExcerpt(markdown, []string{"a", "b", "c", "d", "e", "f"})
	require.Error(t, err)
}

func TestIsCompoundQuestionDoesNotOverRejectChineseHe(t *testing.T) {
	require.True(t, isCompoundQuestion("What is latency and throughput?"))
	require.True(t, isCompoundQuestion("回滚窗口以及审批人是谁？"))
	require.True(t, isCompoundQuestion("还有谁负责归档？"))
	require.False(t, isCompoundQuestion("灾备演练由谁负责？"))
	require.False(t, isCompoundQuestion("How long is the calibration interval?"))
}

func TestIsStructureExcerptDetectsAtomicBlocks(t *testing.T) {
	require.True(t, isStructureExcerpt("| TC-LOGIN | Authentication | Pass |"))
	require.True(t, isStructureExcerpt("intro\n| Case | Result |"))
	require.True(t, isStructureExcerpt("```go\nfunc Retry() {}\n```"))
	require.True(t, isStructureExcerpt("Q: FAQ-ROLLBACK When is rollback required?"))
	require.True(t, isStructureExcerpt("Record ID: PERF-001"))
	require.False(t, isStructureExcerpt("The calibration interval remains thirty days."))
}

func TestUniqueMarkerIsUniqueAndSingleLine(t *testing.T) {
	markdown := "The calibration interval remains thirty days for all regional sensors.\nThe retention schedule keeps audit artifacts for ninety days."
	excerpt := "The calibration interval remains thirty days for all regional sensors."
	marker, err := uniqueMarker(excerpt, markdown, map[string]struct{}{})
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(markdown, marker))
	require.NotContains(t, marker, "\n")
	require.Contains(t, excerpt, marker)
}

func TestUniqueMarkerFailsWhenExcerptIsNotUnique(t *testing.T) {
	_, err := uniqueMarker("alpha", "alpha appears twice. alpha appears twice.", map[string]struct{}{})
	require.Error(t, err)
}

func TestSampleWindowClampsToContiguousRunes(t *testing.T) {
	require.Equal(t, "ghij", sampleWindow("abcdefghij", 4, 8))
	require.Equal(t, "abcd", sampleWindow("abcdefghij", 4, 0))
	require.Equal(t, "abcdefghij", sampleWindow("abcdefghij", 40, 3))
}
