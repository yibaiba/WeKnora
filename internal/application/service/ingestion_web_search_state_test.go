package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type ingestionWebSearchKBStub struct {
	interfaces.WebSearchTemporaryKnowledgeBaseService
	kb      *types.KnowledgeBase
	deleted []string
}

func (s *ingestionWebSearchKBStub) GetKnowledgeBaseByID(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *ingestionWebSearchKBStub) DeleteKnowledgeBase(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

type ingestionWebSearchKnowledgeStub struct {
	interfaces.WebSearchTemporaryKnowledgeService
	deleted []string
}

func (s *ingestionWebSearchKnowledgeStub) DeleteKnowledge(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func TestIngestionWebSearchStateCopiesAndCleansTrackedTemporaryData(t *testing.T) {
	kb := &ingestionWebSearchKBStub{kb: &types.KnowledgeBase{
		ID: "temp-kb", Name: webSearchTemporaryKBNamePrefix + "1", IsTemporary: true,
	}}
	knowledge := &ingestionWebSearchKnowledgeStub{}
	state := newIngestionWebSearchState(kb, knowledge)
	seen := map[string]bool{"https://example.com": true}
	ids := []string{"doc-1", "doc-2"}
	state.SaveWebSearchTempKBState(context.Background(), "session", "temp-kb", seen, ids)
	seen["https://mutated.example"] = true
	ids[0] = "mutated"

	_, storedSeen, storedIDs := state.GetWebSearchTempKBState(context.Background(), "session")
	require.NotContains(t, storedSeen, "https://mutated.example")
	require.Equal(t, []string{"doc-1", "doc-2"}, storedIDs)
	require.NoError(t, state.DeleteWebSearchTempKBState(context.Background(), "session"))
	require.Equal(t, []string{"doc-1", "doc-2"}, knowledge.deleted)
	require.Equal(t, []string{"temp-kb"}, kb.deleted)

	tempKBID, storedSeen, storedIDs := state.GetWebSearchTempKBState(context.Background(), "session")
	require.Empty(t, tempKBID)
	require.Empty(t, storedSeen)
	require.Empty(t, storedIDs)
}

func TestIngestionWebSearchStateRefusesToDeleteNonWebKnowledgeBase(t *testing.T) {
	kb := &ingestionWebSearchKBStub{kb: &types.KnowledgeBase{
		ID: "production-kb", Name: "production", IsTemporary: false,
	}}
	knowledge := &ingestionWebSearchKnowledgeStub{}
	state := newIngestionWebSearchState(kb, knowledge)
	state.SaveWebSearchTempKBState(
		context.Background(), "session", "production-kb", nil, []string{"doc-1"},
	)

	err := state.DeleteWebSearchTempKBState(context.Background(), "session")

	require.ErrorContains(t, err, "拒绝清理非 Web 搜索临时知识库")
	require.Empty(t, knowledge.deleted)
	require.Empty(t, kb.deleted)
}
