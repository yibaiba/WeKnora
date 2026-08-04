package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

var webSearchTool = BaseTool{
	name: ToolWebSearch,
	description: `Search the web for current information and news. This tool searches the internet to find up-to-date information that may not be in the knowledge base.

## CRITICAL - KB First Rule
**ABSOLUTE RULE**: You MUST complete KB retrieval (grep_chunks AND knowledge_search) FIRST before using this tool.
- NEVER use web_search without first trying grep_chunks and knowledge_search
- ONLY use web_search if BOTH grep_chunks AND knowledge_search return insufficient/no results
- KB retrieval is MANDATORY - you CANNOT skip it

## Features
- Real-time web search: Search the internet for current information
- RAG compression: Automatically compresses and extracts relevant content from search results
- Session-scoped caching: Maintains temporary knowledge base for session to avoid re-indexing

## Usage

**Use when**:
- **ONLY after** completing grep_chunks AND knowledge_search
- KB retrieval returned insufficient or no results
- Need current or real-time information (news, events, recent updates)
- Information is not available in knowledge bases
- Need to verify or supplement information from knowledge bases
- Searching for recent developments or trends

**Parameters**:
- query (required): Search query string

**Returns**: Web search results with title, short wN page ID, snippet, and content (up to %d results)

## Examples

` + "`" + `
# Search for current information
{
  "query": "latest developments in AI"
}

# Search for recent news
{
  "query": "Python 3.12 release notes"
}
` + "`" + `

## Evidence and Fallback

- Results are automatically compressed using RAG to extract relevant content
- Search results are stored in a temporary knowledge base for the session
- Titles, URLs, snippets, and content snippets are usable search-summary evidence
- Use web_fetch only when the snippet is insufficient or full-page verification is important
- If web_fetch fails, keep the search evidence, disclose that page content was not verified, and lower confidence for dynamic facts
- Do not repeat equivalent searches merely because a page could not be fetched
- Maximum %d results will be returned per search`,
	schema: utils.GenerateSchema[WebSearchInput](),
}

// WebSearchInput defines the input parameters for web search tool
type WebSearchInput struct {
	Query string `json:"query" jsonschema:"Search query string"`
}

// WebSearchTool performs web searches and returns results
type WebSearchTool struct {
	BaseTool
	webSearchService      interfaces.WebSearchService
	knowledgeBaseService  interfaces.WebSearchTemporaryKnowledgeBaseService
	knowledgeService      interfaces.WebSearchTemporaryKnowledgeService
	webSearchStateService interfaces.WebSearchStateService
	sessionID             string
	maxResults            int
	providerID            string // WebSearchProviderEntity ID (resolved from agent config or tenant default)
	strictCompression     bool
	compressionMu         sync.Mutex
}

type WebSearchToolOptions struct {
	WebSearchService      interfaces.WebSearchService
	KnowledgeBaseService  interfaces.WebSearchTemporaryKnowledgeBaseService
	KnowledgeService      interfaces.WebSearchTemporaryKnowledgeService
	WebSearchStateService interfaces.WebSearchStateService
	SessionID             string
	MaxResults            int
	ProviderID            string
	StrictCompression     bool
}

// NewWebSearchTool creates a new web search tool.
func NewWebSearchTool(options WebSearchToolOptions) *WebSearchTool {
	tool := webSearchTool
	tool.description = fmt.Sprintf(tool.description, options.MaxResults, options.MaxResults)

	return &WebSearchTool{
		BaseTool:              tool,
		webSearchService:      options.WebSearchService,
		knowledgeBaseService:  options.KnowledgeBaseService,
		knowledgeService:      options.KnowledgeService,
		webSearchStateService: options.WebSearchStateService,
		sessionID:             options.SessionID,
		maxResults:            options.MaxResults,
		providerID:            options.ProviderID,
		strictCompression:     options.StrictCompression,
	}
}

