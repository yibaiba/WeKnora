package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupWeComIdentityRepoTestDB(t *testing.T) (*gorm.DB, *wecomIdentityRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.WeComIdentity{},
		&types.WeComDepartment{},
		&types.WeComIdentityDepartment{},
		&types.WeComUserBinding{},
	))
	repo := NewWeComIdentityRepository(db).(*wecomIdentityRepository)
	return db, repo
}

func TestWeComIdentityRepositoryBindingRebindAndResolve(t *testing.T) {
	db, repo := setupWeComIdentityRepoTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	require.NoError(t, repo.UpsertIdentities(ctx, []*types.WeComIdentity{
		{TenantID: 1, UserID: "wx-a", Status: types.WeComIdentityStatusActive, SyncedAt: now},
		{TenantID: 1, UserID: "wx-b", Status: types.WeComIdentityStatusActive, SyncedAt: now},
	}))
	require.NoError(t, repo.ReplaceIdentityDepartments(ctx, 1, "wx-a", []string{"1", "2", "2"}, now))
	binding, err := repo.UpsertBinding(ctx, &types.WeComUserBinding{
		TenantID:      1,
		WeKnoraUserID: "user-1",
		WeComUserID:   "wx-a",
	})
	require.NoError(t, err)
	require.Equal(t, "wx-a", binding.WeComUserID)

	subject, err := repo.ResolveSubject(ctx, 1, "user-1")
	require.NoError(t, err)
	require.True(t, subject.Bound)
	require.Equal(t, "wx-a", subject.WeComUserID)
	require.Equal(t, []string{"1", "2"}, subject.Departments)

	_, err = repo.UpsertBinding(ctx, &types.WeComUserBinding{
		TenantID:      1,
		WeKnoraUserID: "user-1",
		WeComUserID:   "wx-b",
	})
	require.NoError(t, err)
	subject, err = repo.ResolveSubject(ctx, 1, "user-1")
	require.NoError(t, err)
	require.True(t, subject.Bound)
	require.Equal(t, "wx-b", subject.WeComUserID)

	var activeCount int64
	require.NoError(t, db.Model(&types.WeComUserBinding{}).
		Where("tenant_id = ? AND weknora_user_id = ? AND status = ? AND deleted_at IS NULL",
			1, "user-1", types.WeComBindingStatusActive).
		Count(&activeCount).Error)
	require.Equal(t, int64(1), activeCount)
}

func TestWeComIdentityRepositoryRejectsDuplicateWeComUser(t *testing.T) {
	_, repo := setupWeComIdentityRepoTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	require.NoError(t, repo.UpsertIdentities(ctx, []*types.WeComIdentity{
		{TenantID: 1, UserID: "wx-a", Status: types.WeComIdentityStatusActive, SyncedAt: now},
	}))
	_, err := repo.UpsertBinding(ctx, &types.WeComUserBinding{
		TenantID:      1,
		WeKnoraUserID: "user-1",
		WeComUserID:   "wx-a",
	})
	require.NoError(t, err)

	_, err = repo.UpsertBinding(ctx, &types.WeComUserBinding{
		TenantID:      1,
		WeKnoraUserID: "user-2",
		WeComUserID:   "wx-a",
	})
	require.ErrorIs(t, err, ErrWeComBindingAlreadyExists)
}

func TestWeComIdentityRepositoryUnboundAndInactiveSubject(t *testing.T) {
	_, repo := setupWeComIdentityRepoTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	unbound, err := repo.ResolveSubject(ctx, 1, "user-missing")
	require.NoError(t, err)
	require.False(t, unbound.Bound)
	require.Empty(t, unbound.Departments)

	require.NoError(t, repo.UpsertIdentities(ctx, []*types.WeComIdentity{
		{TenantID: 1, UserID: "wx-suspended", Status: types.WeComIdentityStatusSuspended, SyncedAt: now},
	}))
	_, err = repo.UpsertBinding(ctx, &types.WeComUserBinding{
		TenantID:      1,
		WeKnoraUserID: "user-2",
		WeComUserID:   "wx-suspended",
	})
	require.NoError(t, err)

	subject, err := repo.ResolveSubject(ctx, 1, "user-2")
	require.NoError(t, err)
	require.False(t, subject.Bound)
	require.Equal(t, types.WeComIdentityStatusUnavailable, subject.Status)
}
