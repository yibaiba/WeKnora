package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type searchACLGuard struct {
	denyIDs map[string]struct{}
}

func (g *searchACLGuard) CanRead(
	context.Context,
	interfaces.SourceACLGuardRequest,
) (*types.SourceACLDecision, error) {
	return &types.SourceACLDecision{Allowed: true}, nil
}

func (g *searchACLGuard) RequireRead(
	context.Context,
	interfaces.SourceACLGuardRequest,
) error {
	return errors.New("not implemented")
}

func (g *searchACLGuard) FilterKnowledges(
	_ context.Context,
	_ string,
	knowledges []*types.Knowledge,
) ([]*types.Knowledge, error) {
	filtered := make([]*types.Knowledge, 0, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge == nil {
			continue
		}
		if _, denied := g.denyIDs[knowledge.ID]; denied {
			continue
		}
		filtered = append(filtered, knowledge)
	}
	return filtered, nil
}

func (g *searchACLGuard) FilterIndexCandidates(
	context.Context,
	string,
	[]*types.IndexWithScore,
) ([]*types.IndexWithScore, error) {
	return nil, errors.New("not implemented")
}

func TestSearchKnowledgeForScopesSourceACLDoesNotExposeDeniedCounts(t *testing.T) {
	db := setupKnowledgeSearchACLDB(t)
	seedKnowledgeSearchACLRows(t, db)

	guard := &searchACLGuard{denyIDs: map[string]struct{}{}}
	for i := 0; i < sourceACLSearchFetchLimit(10); i++ {
		guard.denyIDs[deniedSearchKnowledgeID(i)] = struct{}{}
	}
	service := &knowledgeService{
		repo:           repository.NewKnowledgeRepository(db),
		sourceACLGuard: guard,
	}

	knowledges, hasMore, total, err := service.SearchKnowledgeForScopes(
		context.Background(),
		[]types.KnowledgeSearchScope{{TenantID: 1, KBID: "kb-1"}},
		"project",
		0,
		10,
		nil,
	)

	require.NoError(t, err)
	require.False(t, hasMore)
	require.Equal(t, int64(1), total)
	require.Len(t, knowledges, 1)
	require.Equal(t, "public", knowledges[0].ID)
}

func setupKnowledgeSearchACLDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}, &types.Knowledge{}))
	return db
}

func seedKnowledgeSearchACLRows(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now()
	require.NoError(t, db.Create(&types.KnowledgeBase{
		ID:        "kb-1",
		Name:      "ACL KB",
		Type:      types.KnowledgeBaseTypeDocument,
		TenantID:  1,
		CreatedAt: now,
		UpdatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID:              "public",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            "file",
		Title:           "project public",
		FileName:        "project-public.txt",
		CreatedAt:       now.Add(time.Minute),
		UpdatedAt:       now.Add(time.Minute),
	}).Error)

	for i := 0; i < sourceACLSearchFetchLimit(10); i++ {
		require.NoError(t, db.Create(&types.Knowledge{
			ID:              deniedSearchKnowledgeID(i),
			TenantID:        1,
			KnowledgeBaseID: "kb-1",
			Type:            "file",
			Title:           "project restricted",
			FileName:        "project-restricted.txt",
			CreatedAt:       now.Add(-time.Duration(i) * time.Minute),
			UpdatedAt:       now.Add(-time.Duration(i) * time.Minute),
		}).Error)
	}
}

func deniedSearchKnowledgeID(index int) string {
	return fmt.Sprintf("restricted-%03d", index)
}
