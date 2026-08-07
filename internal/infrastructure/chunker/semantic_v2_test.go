package chunker

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSemanticGoldmarkRecognizesExtendedGFMStructures(t *testing.T) {
	content := strings.Join([]string{
		"Guide", "=====", "", "- outer item", "  continuation", "  - nested item", "    nested continuation",
		"- second item", "", "> Q: quoted question?", "> A: quoted answer.", "> continued answer", "",
		"| Path | State |", "| --- | --- |", `| a\|b | ok |`, "",
	}, "\n")

	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})

	require.NoError(t, err)
	require.NoError(t, ValidateSemanticDocument(document))
	requireSemanticSourceSlices(t, content, document.Blocks)
	heading := firstSemanticKind(document.Blocks, SemanticKindHeading)
	require.Equal(t, 1, heading.SectionDepth)
	require.Contains(t, string([]rune(content)[heading.Start:heading.End]), "=====")
	require.Equal(t, 2, countSemanticKind(document.Blocks, SemanticKindListItem))
	firstItem := firstSemanticKind(document.Blocks, SemanticKindListItem)
	require.Contains(t, string([]rune(content)[firstItem.Start:firstItem.End]), "nested continuation")
	faq := firstSemanticKind(document.Blocks, SemanticKindFAQ)
	require.Contains(t, string([]rune(content)[faq.Start:faq.End]), "> continued answer")
	require.Equal(t, 1, countSemanticKind(document.Blocks, SemanticKindTableHeader))
	require.Equal(t, 1, countSemanticKind(document.Blocks, SemanticKindTableRow))
}

func TestSemanticGoldmarkIncludesMultilineSetextAndLongFenceMarkers(t *testing.T) {
	content := "first heading line\nsecond heading line\n---\n\n````go\n``` is content\n````\n"

	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})

	require.NoError(t, err)
	heading := firstSemanticKind(document.Blocks, SemanticKindHeading)
	require.Equal(t, 2, heading.SectionDepth)
	require.Contains(t, string([]rune(content)[heading.Start:heading.End]), "---")
	code := firstSemanticKind(document.Blocks, SemanticKindCodeBlock)
	require.Contains(t, string([]rune(content)[code.Start:code.End]), "``` is content")
	require.Contains(t, string([]rune(content)[code.Start:code.End]), "````\n")
}

func TestSemanticLocalAnalysisStartsNewRecordAtRepeatedKey(t *testing.T) {
	content := "Name: Alpha\nState: Ready\nName: Beta\nState: Blocked\n"

	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})

	require.NoError(t, err)
	require.Equal(t, 2, countSemanticKind(document.Blocks, SemanticKindRecord))
	records := make([]SemanticBlock, 0, 2)
	for _, block := range document.Blocks {
		if block.Kind == SemanticKindRecord {
			records = append(records, block)
		}
	}
	require.NotEqual(t, records[0].RecordID, records[1].RecordID)
	require.Contains(t, string([]rune(content)[records[1].Start:records[1].End]), "Beta")
}

func TestSemanticLocalAnalysisMarksRepeatedPageRegionsWithoutDeletingContent(t *testing.T) {
	content := strings.Join([]string{
		"Report Header", "first body", "Report Footer", "\f",
		"Report Header", "second body", "Report Footer", "\f",
		"Report Header", "third body", "Report Footer", "",
	}, "\n")

	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{})

	require.NoError(t, err)
	require.NoError(t, ValidateSemanticDocument(document))
	require.GreaterOrEqual(t, countSemanticKind(document.Blocks, SemanticKindPageRegion), 8)
	require.Equal(t, len([]rune(content)), document.Blocks[len(document.Blocks)-1].End)
	requireSemanticSourceSlices(t, content, document.Blocks)
}

func TestSemanticHintRelocationAcceptsOnlyUniqueNormalizedMatch(t *testing.T) {
	source := "Body   text.\n"
	final := "Body \n text.\n"
	document, err := AnalyzeSemanticDocument(final, SemanticAnalysisOptions{
		HintSource: source,
		Hints: []SemanticBlockHint{{
			ID: "body", Kind: SemanticKindParagraph, Start: 0, End: len([]rune(source)), Atomic: true,
		}},
	})

	require.NoError(t, err)
	require.Equal(t, 1, document.Diagnostics.HintsAccepted)
	require.Zero(t, document.Diagnostics.HintsRejected)

	ambiguousSource := "alpha   beta"
	ambiguousFinal := "alpha beta\nalpha\tbeta"
	ambiguous, err := AnalyzeSemanticDocument(ambiguousFinal, SemanticAnalysisOptions{
		HintSource: ambiguousSource,
		Hints: []SemanticBlockHint{{
			Kind: SemanticKindParagraph, Start: 0, End: utf8.RuneCountInString(ambiguousSource),
		}},
	})
	require.NoError(t, err)
	require.Zero(t, ambiguous.Diagnostics.HintsAccepted)
	require.Contains(t, ambiguous.Diagnostics.ReasonCodes, "hint_normalized_ambiguous")
}

