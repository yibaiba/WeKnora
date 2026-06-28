package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupSourceACLRepoTestDB(t *testing.T) *sourceACLRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.SourceACLSnapshot{}, &types.SourceACLEntry{}))
	return NewSourceACLRepository(db).(*sourceACLRepository)
}

func TestSourceACLRepositoryUpsertSnapshotReplacesEntries(t *testing.T) {
	repo := setupSourceACLRepoTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	record, err := repo.UpsertSnapshot(ctx, interfaces.SourceACLUpsertInput{
		Snapshot: &types.SourceACLSnapshot{
			TenantID:        1,
			Provider:        types.ConnectorTypeWeComWeDrive,
			KnowledgeID:     "knowledge-1",
			KnowledgeBaseID: "kb-1",
			SourceItemID:    "file-1",
			SyncedAt:        now,
		},
		Entries: []*types.SourceACLEntry{
			{SubjectType: types.SourceACLSubjectWeComUser, SubjectID: "wx-a"},
			{SubjectType: types.SourceACLSubjectWeComDepartment, SubjectID: "2"},
			{SubjectType: types.SourceACLSubjectWeComDepartment, SubjectID: "2"},
		},
	})
	require.NoError(t, err)
	require.NotZero(t, record.Snapshot.ID)
	require.Len(t, record.Entries, 2)

	record, err = repo.UpsertSnapshot(ctx, interfaces.SourceACLUpsertInput{
		Snapshot: &types.SourceACLSnapshot{
			TenantID:        1,
			Provider:        types.ConnectorTypeWeComWeDrive,
			KnowledgeID:     "knowledge-1",
			KnowledgeBaseID: "kb-1",
			SourceItemID:    "file-1",
			Status:          types.SourceACLStatusReady,
			Visibility:      types.SourceACLVisibilityRestricted,
			SourceHash:      "hash-2",
		},
		Entries: []*types.SourceACLEntry{
			{SubjectType: types.SourceACLSubjectWeComUser, SubjectID: "wx-b"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "hash-2", record.Snapshot.SourceHash)
	require.Len(t, record.Entries, 1)
	require.Equal(t, "wx-b", record.Entries[0].SubjectID)

	byKnowledge, err := repo.FindByKnowledgeID(ctx, 1, "knowledge-1")
	require.NoError(t, err)
	require.Equal(t, "hash-2", byKnowledge.Snapshot.SourceHash)
	require.Len(t, byKnowledge.Entries, 1)
	require.Equal(t, "wx-b", byKnowledge.Entries[0].SubjectID)

	bySource, err := repo.FindBySourceItem(ctx, 1, types.ConnectorTypeWeComWeDrive, "file-1")
	require.NoError(t, err)
	require.Equal(t, byKnowledge.Snapshot.ID, bySource.Snapshot.ID)
}

func TestSourceACLRepositoryFindMissing(t *testing.T) {
	repo := setupSourceACLRepoTestDB(t)

	_, err := repo.FindByKnowledgeID(context.Background(), 1, "missing")
	require.ErrorIs(t, err, ErrSourceACLNotFound)
}
