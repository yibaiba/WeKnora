package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type webSearchRAGSpy struct {
	compressCalls int
	compressErr   error
}

func (s *webSearchRAGSpy) Search(
	context.Context,
	string,
	*types.WebSearchConfig,
	string,
) ([]*types.WebSearchResult, error) {
	return []*types.WebSearchResult{{
		Title: "raw", URL: "https://example.com", Content: "raw content",
	}}, nil
}

func (s *webSearchRAGSpy) CompressWithRAG(
	_ context.Context,
	_ string,
	_ string,
	_ []string,
	_ []*types.WebSearchResult,
	_ *types.WebSearchConfig,
	_ interfaces.WebSearchTemporaryKnowledgeBaseService,
	_ interfaces.WebSearchTemporaryKnowledgeService,
	_ map[string]bool,
	_ []string,
) ([]*types.WebSearchResult, string, map[string]bool, []string, error) {
	s.compressCalls++
	if s.compressErr != nil {
		return nil, "", nil, nil, s.compressErr
	}
	return []*types.WebSearchResult{{
		Title: "compressed", URL: "https://example.com", Content: "compressed content",
	}}, "temp-kb", map[string]bool{"https://example.com": true}, []string{"temp-doc"}, nil
}

func TestWebSearchToolStrictCompressionExposesFailure(t *testing.T) {
	search := &webSearchRAGSpy{compressErr: context.DeadlineExceeded}
	tool := NewWebSearchTool(WebSearchToolOptions{
		WebSearchService: search, KnowledgeBaseService: &webSearchTemporaryKnowledgeBaseStub{},
		KnowledgeService:      &webSearchTemporaryKnowledgeStub{},
		WebSearchStateService: &webSearchStateSpy{},
		SessionID:             "ingestion-doc-1", MaxResults: 5, ProviderID: "provider-1",
		StrictCompression: true,
	})
	tenant := &types.Tenant{ID: 7, WebSearchConfig: &types.WebSearchConfig{
		CompressionMethod: "rag", EmbeddingModelID: "embedding-1",
	}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"chunking guidance"}`))

	require.ErrorContains(t, err, "web search RAG compression failed")
	require.False(t, result.Success)
	require.NotContains(t, result.Error, "raw content")
}

func TestWebSearchToolCompressionFailureKeepsChatFallback(t *testing.T) {
	search := &webSearchRAGSpy{compressErr: context.DeadlineExceeded}
	tool := NewWebSearchTool(WebSearchToolOptions{
		WebSearchService: search, KnowledgeBaseService: &webSearchTemporaryKnowledgeBaseStub{},
		KnowledgeService:      &webSearchTemporaryKnowledgeStub{},
		WebSearchStateService: &webSearchStateSpy{},
		SessionID:             "chat-session", MaxResults: 5, ProviderID: "provider-1",
	})
	tenant := &types.Tenant{ID: 7, WebSearchConfig: &types.WebSearchConfig{
		CompressionMethod: "rag", EmbeddingModelID: "embedding-1",
	}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"chunking guidance"}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, "raw content")
}

type webSearchStateSpy struct {
	savedKBID string
}

func (s *webSearchStateSpy) GetWebSearchTempKBState(
	context.Context,
	string,
) (string, map[string]bool, []string) {
	return "", map[string]bool{}, nil
}

func (s *webSearchStateSpy) SaveWebSearchTempKBState(
	_ context.Context,
	_ string,
	tempKBID string,
	_ map[string]bool,
	_ []string,
) {
	s.savedKBID = tempKBID
}

func (s *webSearchStateSpy) DeleteWebSearchTempKBState(context.Context, string) error {
	return nil
}

type webSearchTemporaryKnowledgeBaseStub struct {
	interfaces.WebSearchTemporaryKnowledgeBaseService
}

type webSearchTemporaryKnowledgeStub struct {
	interfaces.WebSearchTemporaryKnowledgeService
}

func TestWebSearchToolAppliesRAGCompressionWithScopedServices(t *testing.T) {
	search := &webSearchRAGSpy{}
	state := &webSearchStateSpy{}
	tool := NewWebSearchTool(WebSearchToolOptions{
		WebSearchService: search, KnowledgeBaseService: &webSearchTemporaryKnowledgeBaseStub{},
		KnowledgeService: &webSearchTemporaryKnowledgeStub{}, WebSearchStateService: state,
		SessionID: "ingestion-doc-1", MaxResults: 5, ProviderID: "provider-1",
	})
	tenant := &types.Tenant{ID: 7, WebSearchConfig: &types.WebSearchConfig{
		CompressionMethod: "rag", EmbeddingModelID: "embedding-1",
	}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"chunking guidance"}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, search.compressCalls)
	require.Equal(t, "temp-kb", state.savedKBID)
	require.Contains(t, result.Output, "compressed content")
	require.NotContains(t, result.Output, "raw content")
}
