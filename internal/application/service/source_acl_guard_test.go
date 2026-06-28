package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type guardKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	byID map[string]*types.Knowledge
}

func (r *guardKnowledgeRepo) GetKnowledgeBatchByIDsOnly(
	_ context.Context,
	ids []string,
) ([]*types.Knowledge, error) {
	out := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		if knowledge := r.byID[id]; knowledge != nil {
			out = append(out, knowledge)
		}
	}
	return out, nil
}

type guardAuditService struct {
	interfaces.AuditLogService
	entries []*types.AuditLog
}

func (s *guardAuditService) Log(_ context.Context, entry *types.AuditLog) error {
	s.entries = append(s.entries, entry)
	return nil
}

func TestSourceACLGuardPreservesPublicNonSourceKnowledge(t *testing.T) {
	guard, audit := newTestSourceACLGuard(nil)
	public := &types.Knowledge{ID: "k-public", TenantID: 1, Channel: types.ChannelWeb}

	decision, err := guard.CanRead(context.Background(), interfaces.SourceACLGuardRequest{
		Knowledge: public,
		Purpose:   "knowledge_search",
	})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Empty(t, audit.entries)
}

func TestSourceACLGuardDeniesRestrictedWeDriveWithoutSnapshot(t *testing.T) {
	guard, audit := newTestSourceACLGuard(nil)
	knowledge := restrictedWeDriveKnowledge("k-restricted")

	decision, err := guard.CanRead(context.Background(), interfaces.SourceACLGuardRequest{
		Knowledge: knowledge,
		Purpose:   "preview",
	})

	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, types.SourceACLReasonSnapshotMissing, decision.Reason)
	require.Len(t, audit.entries, 1)
	require.Equal(t, types.AuditActionSourceACLReadDenied, audit.entries[0].Action)
}

func TestSourceACLGuardUsesExplicitActorForServiceKeyCalls(t *testing.T) {
	repo := newMemorySourceACLRepo()
	repo.records["k1"] = sourceACLPolicyRecord("k1", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady,
		aclEntry(types.SourceACLSubjectWeComUser, "wx-a"))
	guard, _ := newTestSourceACLGuard(repo)
	ctx := context.WithValue(context.Background(), types.ServiceKeyCallContextKey, true)
	ctx = context.WithValue(ctx, types.SourceACLActorUserIDContextKey, "u1")

	decision, err := guard.CanRead(ctx, interfaces.SourceACLGuardRequest{
		Knowledge: restrictedWeDriveKnowledge("k1"),
		Purpose:   "cli",
	})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.True(t, decision.ServiceKeyCall)
	require.Equal(t, "u1", decision.ActorUserID)
}

func TestSourceACLGuardFiltersIndexCandidatesBeforeHydration(t *testing.T) {
	repo := newMemorySourceACLRepo()
	repo.records["allow"] = sourceACLPolicyRecord("allow", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady,
		aclEntry(types.SourceACLSubjectWeComUser, "wx-a"))
	repo.records["deny"] = sourceACLPolicyRecord("deny", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady,
		aclEntry(types.SourceACLSubjectWeComUser, "wx-b"))
	guard, _ := newTestSourceACLGuard(repo)
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "u1")

	filtered, err := guard.FilterIndexCandidates(ctx, "hybrid_search", []*types.IndexWithScore{
		{ChunkID: "c1", KnowledgeID: "allow"},
		{ChunkID: "c2", KnowledgeID: "deny"},
		{ChunkID: "c3", KnowledgeID: "public"},
	})

	require.NoError(t, err)
	require.Len(t, filtered, 2)
	require.Equal(t, "allow", filtered[0].KnowledgeID)
	require.Equal(t, "public", filtered[1].KnowledgeID)
}

func TestSourceACLSearchFetchLimitOverfetchesWithinCap(t *testing.T) {
	require.Equal(t, 30, sourceACLSearchFetchLimit(10))
	require.Equal(t, 100, sourceACLSearchFetchLimit(60))
	require.Equal(t, 150, sourceACLSearchFetchLimit(150))
}

func newTestSourceACLGuard(repo *memorySourceACLRepo) (*sourceACLGuardService, *guardAuditService) {
	if repo == nil {
		repo = newMemorySourceACLRepo()
	}
	policy := NewSourceACLPolicyService(repo, sourceACLIdentityService())
	audit := &guardAuditService{}
	knowledge := &guardKnowledgeRepo{byID: map[string]*types.Knowledge{
		"allow":  restrictedWeDriveKnowledge("allow"),
		"deny":   restrictedWeDriveKnowledge("deny"),
		"public": {ID: "public", TenantID: 1, Channel: types.ChannelWeb},
		"k1":     restrictedWeDriveKnowledge("k1"),
	}}
	return NewSourceACLGuardService(repo, policy, knowledge, audit).(*sourceACLGuardService), audit
}

func restrictedWeDriveKnowledge(id string) *types.Knowledge {
	metadata := types.JSON(`{"provider":"wecom_wedrive","access_mode":"restricted","queryable_state":"restricted"}`)
	return &types.Knowledge{ID: id, TenantID: 1, KnowledgeBaseID: "kb-1", Channel: types.ChannelWeComWeDrive, Metadata: metadata}
}
