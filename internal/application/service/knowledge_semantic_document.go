package service

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

type finalSemanticDocumentRequest struct {
	finalMarkdown  string
	parserMarkdown string
	structure      []types.DocumentStructureBlock
}

func analyzeFinalSemanticDocument(
	request finalSemanticDocumentRequest,
) (chunker.SemanticDocument, error) {
	document, err := chunker.AnalyzeSemanticDocument(
		request.finalMarkdown,
		chunker.SemanticAnalysisOptions{
			HintSource: request.parserMarkdown,
			Hints:      ingestionSemanticHints(request.structure),
		},
	)
	if err != nil {
		return chunker.SemanticDocument{}, fmt.Errorf("分析最终 Markdown 结构失败: %w", err)
	}
	return document, nil
}

func ingestionSemanticHints(
	blocks []types.DocumentStructureBlock,
) []chunker.SemanticBlockHint {
	result := make([]chunker.SemanticBlockHint, len(blocks))
	for index, block := range blocks {
		result[index] = chunker.SemanticBlockHint{
			ID: block.ID, Kind: block.Kind, Start: block.Start, End: block.End,
			ParentID: block.ParentID, SectionDepth: block.SectionDepth,
			TableID: block.TableID, RecordID: block.RecordID,
			Atomic: block.Atomic, Confidence: block.Confidence,
			ContextKinds: append([]string(nil), block.ContextKinds...),
		}
	}
	return result
}
