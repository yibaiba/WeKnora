package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type SourceACLUpsertInput struct {
	Snapshot *types.SourceACLSnapshot
	Entries  []*types.SourceACLEntry
}

type SourceACLDecisionRequest struct {
	TenantID       uint64
	KnowledgeID    string
	ActorUserID    string
	Purpose        string
	ServiceKeyCall bool
	Now            time.Time
}

type SourceACLGuardRequest struct {
	Knowledge      *types.Knowledge
	KnowledgeID    string
	TenantID       uint64
	ActorUserID    string
	Purpose        string
	ServiceKeyCall bool
	Now            time.Time
}

type SourceACLRepository interface {
	UpsertSnapshot(ctx context.Context, input SourceACLUpsertInput) (*types.SourceACLRecord, error)
	FindByKnowledgeID(ctx context.Context, tenantID uint64, knowledgeID string) (*types.SourceACLRecord, error)
	FindBySourceItem(
		ctx context.Context,
		tenantID uint64,
		provider string,
		sourceItemID string,
	) (*types.SourceACLRecord, error)
}

type SourceACLPolicyService interface {
	CanRead(ctx context.Context, request SourceACLDecisionRequest) (*types.SourceACLDecision, error)
}

type SourceACLGuardService interface {
	CanRead(ctx context.Context, request SourceACLGuardRequest) (*types.SourceACLDecision, error)
	RequireRead(ctx context.Context, request SourceACLGuardRequest) error
	FilterKnowledges(ctx context.Context, purpose string, knowledges []*types.Knowledge) ([]*types.Knowledge, error)
	FilterIndexCandidates(ctx context.Context, purpose string, candidates []*types.IndexWithScore) ([]*types.IndexWithScore, error)
}
