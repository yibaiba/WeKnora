package service

import (
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
)

// buildKnowledgeIndexContent adds only the document title to searchable text.
// Custom metadata stays document-scoped: it is supplied once to the answer
// model and summary model, rather than repeated in every chunk embedding.
func buildKnowledgeIndexContent(knowledge *types.Knowledge, content string) string {
	if knowledge == nil {
		return content
	}
	return chunker.PrependEmbeddingPrefix(knowledge.Title, content)
}
