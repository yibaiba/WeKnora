package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type webSearchRAGKnowledgeBaseStub struct {
	interfaces.WebSearchTemporaryKnowledgeBaseService
	kb     *types.KnowledgeBase
	getErr error
}

func (s *webSearchRAGKnowledgeBaseStub) GetKnowledgeBaseByID(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.kb, nil
}

type webSearchRAGKnowledgeStub struct {
	interfaces.WebSearchTemporaryKnowledgeService
}

func TestWebSearchRAGRefusesToReuseNonTemporaryKnowledgeBase(t *testing.T) {
	service := &WebSearchService{}
	kb := &webSearchRAGKnowledgeBaseStub{kb: &types.KnowledgeBase{
		ID: "production-kb", Name: "production", IsTemporary: false,
	}}

	_, _, _, _, err := service.CompressWithRAG(
		context.Background(), "session", "production-kb", []string{"query"},
		[]*types.WebSearchResult{{URL: "https://example.com"}},
		&types.WebSearchConfig{EmbeddingModelID: "embedding-1"},
		kb, &webSearchRAGKnowledgeStub{}, nil, nil,
	)

	require.ErrorContains(t, err, "is not a Web search temporary knowledge base")
}

func TestWebSearchRAGDoesNotReplaceTemporaryKnowledgeBaseOnLoadFailure(t *testing.T) {
	service := &WebSearchService{}
	kb := &webSearchRAGKnowledgeBaseStub{getErr: errors.New("database unavailable")}
	seen := map[string]bool{"https://old.example": true}
	ids := []string{"old-doc"}

	_, kbID, returnedSeen, returnedIDs, err := service.CompressWithRAG(
		context.Background(), "session", "temp-kb", []string{"query"},
		[]*types.WebSearchResult{{URL: "https://example.com"}},
		&types.WebSearchConfig{EmbeddingModelID: "embedding-1"},
		kb, &webSearchRAGKnowledgeStub{}, seen, ids,
	)

	require.ErrorContains(t, err, "failed to load Web search temporary knowledge base")
	require.Equal(t, "temp-kb", kbID)
	require.Equal(t, seen, returnedSeen)
	require.Equal(t, ids, returnedIDs)
}
