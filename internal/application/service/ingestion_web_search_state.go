package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type ingestionWebSearchState struct {
	mu            sync.Mutex
	knowledgeBase interfaces.WebSearchTemporaryKnowledgeBaseService
	knowledge     interfaces.WebSearchTemporaryKnowledgeService
	tempKBID      string
	seenURLs      map[string]bool
	knowledgeIDs  []string
}

func newIngestionWebSearchState(
	knowledgeBase interfaces.WebSearchTemporaryKnowledgeBaseService,
	knowledge interfaces.WebSearchTemporaryKnowledgeService,
) *ingestionWebSearchState {
	return &ingestionWebSearchState{
		knowledgeBase: knowledgeBase,
		knowledge:     knowledge,
		seenURLs:      make(map[string]bool),
	}
}

func (s *ingestionWebSearchState) GetWebSearchTempKBState(
	_ context.Context,
	_ string,
) (string, map[string]bool, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tempKBID, copySeenURLs(s.seenURLs), append([]string(nil), s.knowledgeIDs...)
}

func (s *ingestionWebSearchState) SaveWebSearchTempKBState(
	_ context.Context,
	_ string,
	tempKBID string,
	seenURLs map[string]bool,
	knowledgeIDs []string,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tempKBID = tempKBID
	s.seenURLs = copySeenURLs(seenURLs)
	s.knowledgeIDs = append([]string(nil), knowledgeIDs...)
}

func (s *ingestionWebSearchState) DeleteWebSearchTempKBState(ctx context.Context, _ string) error {
	tempKBID, knowledgeIDs := s.cleanupSnapshot()
	if tempKBID == "" {
		return nil
	}
	kb, err := s.knowledgeBase.GetKnowledgeBaseByID(ctx, tempKBID)
	if err != nil {
		return fmt.Errorf("读取 Web 搜索临时知识库失败: %w", err)
	}
	if !isWebSearchTemporaryKB(kb) {
		return fmt.Errorf("拒绝清理非 Web 搜索临时知识库 %s", tempKBID)
	}

	cleanupErr := s.deleteTemporaryKnowledge(ctx, knowledgeIDs)
	if err := s.knowledgeBase.DeleteKnowledgeBase(ctx, tempKBID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("删除 Web 搜索临时知识库失败: %w", err))
	}
	if cleanupErr == nil {
		s.clear()
	}
	return cleanupErr
}

func (s *ingestionWebSearchState) cleanupSnapshot() (string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tempKBID, append([]string(nil), s.knowledgeIDs...)
}

func (s *ingestionWebSearchState) deleteTemporaryKnowledge(ctx context.Context, ids []string) error {
	var cleanupErr error
	for _, id := range ids {
		if err := s.knowledge.DeleteKnowledge(ctx, id); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("删除 Web 搜索临时文档失败: %w", err))
		}
	}
	return cleanupErr
}

func (s *ingestionWebSearchState) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tempKBID = ""
	s.seenURLs = make(map[string]bool)
	s.knowledgeIDs = nil
}

func copySeenURLs(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for url, seen := range source {
		result[url] = seen
	}
	return result
}
