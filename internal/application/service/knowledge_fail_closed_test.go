package service

import (
	"context"
	"errors"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFailClosedKnowledgeProcessingPersistsFailedOnLastRetry(t *testing.T) {
	knowledge := &types.Knowledge{ID: "k1", ParseStatus: types.ParseStatusProcessing}
	repo := &reparseFailureKnowledgeRepo{knowledge: knowledge}
	svc := &knowledgeService{repo: repo}

	err := svc.failClosedKnowledgeProcessing(
		context.Background(), knowledge, true,
		types.StageChunking, werrors.ErrCodeChunkingFailed,
		"failed to split knowledge document: %v", errors.New("oversize_atomic"),
	)

	require.Error(t, err)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
	require.Contains(t, knowledge.ErrorMessage, "oversize_atomic")
	require.Equal(t, 1, repo.updateCalls)
}

func TestFailClosedKnowledgeProcessingDoesNotPersistBeforeLastRetry(t *testing.T) {
	knowledge := &types.Knowledge{ID: "k1", ParseStatus: types.ParseStatusProcessing}
	repo := &reparseFailureKnowledgeRepo{knowledge: knowledge}
	svc := &knowledgeService{repo: repo}

	err := svc.failClosedKnowledgeProcessing(
		context.Background(), knowledge, false,
		types.StageChunking, werrors.ErrCodeChunkingFailed,
		"failed to split knowledge document: %v", errors.New("oversize_atomic"),
	)

	require.Error(t, err)
	require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus)
	require.Equal(t, 0, repo.updateCalls)
}
