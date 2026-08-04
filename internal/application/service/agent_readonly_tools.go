package service

import (
	"context"
	"database/sql"
	"fmt"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

var sharedReadOnlyAgentTools = map[string]struct{}{
	agenttools.ToolThinking:            {},
	agenttools.ToolKnowledgeSearch:     {},
	agenttools.ToolGrepChunks:          {},
	agenttools.ToolListKnowledgeChunks: {},
	agenttools.ToolQueryKnowledgeGraph: {},
	agenttools.ToolGetDocumentInfo:     {},
	agenttools.ToolDatabaseQuery:       {},
	agenttools.ToolDataAnalysis:        {},
	agenttools.ToolDataSchema:          {},
	agenttools.ToolWebSearch:           {},
	agenttools.ToolWebFetch:            {},
	agenttools.ToolWikiReadPage:        {},
	agenttools.ToolWikiSearch:          {},
	agenttools.ToolWikiReadSourceDoc:   {},
	agenttools.ToolWikiReadIssue:       {},
}

type readOnlyAgentToolFactoryParams struct {
	dig.In

	Config               *config.Config
	KnowledgeBaseService interfaces.KnowledgeBaseService
	KnowledgeRepository  interfaces.KnowledgeRepository
	ChunkService         interfaces.ChunkService
	SourceACLGuard       interfaces.SourceACLGuardService
	FileService          interfaces.FileService
	TenantService        interfaces.TenantService
	WebSearchService     interfaces.WebSearchService
	WikiPageService      interfaces.WikiPageService
	StorageResolver      interfaces.StorageBackendResolver
	MCPServiceService    interfaces.MCPServiceService
	MCPManager           *mcp.MCPManager
	DB                   *gorm.DB
	DuckDB               *sql.DB
}

type readOnlyAgentToolFactory struct {
	params readOnlyAgentToolFactoryParams
	reader interfaces.KnowledgeReadService
}

type readOnlyAgentToolOptions struct {
	Config          *types.AgentConfig
	KnowledgeReader interfaces.KnowledgeReadService
	RerankModel     rerank.Reranker
	ChatModel       chat.Chat
	SessionID       string
	WebSearchState  interfaces.WebSearchStateService
	ReadOnlyWeb     bool
	WikiScopes      []agenttools.WikiScope
	WikiKBIDs       []string
	WikiRoutes      *agenttools.WikiRouteResolver
}

func NewReadOnlyAgentToolFactory(params readOnlyAgentToolFactoryParams) *readOnlyAgentToolFactory {
	return &readOnlyAgentToolFactory{
		params: params,
		reader: &repositoryKnowledgeReader{repository: params.KnowledgeRepository},
	}
}

func isSharedReadOnlyAgentTool(name string) bool {
	_, ok := sharedReadOnlyAgentTools[name]
	return ok
}

func (f *readOnlyAgentToolFactory) Build(
	_ context.Context,
	name string,
	options readOnlyAgentToolOptions,
) (types.Tool, error) {
	if f == nil || options.Config == nil {
		return nil, fmt.Errorf("只读 Agent 工具工厂未配置")
	}
	reader := options.KnowledgeReader
	if reader == nil {
		reader = f.reader
	}
	targets := options.Config.SearchTargets
	switch name {
	case agenttools.ToolThinking:
		return agenttools.NewSequentialThinkingTool(), nil
	case agenttools.ToolKnowledgeSearch:
		if err := f.requireKnowledgeTools(reader); err != nil {
			return nil, err
		}
		return agenttools.NewKnowledgeSearchTool(
			f.params.KnowledgeBaseService, reader, f.params.ChunkService,
			targets, options.RerankModel, options.ChatModel, f.params.Config,
		), nil
	case agenttools.ToolGrepChunks:
		if err := f.requireKnowledgeTools(reader); err != nil || f.params.DB == nil {
			return nil, joinToolDependencyError(err, "数据库未配置")
		}
		return agenttools.NewGrepChunksTool(f.params.DB, reader, f.params.SourceACLGuard, targets), nil
	case agenttools.ToolListKnowledgeChunks:
		if err := f.requireKnowledgeTools(reader); err != nil {
			return nil, err
		}
		return agenttools.NewListKnowledgeChunksTool(reader, f.params.ChunkService, f.params.SourceACLGuard, targets), nil
	case agenttools.ToolQueryKnowledgeGraph:
		if err := f.requireKnowledgeTools(reader); err != nil {
			return nil, err
		}
		return agenttools.NewQueryKnowledgeGraphTool(f.params.KnowledgeBaseService, targets).WithKnowledgeScope(reader), nil
	case agenttools.ToolGetDocumentInfo:
		if err := f.requireKnowledgeTools(reader); err != nil {
			return nil, err
		}
		return agenttools.NewGetDocumentInfoTool(reader, f.params.ChunkService, f.params.SourceACLGuard, targets), nil
	case agenttools.ToolDatabaseQuery:
		if f.params.DB == nil {
			return nil, fmt.Errorf("数据库未配置")
		}
		return agenttools.NewDatabaseQueryTool(f.params.DB, targets), nil
	case agenttools.ToolDataAnalysis:
		if err := f.requireKnowledgeTools(reader); err != nil || f.params.DuckDB == nil {
			return nil, joinToolDependencyError(err, "DuckDB 未配置")
		}
		return agenttools.NewDataAnalysisTool(
			f.params.KnowledgeBaseService, reader, f.params.TenantService,
			f.params.FileService, f.params.DuckDB, options.SessionID,
			f.params.SourceACLGuard, f.params.StorageResolver,
		).WithSearchTargets(targets), nil
	case agenttools.ToolDataSchema:
		if err := f.requireKnowledgeTools(reader); err != nil {
			return nil, err
		}
		return agenttools.NewDataSchemaTool(reader, f.params.ChunkService.GetRepository(), f.params.SourceACLGuard).
			WithSearchTargets(targets), nil
	case agenttools.ToolWebSearch:
		if f.params.WebSearchService == nil || (!options.ReadOnlyWeb && options.WebSearchState == nil) {
			return nil, fmt.Errorf("Web 搜索服务未配置")
		}
		if options.ReadOnlyWeb {
			return agenttools.NewReadOnlyWebSearchTool(
				f.params.WebSearchService, f.params.KnowledgeBaseService, reader,
				options.SessionID, options.Config.WebSearchMaxResults, options.Config.WebSearchProviderID,
			), nil
		}
		return agenttools.NewWebSearchTool(
			f.params.WebSearchService, f.params.KnowledgeBaseService, reader,
			options.WebSearchState, options.SessionID,
			options.Config.WebSearchMaxResults, options.Config.WebSearchProviderID,
		), nil
	case agenttools.ToolWebFetch:
		if options.ChatModel == nil {
			return nil, fmt.Errorf("web_fetch 需要聊天模型")
		}
		return agenttools.NewWebFetchTool(options.ChatModel), nil
	case agenttools.ToolWikiReadPage:
		if f.params.WikiPageService == nil || reader == nil {
			return nil, fmt.Errorf("Wiki 读取服务未配置")
		}
		return agenttools.NewWikiReadPageTool(f.params.WikiPageService, reader, options.WikiScopes, options.WikiRoutes), nil
	case agenttools.ToolWikiSearch:
		if f.params.WikiPageService == nil || reader == nil {
			return nil, fmt.Errorf("Wiki 读取服务未配置")
		}
		return agenttools.NewWikiSearchTool(f.params.WikiPageService, reader, options.WikiScopes, options.WikiRoutes), nil
	case agenttools.ToolWikiReadSourceDoc:
		if err := f.requireKnowledgeTools(reader); err != nil {
			return nil, err
		}
		return agenttools.NewWikiReadSourceDocTool(reader, f.params.ChunkService, f.params.SourceACLGuard, targets), nil
	case agenttools.ToolWikiReadIssue:
		if f.params.WikiPageService == nil {
			return nil, fmt.Errorf("Wiki 读取服务未配置")
		}
		return agenttools.NewWikiReadIssueTool(f.params.WikiPageService, options.WikiKBIDs), nil
	default:
		return nil, fmt.Errorf("工具 %q 不是共享只读工具", name)
	}
}

func (f *readOnlyAgentToolFactory) RegisterMCP(
	ctx context.Context,
	registry *agenttools.ToolRegistry,
	tenantID uint64,
) (int, error) {
	if f == nil || f.params.MCPServiceService == nil || f.params.MCPManager == nil {
		return 0, fmt.Errorf("MCP 只读工具服务未配置")
	}
	services, err := f.params.MCPServiceService.ListMCPServices(ctx, tenantID)
	if err != nil {
		return 0, fmt.Errorf("列出 MCP 服务失败: %w", err)
	}
	return agenttools.RegisterReadOnlyMCPTools(ctx, registry, services, f.params.MCPManager, nil, nil)
}

func (f *readOnlyAgentToolFactory) requireKnowledgeTools(reader interfaces.KnowledgeReadService) error {
	if reader == nil || f.params.KnowledgeBaseService == nil || f.params.ChunkService == nil ||
		f.params.SourceACLGuard == nil {
		return fmt.Errorf("知识读取服务未完整配置")
	}
	return nil
}

func joinToolDependencyError(err error, fallback string) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("%s", fallback)
}

type repositoryKnowledgeReader struct {
	repository interfaces.KnowledgeRepository
}

func (r *repositoryKnowledgeReader) GetKnowledgeByIDOnly(
	ctx context.Context,
	id string,
) (*types.Knowledge, error) {
	if r == nil || r.repository == nil {
		return nil, fmt.Errorf("知识仓储未配置")
	}
	return r.repository.GetKnowledgeByIDOnly(ctx, id)
}

func (r *repositoryKnowledgeReader) GetKnowledgeBatchByIDsOnly(
	ctx context.Context,
	ids []string,
) ([]*types.Knowledge, error) {
	if r == nil || r.repository == nil {
		return nil, fmt.Errorf("知识仓储未配置")
	}
	return r.repository.GetKnowledgeBatchByIDsOnly(ctx, ids)
}

func (r *repositoryKnowledgeReader) GetKnowledgeTags(
	ctx context.Context,
	knowledgeIDs []string,
) (map[string][]*types.KnowledgeTag, error) {
	if r == nil || r.repository == nil {
		return nil, fmt.Errorf("知识仓储未配置")
	}
	return r.repository.GetKnowledgeTags(ctx, knowledgeIDs)
}
