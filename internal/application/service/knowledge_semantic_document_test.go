package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeFinalSemanticDocumentRelocatesHintsAfterImageReplacement(t *testing.T) {
	parserMarkdown := "# Report\n![chart](old.png)\nVerified result.\n"
	finalMarkdown := "# Report\n![chart](stored/chart-long-name.png)\nVerified result.\n"
	bodyStart := len([]rune("# Report\n![chart](old.png)\n"))

	document, err := analyzeFinalSemanticDocument(finalSemanticDocumentRequest{
		finalMarkdown: finalMarkdown, parserMarkdown: parserMarkdown,
		structure: []types.DocumentStructureBlock{
			{ID: "heading", Kind: chunker.SemanticKindHeading, Start: 0, End: 9, SectionDepth: 1},
			{ID: "image", Kind: chunker.SemanticKindImage, Start: 9, End: bodyStart, Atomic: true},
			{ID: "body", Kind: chunker.SemanticKindParagraph, Start: bodyStart, End: len([]rune(parserMarkdown))},
		},
	})

	require.NoError(t, err)
	require.Equal(t, len([]rune(finalMarkdown)), document.ContentLength)
	require.Equal(t, 3, document.Diagnostics.HintsProvided)
	require.Equal(t, 2, document.Diagnostics.HintsAccepted)
	require.Equal(t, 1, document.Diagnostics.HintsRejected)
	require.Contains(t, document.Diagnostics.ReasonCodes, "hint_source_unmatched")
	require.NoError(t, chunker.ValidateSemanticDocument(document))
	requireSemanticDocumentCoversSource(t, finalMarkdown, document)
}

func requireSemanticDocumentCoversSource(
	t *testing.T,
	content string,
	document chunker.SemanticDocument,
) {
	t.Helper()
	runes := []rune(content)
	expectedStart := 0
	for _, block := range document.Blocks {
		require.Equal(t, expectedStart, block.Start)
		require.LessOrEqual(t, block.End, len(runes))
		expectedStart = block.End
	}
	require.Equal(t, len(runes), expectedStart)
}