// Execute executes the web search tool
func (t *WebSearchTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][WebSearch] Execute started")

	// Parse args from json.RawMessage
	var input WebSearchInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][WebSearch] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	// Parse query
	query := input.Query
	ok := query != ""
	if !ok || query == "" {
		logger.Errorf(ctx, "[Tool][WebSearch] Query is required")
		return &types.ToolResult{
			Success: false,
			Error:   "query parameter is required",
		}, fmt.Errorf("query parameter is required")
	}

	logger.Infof(ctx, "[Tool][WebSearch] Searching with query: %s, max_results: %d", query, t.maxResults)

	// Get tenant ID from context
	tenantID := uint64(0)
	if tid, ok := ctx.Value(types.TenantIDContextKey).(uint64); ok {
		tenantID = tid
	}

	if tenantID == 0 {
		logger.Errorf(ctx, "[Tool][WebSearch] Workspace ID not found in context")
		return &types.ToolResult{
			Success: false,
			Error:   "workspace ID not found in context",
		}, fmt.Errorf("workspace ID not found in context")
	}

	// Get tenant info from context (same approach as search.go)
	var tenant *types.Tenant
	if tenantValue := ctx.Value(types.TenantInfoContextKey); tenantValue != nil {
		tenant, _ = tenantValue.(*types.Tenant)
	}

	// Resolve provider ID: tool-level (set from agent config, which already resolved default)
	resolvedProviderID := t.providerID

	// Create a copy of the effective web search config with maxResults from agent config.
	searchConfig := types.EffectiveWebSearchConfig(nil)
	if tenant != nil {
		searchConfig = types.EffectiveWebSearchConfig(tenant.WebSearchConfig)
	}
	searchConfig.MaxResults = t.maxResults
	// Perform web search
	logger.Infof(
		ctx,
		"[Tool][WebSearch] Performing web search with providerID: %s, maxResults: %d",
		resolvedProviderID,
		searchConfig.MaxResults,
	)
	webResults, err := t.webSearchService.Search(ctx, resolvedProviderID, searchConfig, query)
	if err != nil {
		logger.Errorf(ctx, "[Tool][WebSearch] Web search failed: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("web search failed: %v", err),
		}, fmt.Errorf("web search failed: %w", err)
	}

	logger.Infof(ctx, "[Tool][WebSearch] Web search returned %d results", len(webResults))

	// Apply RAG compression if configured
	if len(webResults) > 0 && searchConfig.CompressionMethod != "none" &&
		searchConfig.CompressionMethod != "" {
		logger.Infof(ctx, "[Tool][WebSearch] Applying RAG compression")
		compressed, err := t.compressWithRAG(ctx, webSearchCompressionRequest{
			query: query, results: webResults, config: searchConfig,
		})
		if err != nil {
			if t.strictCompression {
				wrapped := fmt.Errorf("web search RAG compression failed: %w", err)
				return &types.ToolResult{Success: false, Error: wrapped.Error()}, wrapped
			}
			logger.Warnf(ctx, "[Tool][WebSearch] RAG compression failed, using raw results: %v", err)
		} else {
			webResults = compressed
			logger.Infof(ctx, "[Tool][WebSearch] RAG compression completed, %d results", len(webResults))
		}
	}

	// Format output
	if len(webResults) == 0 {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("No web search results found for query: %s", query),
			Data: map[string]interface{}{
				"query":   query,
				"results": []interface{}{},
				"count":   0,
			},
		}, nil
	}

	// Build output text
	output := "=== Web Search Results ===\n"
	output += fmt.Sprintf("Query: %s\n", query)
	output += fmt.Sprintf("Found %d result(s)\n\n", len(webResults))

	// Format results
	formattedResults := make([]map[string]interface{}, 0, len(webResults))
	for i, result := range webResults {
		output += fmt.Sprintf("Result #%d:\n", i+1)
		output += fmt.Sprintf("  Title: %s\n", result.Title)
		output += fmt.Sprintf("  URL: %s\n", result.URL)
		if result.Snippet != "" {
			output += fmt.Sprintf("  Snippet: %s\n", result.Snippet)
		}
		if result.Content != "" {
			// Truncate content if too long
			content := result.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			output += fmt.Sprintf("  Content: %s\n", content)
		}
		if result.PublishedAt != nil {
			output += fmt.Sprintf("  Published: %s\n", result.PublishedAt.Format(time.RFC3339))
		}
		output += "\n"

		resultData := map[string]interface{}{
			"result_index":  i + 1,
			"title":         result.Title,
			"url":           result.URL,
			"snippet":       result.Snippet,
			"content":       result.Content,
			"source":        result.Source,
			"evidence_type": "search_summary",
			"page_verified": false,
		}
		if result.PublishedAt != nil {
			resultData["published_at"] = result.PublishedAt.Format(time.RFC3339)
		}
		formattedResults = append(formattedResults, resultData)
	}

	// Add guidance for next steps
	output += "\n=== Next Steps ===\n"
	if len(webResults) > 0 {
		output += "- Titles, URLs, snippets, and content snippets are usable search-summary evidence.\n"
		output += "- If the evidence is sufficient, answer now. Use web_fetch only for claims that need full-page verification.\n"
		output += "- If fetching fails, retain these results, disclose that page content was not verified, and avoid presenting dynamic facts as certain.\n"
	} else {
		output += "- No web search results found. Consider:\n"
		output += "  - Try different search queries or keywords\n"
		output += "  - Check if question can be answered from knowledge base instead\n"
		output += "  - Verify if the topic requires real-time information\n"
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"query":        query,
			"results":      formattedResults,
			"count":        len(webResults),
			"display_type": "web_search_results",
		},
	}, nil
}

type webSearchCompressionRequest struct {
	query   string
	results []*types.WebSearchResult
	config  *types.WebSearchConfig
}

func (t *WebSearchTool) compressWithRAG(
	ctx context.Context,
	request webSearchCompressionRequest,
) ([]*types.WebSearchResult, error) {
	t.compressionMu.Lock()
	defer t.compressionMu.Unlock()

	tempKBID, seen, ids := t.webSearchStateService.GetWebSearchTempKBState(ctx, t.sessionID)
	compressed, kbID, newSeen, newIDs, err := t.webSearchService.CompressWithRAG(
		ctx, t.sessionID, tempKBID, []string{strings.TrimSpace(request.query)},
		request.results, request.config, t.knowledgeBaseService, t.knowledgeService, seen, ids,
	)
	if err != nil {
		return nil, err
	}
	t.webSearchStateService.SaveWebSearchTempKBState(
		ctx, t.sessionID, kbID, newSeen, newIDs,
	)
	return compressed, nil
}
