package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type personalKeyServiceStub struct {
	interfaces.TenantAPIKeyService
	created interfaces.TenantAPIKeyCreateRequest
	listed  []*types.TenantAPIKey
	revoked struct {
		tenantID uint64
		ownerID  string
		keyID    uint64
	}
}

func (s *personalKeyServiceStub) CreateAPIKey(
	_ context.Context, req interfaces.TenantAPIKeyCreateRequest,
) (*interfaces.TenantAPIKeyCreateResult, error) {
	s.created = req
	key := &types.TenantAPIKey{
		ID:               7,
		TenantID:         req.TenantID,
		OwnerUserID:      req.OwnerUserID,
		Name:             req.Name,
		KnowledgeBaseIDs: req.KnowledgeBaseIDs,
		Capabilities:     req.Capabilities,
		CreatedAt:        time.Now(),
	}
	return &interfaces.TenantAPIKeyCreateResult{APIKey: key, Token: "wk_personal_secret"}, nil
}

func (s *personalKeyServiceStub) ListPersonalAPIKeys(
	_ context.Context, _ uint64, _ string,
) ([]*types.TenantAPIKey, error) {
	return s.listed, nil
}

func (s *personalKeyServiceStub) RevokePersonalAPIKey(
	_ context.Context, tenantID uint64, ownerUserID string, keyID uint64,
) error {
	s.revoked.tenantID = tenantID
	s.revoked.ownerID = ownerUserID
	s.revoked.keyID = keyID
	return nil
}

type personalKeyUserServiceStub struct {
	interfaces.UserService
	user *types.User
}

func (s *personalKeyUserServiceStub) GetCurrentUser(context.Context) (*types.User, error) {
	return s.user, nil
}

type personalKeyKBServiceStub struct {
	interfaces.KnowledgeBaseService
	knowledgeBases map[string]*types.KnowledgeBase
}

func (s *personalKeyKBServiceStub) GetKnowledgeBaseByIDOnly(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	return s.knowledgeBases[id], nil
}

type personalKeyShareServiceStub struct {
	interfaces.KBShareService
	readable map[string]bool
}

func (s *personalKeyShareServiceStub) HasTenantKBPermission(
	_ context.Context, kbID string, _ uint64, _ types.TenantRole, _ types.OrgMemberRole,
) (bool, error) {
	return s.readable[kbID], nil
}

func personalKeyTestHandler(apiKeys *personalKeyServiceStub) *TenantHandler {
	return &TenantHandler{
		apiKeyService: apiKeys,
		userService: &personalKeyUserServiceStub{user: &types.User{
			ID: "viewer-1", Username: "viewer", Email: "viewer@example.com", IsActive: true,
		}},
		kbService: &personalKeyKBServiceStub{knowledgeBases: map[string]*types.KnowledgeBase{
			"own-kb":    {ID: "own-kb", TenantID: 42},
			"shared-kb": {ID: "shared-kb", TenantID: 99},
			"denied-kb": {ID: "denied-kb", TenantID: 99},
		}},
		kbShareService: &personalKeyShareServiceStub{readable: map[string]bool{"shared-kb": true}},
	}
}

func personalKeyTestRouter(handler *TenantHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	group := router.Group("/tenants/:id/me/api-keys")
	group.POST("", handler.CreatePersonalAPIKey)
	group.GET("", handler.ListPersonalAPIKeys)
	group.DELETE("/:key_id", handler.DeletePersonalAPIKey)
	return router
}

func TestCreatePersonalAPIKeyIgnoresForgedCapabilities(t *testing.T) {
	apiKeys := &personalKeyServiceStub{}
	router := personalKeyTestRouter(personalKeyTestHandler(apiKeys))
	body := map[string]any{
		"name": "my integration", "knowledge_base_ids": []string{"own-kb", "shared-kb", "own-kb"},
		"full_access": true, "capabilities": []string{"manage_models", "message_history"},
	}
	recorder := performPersonalKeyRequest(t, router, http.MethodPost, "/tenants/42/me/api-keys", body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if apiKeys.created.FullAccess {
		t.Fatal("personal key must never be full access")
	}
	assertStringsEqual(t, apiKeys.created.Capabilities, []string{"retrieve", "chat"})
	assertStringsEqual(t, apiKeys.created.KnowledgeBaseIDs, []string{"own-kb", "shared-kb"})
	if apiKeys.created.OwnerUserID == nil || *apiKeys.created.OwnerUserID != "viewer-1" {
		t.Fatalf("unexpected owner: %#v", apiKeys.created.OwnerUserID)
	}
}

func TestCreatePersonalAPIKeyRequiresExplicitAccessibleKnowledgeBases(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want int
	}{
		{name: "empty", ids: nil, want: http.StatusBadRequest},
		{name: "inaccessible shared knowledge base", ids: []string{"denied-kb"}, want: http.StatusForbidden},
		{name: "unknown knowledge base", ids: []string{"missing-kb"}, want: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := personalKeyTestRouter(personalKeyTestHandler(&personalKeyServiceStub{}))
			recorder := performPersonalKeyRequest(t, router, http.MethodPost, "/tenants/42/me/api-keys", map[string]any{
				"name": "restricted", "knowledge_base_ids": tc.ids,
			})
			if recorder.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestDeletePersonalAPIKeyScopesRevocationToCurrentUser(t *testing.T) {
	apiKeys := &personalKeyServiceStub{}
	router := personalKeyTestRouter(personalKeyTestHandler(apiKeys))
	recorder := performPersonalKeyRequest(t, router, http.MethodDelete, "/tenants/42/me/api-keys/9", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if apiKeys.revoked.tenantID != 42 || apiKeys.revoked.ownerID != "viewer-1" || apiKeys.revoked.keyID != 9 {
		t.Fatalf("unexpected revoke scope: %#v", apiKeys.revoked)
	}
}

func performPersonalKeyRequest(
	t *testing.T, router http.Handler, method, path string, body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertStringsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}
