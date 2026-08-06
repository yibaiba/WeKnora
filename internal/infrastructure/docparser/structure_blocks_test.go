package docparser

import (
	"testing"

	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/stretchr/testify/require"
)

func TestHTTPStructureBlocksAreOptionalAndCopied(t *testing.T) {
	empty := fromHTTPReadResponse(&httpReadResponse{MarkdownContent: "plain"})
	require.Empty(t, empty.StructureBlocks)

	response := fromHTTPReadResponse(&httpReadResponse{
		MarkdownContent: "# Heading\nBody",
		StructureBlocks: []httpStructureBlock{{
			ID: "heading-1", Kind: "heading", Start: 0, End: 9, SectionDepth: 1,
			Atomic: true, Confidence: "high", ContextKinds: []string{"section"},
		}},
	})
	require.Len(t, response.StructureBlocks, 1)
	require.Equal(t, "heading", response.StructureBlocks[0].Kind)
	require.Equal(t, "heading-1", response.StructureBlocks[0].ID)
	require.Equal(t, []string{"section"}, response.StructureBlocks[0].ContextKinds)
}

func TestProtoStructureBlocksMapAllFields(t *testing.T) {
	blocks := fromProtoStructureBlocks([]*proto.StructureBlock{nil, {
		Id: "row-1", Kind: "table_row", Start: 10, End: 20, ParentId: "parent-1",
		SectionDepth: 2, TableId: "table-1", RecordId: "record-1",
		Atomic: true, Confidence: "high", ContextKinds: []string{"table_header"},
	}})
	require.Len(t, blocks, 1)
	require.Equal(t, 10, blocks[0].Start)
	require.Equal(t, "row-1", blocks[0].ID)
	require.Equal(t, "table-1", blocks[0].TableID)
	require.Equal(t, []string{"table_header"}, blocks[0].ContextKinds)
}