func TestSemanticHintsRejectInvalidRelationshipsPerBlock(t *testing.T) {
	content := "parent\nchild\n"
	document, err := AnalyzeSemanticDocument(content, SemanticAnalysisOptions{
		Hints: []SemanticBlockHint{
			{ID: "parent", Kind: SemanticKindParagraph, Start: 0, End: 7},
			{ID: "child", Kind: SemanticKindParagraph, Start: 7, End: len([]rune(content)), ParentID: "parent"},
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, document.Diagnostics.HintsAccepted)
	require.Equal(t, 1, document.Diagnostics.HintsRejected)
	require.Contains(t, document.Diagnostics.ReasonCodes, "hint_parent_not_heading")
	require.NoError(t, ValidateSemanticDocument(document))
}

func TestSemanticHintsRejectDuplicateForwardAndTypedMetadata(t *testing.T) {
	tests := []struct {
		name    string
		content string
		hints   []SemanticBlockHint
		reason  string
	}{
		{
			name: "duplicate id", content: "# H\nbody\n",
			hints: []SemanticBlockHint{
				{ID: "same", Kind: SemanticKindHeading, Start: 0, End: 4, SectionDepth: 1},
				{ID: "same", Kind: SemanticKindParagraph, Start: 4, End: 9},
			},
			reason: "hint_id_duplicate",
		},
		{
			name: "forward parent", content: "body\n# H\n",
			hints: []SemanticBlockHint{
				{ID: "body", Kind: SemanticKindParagraph, Start: 0, End: 5, ParentID: "heading"},
				{ID: "heading", Kind: SemanticKindHeading, Start: 5, End: 9, SectionDepth: 1},
			},
			reason: "hint_parent_missing_or_forward",
		},
		{
			name: "table id", content: "plain body\n",
			hints: []SemanticBlockHint{{
				ID: "row", Kind: SemanticKindTableRow, Start: 0, End: 11,
			}},
			reason: "hint_table_relation_invalid",
		},
		{
			name: "context kind", content: "Q: why?\nA: because.\n",
			hints: []SemanticBlockHint{{
				ID: "faq", Kind: SemanticKindFAQ, Start: 0, End: 20,
				ContextKinds: []string{"caption"},
			}},
			reason: "hint_context_kinds_invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := AnalyzeSemanticDocument(test.content, SemanticAnalysisOptions{Hints: test.hints})
			require.NoError(t, err)
			require.Contains(t, document.Diagnostics.ReasonCodes, test.reason)
			require.Greater(t, document.Diagnostics.HintsRejected, 0)
			require.NoError(t, ValidateSemanticDocument(document))
			requireSemanticSourceSlices(t, test.content, document.Blocks)
		})
	}
}

func TestValidateSemanticDocumentRejectsInvalidRelations(t *testing.T) {
	tests := []struct {
		name   string
		blocks []SemanticBlock
		match  string
	}{
		{"duplicate id", []SemanticBlock{paragraphBlock("same", 0, 1), paragraphBlock("same", 1, 2)}, "duplicates id"},
		{"forward parent", []SemanticBlock{
			{ID: "child", Kind: SemanticKindParagraph, Start: 0, End: 1, ParentID: "heading"},
			{ID: "heading", Kind: SemanticKindHeading, Start: 1, End: 2, SectionDepth: 1},
		}, "missing or forward parent"},
		{"cycle", []SemanticBlock{{
			ID: "self", Kind: SemanticKindHeading, Start: 0, End: 2,
			ParentID: "self", SectionDepth: 1,
		}}, "missing or forward parent"},
		{"non heading parent", []SemanticBlock{
			paragraphBlock("parent", 0, 1),
			{ID: "child", Kind: SemanticKindParagraph, Start: 1, End: 2, ParentID: "parent"},
		}, "is not a heading"},
		{"invalid depth", []SemanticBlock{{
			ID: "heading", Kind: SemanticKindHeading, Start: 0, End: 2, SectionDepth: 7,
		}}, "invalid depth"},
		{"duplicate header", []SemanticBlock{
			tableBlock("header-1", SemanticKindTableHeader, "table", 0, 1),
			tableBlock("header-2", SemanticKindTableHeader, "table", 1, 2),
		}, "invalid header"},
		{"missing header", []SemanticBlock{
			tableBlock("row", SemanticKindTableRow, "table", 0, 2),
		}, "has no header"},
		{"wrong table relation", []SemanticBlock{{
			ID: "paragraph", Kind: SemanticKindParagraph, Start: 0, End: 2, TableID: "table",
		}}, "inconsistent table id"},
		{"duplicate record relation", []SemanticBlock{
			recordBlock("record-1", "record", 0, 1), recordBlock("record-2", "record", 1, 2),
		}, "duplicates record id"},
		{"wrong context kind", []SemanticBlock{{
			ID: "faq", Kind: SemanticKindFAQ, Start: 0, End: 2,
			ContextKinds: []string{"question"},
		}}, "invalid context kinds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSemanticDocument(SemanticDocument{ContentLength: 2, Blocks: test.blocks})
			require.ErrorContains(t, err, test.match)
		})
	}
}

func paragraphBlock(id string, start, end int) SemanticBlock {
	return SemanticBlock{ID: id, Kind: SemanticKindParagraph, Start: start, End: end}
}

func tableBlock(id, kind, tableID string, start, end int) SemanticBlock {
	return SemanticBlock{ID: id, Kind: kind, Start: start, End: end, TableID: tableID}
}

func recordBlock(id, recordID string, start, end int) SemanticBlock {
	return SemanticBlock{
		ID: id, Kind: SemanticKindRecord, Start: start, End: end,
		RecordID: recordID, ContextKinds: []string{"record"},
	}
}
