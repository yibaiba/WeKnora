package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WebSearchProvider defines the interface for web search providers
type WebSearchProvider interface {
	// Name returns the name of the provider
	Name() string
	// Search performs a web search
	Search(ctx context.Context, query string, maxResults int, includeDate bool) ([]*types.WebSearchResult, error)
}

// WebSearchTemporaryKnowledgeBaseService is the only knowledge-base surface
// available to Web RAG compression. Implementations must scope these methods
// to the caller's tenant.
type WebSearchTemporaryKnowledgeBaseService interface {
	CreateKnowledgeBase(ctx context.Context, kb *types.KnowledgeBase) (*types.KnowledgeBase, error)
	GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error)
	HybridSearch(ctx context.Context, id string, params types.SearchParams) ([]*types.SearchResult, error)
	DeleteKnowledgeBase(ctx context.Context, id string) error
}

// WebSearchTemporaryKnowledgeService limits Web RAG compression to creating
// and cleaning passages inside its internally tracked temporary knowledge base.
type WebSearchTemporaryKnowledgeService interface {
	CreateKnowledgeFromPassageSync(
		ctx context.Context,
		kbID string,
		passage []string,
		channel string,
	) (*types.Knowledge, error)
	DeleteKnowledge(ctx context.Context, id string) error
}

// WebSearchService defines the interface for web search services
type WebSearchService interface {
	// Search performs a web search using the provider entity identified by providerID.
	// If providerID is empty, it falls back to the deprecated config.Provider field for backward compatibility.
	Search(ctx context.Context, providerID string, config *types.WebSearchConfig, query string) ([]*types.WebSearchResult, error)
	// CompressWithRAG performs RAG-based compression using a temporary, hidden knowledge base
	// The temporary knowledge base is deleted after use. The UI will not list it due to repo filtering.
	CompressWithRAG(ctx context.Context, sessionID string, tempKBID string, questions []string,
		webSearchResults []*types.WebSearchResult, cfg *types.WebSearchConfig,
		kbSvc WebSearchTemporaryKnowledgeBaseService, knowSvc WebSearchTemporaryKnowledgeService,
		seenURLs map[string]bool, knowledgeIDs []string,
	) (compressed []*types.WebSearchResult, kbID string, newSeen map[string]bool, newIDs []string, err error)
}
