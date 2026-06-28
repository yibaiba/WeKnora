package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type sourceACLTestKnowledgeService struct {
	interfaces.KnowledgeService
	byID []*types.Knowledge
	list []*types.Knowledge
}

func (s *sourceACLTestKnowledgeService) GetKnowledgeByIDOnly(
	_ context.Context,
	id string,
) (*types.Knowledge, error) {
	return s.find(id), nil
}

func (s *sourceACLTestKnowledgeService) GetKnowledgeByID(
	_ context.Context,
	id string,
) (*types.Knowledge, error) {
	return s.find(id), nil
}

func (s *sourceACLTestKnowledgeService) ListPagedKnowledgeByKnowledgeBaseID(
	_ context.Context,
	_ string,
	page *types.Pagination,
	_ types.KnowledgeListFilter,
) (*types.PageResult, error) {
	return types.NewPageResult(int64(len(s.list)), page, s.list), nil
}

func (s *sourceACLTestKnowledgeService) find(id string) *types.Knowledge {
	for _, knowledge := range s.byID {
		if knowledge != nil && knowledge.ID == id {
			return knowledge
		}
	}
	return nil
}

type sourceACLTestKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *sourceACLTestKBService) GetKnowledgeBaseByID(
	context.Context,
	string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type sourceACLTestGuard struct {
	denyIDs map[string]struct{}
}

func (g *sourceACLTestGuard) CanRead(
	context.Context,
	interfaces.SourceACLGuardRequest,
) (*types.SourceACLDecision, error) {
	return &types.SourceACLDecision{Allowed: true}, nil
}

func (g *sourceACLTestGuard) RequireRead(
	_ context.Context,
	request interfaces.SourceACLGuardRequest,
) error {
	if g.denies(request.Knowledge) {
		return apperrors.NewForbiddenError("source ACL denied")
	}
	return nil
}

func (g *sourceACLTestGuard) FilterKnowledges(
	_ context.Context,
	_ string,
	knowledges []*types.Knowledge,
) ([]*types.Knowledge, error) {
	filtered := make([]*types.Knowledge, 0, len(knowledges))
	for _, knowledge := range knowledges {
		if !g.denies(knowledge) {
			filtered = append(filtered, knowledge)
		}
	}
	return filtered, nil
}

func (g *sourceACLTestGuard) FilterIndexCandidates(
	_ context.Context,
	_ string,
	candidates []*types.IndexWithScore,
) ([]*types.IndexWithScore, error) {
	return candidates, nil
}

func (g *sourceACLTestGuard) denies(knowledge *types.Knowledge) bool {
	if knowledge == nil {
		return false
	}
	_, denied := g.denyIDs[knowledge.ID]
	return denied
}

func newSourceACLKnowledgeRouter(
	kg interfaces.KnowledgeService,
	guard interfaces.SourceACLGuardService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "u-test")
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	handler := &KnowledgeHandler{
		kgService:      kg,
		kbService:      &sourceACLTestKBService{kb: &types.KnowledgeBase{ID: "kb1", TenantID: 1}},
		sourceACLGuard: guard,
	}
	router.GET("/knowledge/:id", handler.GetKnowledge)
	router.GET("/knowledge/:id/spans", handler.GetKnowledgeSpans)
	router.GET("/knowledge-bases/:id/knowledge", handler.ListKnowledge)
	return router
}

func TestKnowledgeDetailRequiresSourceACLRead(t *testing.T) {
	restricted := &types.Knowledge{ID: "restricted", TenantID: 1, KnowledgeBaseID: "kb1", Title: "restricted title"}
	router := newSourceACLKnowledgeRouter(
		&sourceACLTestKnowledgeService{byID: []*types.Knowledge{restricted}},
		&sourceACLTestGuard{denyIDs: map[string]struct{}{"restricted": {}}},
	)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/knowledge/restricted", nil)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if body := response.Body.String(); body == "" || strings.Contains(body, "restricted title") {
		t.Fatalf("detail response leaked restricted knowledge: %s", body)
	}
}

func TestKnowledgeSpansRequireSourceACLRead(t *testing.T) {
	restricted := &types.Knowledge{ID: "restricted", TenantID: 1, KnowledgeBaseID: "kb1"}
	router := newSourceACLKnowledgeRouter(
		&sourceACLTestKnowledgeService{byID: []*types.Knowledge{restricted}},
		&sourceACLTestGuard{denyIDs: map[string]struct{}{"restricted": {}}},
	)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/knowledge/restricted/spans", nil)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "parse_status") {
		t.Fatalf("spans response leaked trace payload: %s", response.Body.String())
	}
}

func TestListKnowledgeFiltersSourceACLDeniedEntries(t *testing.T) {
	public := &types.Knowledge{ID: "public", TenantID: 1, KnowledgeBaseID: "kb1", Title: "public title"}
	restricted := &types.Knowledge{ID: "restricted", TenantID: 1, KnowledgeBaseID: "kb1", Title: "restricted title"}
	router := newSourceACLKnowledgeRouter(
		&sourceACLTestKnowledgeService{list: []*types.Knowledge{public, restricted}},
		&sourceACLTestGuard{denyIDs: map[string]struct{}{"restricted": {}}},
	)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb1/knowledge", nil)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "public title") || strings.Contains(body, "restricted title") {
		t.Fatalf("list response did not filter restricted knowledge: %s", body)
	}
	if !strings.Contains(body, `"total":1`) {
		t.Fatalf("list total must not expose restricted row count: %s", body)
	}
}
