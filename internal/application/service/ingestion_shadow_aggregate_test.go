package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAggregateShadowDiagnosticsBySourceFormatAndKind(t *testing.T) {
	rows := AggregateShadowDiagnostics([]types.IngestionSemanticDiagnostics{
		{
			SourceFormat: "pdf", HintAcceptanceRate: 0.50, HintsProvided: 2,
			KindBlockCounts: map[string]int{chunker.SemanticKindTableRow: 4, chunker.SemanticKindFAQ: 1},
			KindViolationCounts: map[string]int{
				"table:table_continuation_header_missing": 2,
			},
			StructureViolationCounts: map[string]int{"table_continuation_header_missing": 2},
			FallbackApplied:          true, CandidateCount: 3, ValidCandidateCount: 1,
		},
		{
			SourceFormat: "pdf", HintAcceptanceRate: 1, HintsProvided: 1,
			KindBlockCounts:          map[string]int{chunker.SemanticKindTableRow: 2},
			StructureViolationCounts: map[string]int{"atomic_block_split": 1},
			CandidateCount:           3, ValidCandidateCount: 3,
		},
		{
			SourceFormat:    "docx",
			KindBlockCounts: map[string]int{chunker.SemanticKindFAQ: 3},
			CandidateCount:  3, ValidCandidateCount: 3,
		},
	})

	require.GreaterOrEqual(t, len(rows), 4)
	pdfTable := shadowRow(t, rows, "pdf", chunker.SemanticKindTableRow)
	require.Equal(t, 2, pdfTable.Documents)
	require.Equal(t, 2, pdfTable.KindViolationCounts["table:table_continuation_header_missing"])
	require.Equal(t, 1, pdfTable.FallbackCount)
	require.InDelta(t, 0.75, pdfTable.HintAcceptanceRate(), 0.000001)

	pdfAll := shadowRow(t, rows, "pdf", "all")
	require.Equal(t, 2, pdfAll.Documents)
	require.Equal(t, 2, pdfAll.StructureViolationCounts["table_continuation_header_missing"])

	docxFAQ := shadowRow(t, rows, "docx", chunker.SemanticKindFAQ)
	require.Equal(t, 1, docxFAQ.Documents)
	require.Zero(t, docxFAQ.FallbackCount)
}

func shadowRow(t *testing.T, rows []ShadowKindAggregate, format, kind string) ShadowKindAggregate {
	t.Helper()
	for _, row := range rows {
		if row.SourceFormat == format && row.Kind == kind {
			return row
		}
	}
	require.FailNowf(t, "missing aggregate", "format=%s kind=%s", format, kind)
	return ShadowKindAggregate{}
}
