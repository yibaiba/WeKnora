package chunker

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

type semanticFormatFixture struct {
	Contracts []semanticFormatContract `json:"contracts"`
}

type semanticFormatContract struct {
	Format                string                    `json:"format"`
	Markdown              string                    `json:"markdown"`
	HintSource            string                    `json:"hint_source"`
	StructureBlocks       []semanticFormatStructure `json:"structure_blocks"`
	ExpectedKinds         []string                  `json:"expected_kinds"`
	ExpectedHintsAccepted int                       `json:"expected_hints_accepted"`
	ExpectedHintsRejected int                       `json:"expected_hints_rejected"`
	ReportRegression      bool                      `json:"report_regression"`
}

type semanticFormatStructure struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Needle       string   `json:"needle"`
	ParentID     string   `json:"parent_id"`
	SectionDepth int      `json:"section_depth"`
	TableID      string   `json:"table_id"`
	RecordID     string   `json:"record_id"`
	Atomic       bool     `json:"atomic"`
	Confidence   string   `json:"confidence"`
	ContextKinds []string `json:"context_kinds"`
}

func TestSemanticFormatContractsFuseOptionalStructure(t *testing.T) {
	for _, contract := range loadSemanticFormatContracts(t) {
		contract := contract
		t.Run(contract.Format, func(t *testing.T) {
			document, err := AnalyzeSemanticDocument(contract.Markdown, SemanticAnalysisOptions{
				HintSource: semanticContractHintSource(contract),
				Hints:      semanticContractHints(t, contract),
			})

			require.NoError(t, err)
			require.NoError(t, ValidateSemanticDocument(document))
			requireSemanticSourceSlices(t, contract.Markdown, document.Blocks)
			require.Equal(t, contract.ExpectedHintsAccepted, document.Diagnostics.HintsAccepted)
			require.Equal(t, contract.ExpectedHintsRejected, document.Diagnostics.HintsRejected)
			for _, kind := range contract.ExpectedKinds {
				require.Contains(t, semanticKinds(document.Blocks), kind)
			}
		})
	}
}

func TestSemanticReportRegressionKeepsContentsSectionsAndTablesSeparate(t *testing.T) {
	contract := semanticReportContract(t, loadSemanticFormatContracts(t))
	document, err := AnalyzeSemanticDocument(contract.Markdown, SemanticAnalysisOptions{
		HintSource: semanticContractHintSource(contract), Hints: semanticContractHints(t, contract),
	})
	require.NoError(t, err)

	chunks, err := SplitSemanticDocument(contract.Markdown, SplitterConfig{
		Strategy: StrategyAuto, ChunkSize: 120, ChunkOverlap: 0, AllowZeroOverlap: true,
	}, document)

	require.NoError(t, err)
	requireChunksRestoreSource(t, contract.Markdown, chunks)
	requireReportSectionIsolation(t, chunks)
	requireReportTableContinuations(t, chunks)
}

func loadSemanticFormatContracts(t *testing.T) []semanticFormatContract {
	t.Helper()
	raw, err := os.ReadFile("testdata/semantic_format_contracts.json")
	require.NoError(t, err)
	var fixture semanticFormatFixture
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Len(t, fixture.Contracts, 6)
	return fixture.Contracts
}

func semanticContractHintSource(contract semanticFormatContract) string {
	if contract.HintSource != "" {
		return contract.HintSource
	}
	return contract.Markdown
}

func semanticContractHints(t *testing.T, contract semanticFormatContract) []SemanticBlockHint {
	t.Helper()
	source := semanticContractHintSource(contract)
	hints := make([]SemanticBlockHint, 0, len(contract.StructureBlocks))
	for _, raw := range contract.StructureBlocks {
		start := strings.Index(source, raw.Needle)
		require.GreaterOrEqual(t, start, 0)
		start = utf8.RuneCountInString(source[:start])
		hints = append(hints, SemanticBlockHint{
			ID: raw.ID, Kind: raw.Kind, Start: start, End: start + utf8.RuneCountInString(raw.Needle),
			ParentID: raw.ParentID, SectionDepth: raw.SectionDepth, TableID: raw.TableID,
			RecordID: raw.RecordID, Atomic: raw.Atomic, Confidence: raw.Confidence,
			ContextKinds: append([]string(nil), raw.ContextKinds...),
		})
	}
	return hints
}

func semanticReportContract(t *testing.T, contracts []semanticFormatContract) semanticFormatContract {
	t.Helper()
	for _, contract := range contracts {
		if contract.ReportRegression {
			return contract
		}
	}
	require.FailNow(t, "report regression contract is missing")
	return semanticFormatContract{}
}

func requireReportSectionIsolation(t *testing.T, chunks []Chunk) {
	t.Helper()
	interfaceChunk := chunkContaining(chunks, "TC-LOGIN")
	performanceChunk := chunkContaining(chunks, "PERF-001")
	require.NotContains(t, interfaceChunk.Content, "## Contents")
	require.NotContains(t, interfaceChunk.Content, "1. Overview")
	require.NotContains(t, performanceChunk.Content, "TC-LOGIN")
	for _, chunk := range chunks {
		require.False(t, strings.Contains(chunk.Content, "## Contents") &&
			strings.Contains(chunk.Content, "## Interface Checks"))
	}
}

func requireReportTableContinuations(t *testing.T, chunks []Chunk) {
	t.Helper()
	const header = "| Case | Category | Result |"
	for _, chunk := range chunks {
		if !strings.Contains(chunk.Content, "| TC-") || strings.Contains(chunk.Content, header) {
			continue
		}
		require.Contains(t, chunk.ContextHeader, header)
		require.NotContains(t, chunk.ContextHeader, "TC-LOGIN")
		require.NotContains(t, chunk.ContextHeader, "TC-EXPORT")
	}
}

func chunkContaining(chunks []Chunk, needle string) Chunk {
	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, needle) {
			return chunk
		}
	}
	return Chunk{}
}
