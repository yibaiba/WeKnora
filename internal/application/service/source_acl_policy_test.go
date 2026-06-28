package service

import (
	"context"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type memorySourceACLRepo struct {
	records map[string]*types.SourceACLRecord
}

func newMemorySourceACLRepo() *memorySourceACLRepo {
	return &memorySourceACLRepo{records: map[string]*types.SourceACLRecord{}}
}

func (r *memorySourceACLRepo) UpsertSnapshot(
	_ context.Context,
	input interfaces.SourceACLUpsertInput,
) (*types.SourceACLRecord, error) {
	record := &types.SourceACLRecord{Snapshot: input.Snapshot, Entries: input.Entries}
	r.records[input.Snapshot.KnowledgeID] = record
	return record, nil
}

func (r *memorySourceACLRepo) FindByKnowledgeID(
	_ context.Context,
	_ uint64,
	knowledgeID string,
) (*types.SourceACLRecord, error) {
	record := r.records[knowledgeID]
	if record == nil {
		return nil, apprepo.ErrSourceACLNotFound
	}
	return record, nil
}

func (r *memorySourceACLRepo) FindBySourceItem(
	context.Context,
	uint64,
	string,
	string,
) (*types.SourceACLRecord, error) {
	return nil, apprepo.ErrSourceACLNotFound
}

type memoryWeComIdentityService struct {
	interfaces.WeComIdentityService
	subjects map[string]*types.WeComACLSubject
}

func (s *memoryWeComIdentityService) ResolveSubject(
	_ context.Context,
	_ uint64,
	weknoraUserID string,
) (*types.WeComACLSubject, error) {
	subject := s.subjects[weknoraUserID]
	if subject == nil {
		return &types.WeComACLSubject{Bound: false, Departments: []string{}, Groups: []string{}}, nil
	}
	return subject, nil
}

func TestSourceACLPolicyAllowsDirectDepartmentGroupAndPublic(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name       string
		record     *types.SourceACLRecord
		actor      string
		wantReason string
	}{
		{
			name: "direct user",
			record: sourceACLPolicyRecord("k-direct", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady,
				aclEntry(types.SourceACLSubjectWeComUser, "wx-a")),
			actor:      "u1",
			wantReason: types.SourceACLReasonAllowedDirectUser,
		},
		{
			name: "department",
			record: sourceACLPolicyRecord("k-dept", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady,
				aclEntry(types.SourceACLSubjectWeComDepartment, "2")),
			actor:      "u1",
			wantReason: types.SourceACLReasonAllowedDepartment,
		},
		{
			name: "group",
			record: sourceACLPolicyRecord("k-group", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady,
				aclEntry(types.SourceACLSubjectWeComGroup, "g1")),
			actor:      "u1",
			wantReason: types.SourceACLReasonAllowedGroup,
		},
		{
			name:       "public visibility",
			record:     sourceACLPolicyRecord("k-public", types.SourceACLVisibilityPublic, types.SourceACLStatusReady),
			actor:      "",
			wantReason: types.SourceACLReasonAllowedPublic,
		},
		{
			name: "public entry",
			record: sourceACLPolicyRecord("k-public-entry", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady,
				aclEntry(types.SourceACLSubjectPublic, "")),
			actor:      "",
			wantReason: types.SourceACLReasonAllowedPublic,
		},
		{
			name:       "all company",
			record:     sourceACLPolicyRecord("k-all", types.SourceACLVisibilityAllCompany, types.SourceACLStatusReady),
			actor:      "u1",
			wantReason: types.SourceACLReasonAllowedAllCompany,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemorySourceACLRepo()
			repo.records[tt.record.Snapshot.KnowledgeID] = tt.record
			svc := NewSourceACLPolicyService(repo, sourceACLIdentityService()).(*sourceACLPolicyService)

			decision, err := svc.CanRead(context.Background(), interfaces.SourceACLDecisionRequest{
				TenantID:    1,
				KnowledgeID: tt.record.Snapshot.KnowledgeID,
				ActorUserID: tt.actor,
				Now:         now,
			})
			require.NoError(t, err)
			require.True(t, decision.Allowed)
			require.Equal(t, tt.wantReason, decision.Reason)
			if tt.actor == "u1" {
				require.Equal(t, "wx-a", decision.WeComUserID)
			}
		})
	}
}

func TestSourceACLPolicyDeniesRestrictedFailures(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	tests := []struct {
		name       string
		record     *types.SourceACLRecord
		actor      string
		wantReason string
	}{
		{
			name:       "missing actor",
			record:     sourceACLPolicyRecord("k-unbound", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady),
			wantReason: types.SourceACLReasonActorUnbound,
		},
		{
			name:       "no matching subject",
			record:     sourceACLPolicyRecord("k-no-match", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady, aclEntry(types.SourceACLSubjectWeComUser, "wx-b")),
			actor:      "u1",
			wantReason: types.SourceACLReasonNoMatchingSubject,
		},
		{
			name:       "invalid snapshot",
			record:     sourceACLPolicyRecord("k-invalid", types.SourceACLVisibilityRestricted, types.SourceACLStatusInvalid),
			actor:      "u1",
			wantReason: types.SourceACLReasonSnapshotInvalid,
		},
		{
			name:       "stale status",
			record:     sourceACLPolicyRecord("k-stale", types.SourceACLVisibilityRestricted, types.SourceACLStatusStale),
			actor:      "u1",
			wantReason: types.SourceACLReasonSnapshotStale,
		},
		{
			name:       "unmapped",
			record:     sourceACLPolicyRecord("k-unmapped", types.SourceACLVisibilityRestricted, types.SourceACLStatusUnmapped),
			actor:      "u1",
			wantReason: types.SourceACLReasonSnapshotUnmapped,
		},
		{
			name: "expired",
			record: func() *types.SourceACLRecord {
				r := sourceACLPolicyRecord("k-expired", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady)
				r.Snapshot.StaleAfter = &past
				return r
			}(),
			actor:      "u1",
			wantReason: types.SourceACLReasonSnapshotStale,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemorySourceACLRepo()
			repo.records[tt.record.Snapshot.KnowledgeID] = tt.record
			svc := NewSourceACLPolicyService(repo, sourceACLIdentityService()).(*sourceACLPolicyService)

			decision, err := svc.CanRead(context.Background(), interfaces.SourceACLDecisionRequest{
				TenantID:    1,
				KnowledgeID: tt.record.Snapshot.KnowledgeID,
				ActorUserID: tt.actor,
				Now:         now,
			})
			require.NoError(t, err)
			require.False(t, decision.Allowed)
			require.Equal(t, tt.wantReason, decision.Reason)
		})
	}
}

