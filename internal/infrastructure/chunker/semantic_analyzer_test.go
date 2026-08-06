package chunker

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeSemanticDocumentRecognizesMarkdownStructures(t *testing.T) {
	content := strings.Join([]string{
		"封面说明。", "", "# 使用指南", "完整段落保留在同一个结构块。", "", "- 第一项", "  延续说明", "- 第二项", "",
		"Q: 如何使用？", "A: 按照步骤执行。", "", "| 名称 | 状态 |", "| --- | --- |", "| A | 正常 |", "| B | 异常 |", "",
		"```go", "fmt.Println(\"ok\")", "```", "", "![结果图](images/result.png)", "图 1 结果概览", "", "\f第 2 页", "",
		"负责人：张三", "状态：完成", "",
	}, "\n")

	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})

	require.NoError(t, err)
	require.NoError(t, ValidateSemanticDocument(document))
	requireSemanticSourceSlices(t, content, document.Blocks)
	kinds := semanticKinds(document.Blocks)
	for _, kind := range []string{
		SemanticKindPreamble, SemanticKindHeading, SemanticKindParagraph,
		SemanticKindListItem, SemanticKindFAQ, SemanticKindTableHeader,
		SemanticKindTableRow, SemanticKindCodeBlock, SemanticKindImage,
		SemanticKindPageRegion, SemanticKindRecord,
	} {
		require.Contains(t, kinds, kind)
	}
	require.Equal(t, 2, countSemanticKind(document.Blocks, SemanticKindTableRow))
	require.NotEmpty(t, firstSemanticKind(document.Blocks, SemanticKindParagraph).ParentID)
	require.False(t, firstSemanticKind(document.Blocks, SemanticKindRecord).Atomic)
}

func TestAnalyzeSemanticDocumentRecognizesHTMLTableRows(t *testing.T) {
	content := "<h1>Report</h1>\n<table><tr><th>Name</th><th>State</th></tr>\n" +
		"<tr><td>A</td><td>OK</td></tr></table>\n"

	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})

	require.NoError(t, err)
	require.NoError(t, ValidateSemanticDocument(document))
	require.Equal(t, 1, countSemanticKind(document.Blocks, SemanticKindTableHeader))
	require.Equal(t, 1, countSemanticKind(document.Blocks, SemanticKindTableRow))
	header := firstSemanticKind(document.Blocks, SemanticKindTableHeader)
	row := firstSemanticKind(document.Blocks, SemanticKindTableRow)
	require.Equal(t, header.TableID, row.TableID)
	requireSemanticSourceSlices(t, content, document.Blocks)
}

func TestAnalyzeSemanticDocumentRelocatesAndCompletesPartialHints(t *testing.T) {
	source := "# Title\n![plot](old.png)\nBody text.\n"
	final := "# Title\n![plot](stored/long-name.png)\nBody text.\n"
	bodyStart := utf8.RuneCountInString("# Title\n![plot](old.png)\n")
	document, err := AnalyzeSemanticDocument(final, SemanticAnalysisOptions{
		HintSource: source,
		Hints: []SemanticBlockHint{
			{ID: "heading", Kind: SemanticKindHeading, Start: 0, End: 8, SectionDepth: 1, Atomic: true},
			{ID: "image", Kind: SemanticKindImage, Start: 8, End: bodyStart, Atomic: true},
			{ID: "body", Kind: SemanticKindParagraph, Start: bodyStart, End: len([]rune(source)), ParentID: "heading", Atomic: true},
		},
	})

	require.NoError(t, err)
	require.Equal(t, 3, document.Diagnostics.HintsProvided)
	require.Equal(t, 2, document.Diagnostics.HintsAccepted)
	require.Equal(t, 1, document.Diagnostics.HintsRejected)
	require.Contains(t, document.Diagnostics.ReasonCodes, "hint_source_unmatched")
	require.NoError(t, ValidateSemanticDocument(document))
	requireSemanticSourceSlices(t, final, document.Blocks)
	body := semanticBlockContaining(document.Blocks, "Body text.", final)
	require.NotEmpty(t, body.ParentID)
}

func TestAnalyzeSemanticDocumentRejectsInvalidHintsWithoutContentDiagnostics(t *testing.T) {
	secret := "customer-secret-value"
	document, err := AnalyzeSemanticDocument(secret, SemanticAnalysisOptions{
		Hints: []SemanticBlockHint{{Kind: "unknown-kind", Start: 0, End: len([]rune(secret))}},
	})

	require.NoError(t, err)
	require.Equal(t, 1, document.Diagnostics.HintsRejected)
	require.NotContains(t, strings.Join(document.Diagnostics.ReasonCodes, " "), secret)
	require.NoError(t, ValidateSemanticDocument(document))
}

func TestAnalyzeSemanticDocumentKeepsUnstructuredOCRCovered(t *testing.T) {
	content := "I0O0 l1l1 noisy OCR text without headings\ncontinued line with artifacts ### not heading\n"
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})

	require.NoError(t, err)
	require.NoError(t, ValidateSemanticDocument(document))
	requireSemanticSourceSlices(t, content, document.Blocks)
	require.Contains(t, semanticKinds(document.Blocks), SemanticKindParagraph)
}

func requireSemanticSourceSlices(t *testing.T, content string, blocks []SemanticBlock) {
	t.Helper()
	runes := []rune(content)
	for _, block := range blocks {
		require.Equal(t, block.End-block.Start, utf8.RuneCountInString(string(runes[block.Start:block.End])))
	}
}

func semanticKinds(blocks []SemanticBlock) []string {
	result := make([]string, len(blocks))
	for index, block := range blocks {
		result[index] = block.Kind
	}
	return result
}

func countSemanticKind(blocks []SemanticBlock, kind string) int {
	count := 0
	for _, block := range blocks {
		if block.Kind == kind {
			count++
		}
	}
	return count
}

func firstSemanticKind(blocks []SemanticBlock, kind string) SemanticBlock {
	for _, block := range blocks {
		if block.Kind == kind {
			return block
		}
	}
	return SemanticBlock{}
}

func semanticBlockContaining(blocks []SemanticBlock, needle, content string) SemanticBlock {
	runes := []rune(content)
	for _, block := range blocks {
		if strings.Contains(string(runes[block.Start:block.End]), needle) {
			return block
		}
	}
	return SemanticBlock{}
}