func TestSourceACLPolicyServiceKeyRequiresActor(t *testing.T) {
	repo := newMemorySourceACLRepo()
	repo.records["k1"] = sourceACLPolicyRecord("k1", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady,
		aclEntry(types.SourceACLSubjectWeComUser, "wx-a"))
	repo.records["public"] = sourceACLPolicyRecord("public", types.SourceACLVisibilityPublic, types.SourceACLStatusReady)
	svc := NewSourceACLPolicyService(repo, sourceACLIdentityService()).(*sourceACLPolicyService)

	decision, err := svc.CanRead(context.Background(), interfaces.SourceACLDecisionRequest{
		TenantID:       1,
		KnowledgeID:    "k1",
		ServiceKeyCall: true,
	})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, types.SourceACLReasonServiceKeyActorMissing, decision.Reason)

	decision, err = svc.CanRead(context.Background(), interfaces.SourceACLDecisionRequest{
		TenantID:       1,
		KnowledgeID:    "k1",
		ActorUserID:    "u1",
		ServiceKeyCall: true,
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Equal(t, types.SourceACLReasonAllowedDirectUser, decision.Reason)

	decision, err = svc.CanRead(context.Background(), interfaces.SourceACLDecisionRequest{
		TenantID:       1,
		KnowledgeID:    "public",
		ActorUserID:    "missing",
		ServiceKeyCall: true,
	})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, types.SourceACLReasonActorUnbound, decision.Reason)

	decision, err = svc.CanRead(context.Background(), interfaces.SourceACLDecisionRequest{
		TenantID:       1,
		KnowledgeID:    "public",
		ActorUserID:    "u1",
		ServiceKeyCall: true,
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Equal(t, types.SourceACLReasonAllowedPublic, decision.Reason)
	require.Equal(t, "wx-a", decision.WeComUserID)
}

func TestSourceACLPolicyDenyMissingAndRevoked(t *testing.T) {
	repo := newMemorySourceACLRepo()
	svc := NewSourceACLPolicyService(repo, sourceACLIdentityService()).(*sourceACLPolicyService)

	decision, err := svc.CanRead(context.Background(), interfaces.SourceACLDecisionRequest{
		TenantID:    1,
		KnowledgeID: "missing",
		ActorUserID: "u1",
	})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, types.SourceACLReasonSnapshotMissing, decision.Reason)

	repo.records["k1"] = sourceACLPolicyRecord("k1", types.SourceACLVisibilityRestricted, types.SourceACLStatusReady,
		aclEntry(types.SourceACLSubjectWeComUser, "wx-b"))
	decision, err = svc.CanRead(context.Background(), interfaces.SourceACLDecisionRequest{
		TenantID:    1,
		KnowledgeID: "k1",
		ActorUserID: "u1",
	})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, types.SourceACLReasonNoMatchingSubject, decision.Reason)
}

func sourceACLPolicyRecord(
	knowledgeID string,
	visibility string,
	status string,
	entries ...*types.SourceACLEntry,
) *types.SourceACLRecord {
	return &types.SourceACLRecord{
		Snapshot: &types.SourceACLSnapshot{
			TenantID:        1,
			Provider:        types.ConnectorTypeWeComWeDrive,
			KnowledgeID:     knowledgeID,
			KnowledgeBaseID: "kb-1",
			SourceItemID:    "file-" + knowledgeID,
			Visibility:      visibility,
			Status:          status,
			SyncedAt:        time.Now().UTC(),
		},
		Entries: entries,
	}
}

func aclEntry(subjectType string, subjectID string) *types.SourceACLEntry {
	return &types.SourceACLEntry{
		SubjectType: subjectType,
		SubjectID:   subjectID,
		Permission:  types.SourceACLPermissionRead,
	}
}

func sourceACLIdentityService() interfaces.WeComIdentityService {
	return &memoryWeComIdentityService{subjects: map[string]*types.WeComACLSubject{
		"u1": {
			Bound:         true,
			WeKnoraUserID: "u1",
			WeComUserID:   "wx-a",
			Status:        types.WeComIdentityStatusActive,
			Departments:   []string{"1", "2"},
			Groups:        []string{"g1"},
		},
		"inactive": {
			Bound:       false,
			WeComUserID: "wx-inactive",
			Status:      types.WeComIdentityStatusUnavailable,
			Departments: []string{},
			Groups:      []string{},
		},
	}}
}
